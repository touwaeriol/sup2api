// Package egress implements the host side of EgressService: every outbound
// connection of a plugin is tunnelled through the core, checked against the
// plugin's egress policy, forwarded and logged to plugin_egress_logs.
package egress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// DNSHost/DNSPort is the in-process DNS-over-TCP endpoint the plugin SDK
// resolver dials.
const (
	DNSHost = "dns.sub2api"
	DNSPort = 53
)

// MaxFrameData is the largest data payload per frame.
const MaxFrameData = 32 << 10

// Policy modes.
const (
	PolicyAllowAll  = "allow_all"
	PolicyAllowlist = "allowlist"
)

// Log results (plugin_egress_logs.result).
const (
	ResultOK        = "ok"
	ResultDenied    = "denied"
	ResultDialError = "dial_error"
	ResultReset     = "reset"
)

// Options configures a Provider. Zero values use the defaults.
type Options struct {
	NodeID      string
	DialTimeout time.Duration // default 10 s
	// DatabaseAddrs lists the "host:port" of the PostgreSQL server in the
	// DSN HostService.GetDSN hands out without strict network (the core's
	// DSN; under strict network a plugin gets the virtual address instead,
	// DatabaseProxy). A plugin reaches it through the tunnel only when it
	// installs the tunnel on a host that does not require it
	// (pluginsdk.WithStrictNetwork(StrictAlways)). Only a plugin whose
	// policy has Database may reach it, by exactly this name; it is dialled
	// by name.
	DatabaseAddrs []string
	// DatabaseProxy serves a connection to DatabaseHost:DatabasePort of a
	// plugin whose policy has Database (database.go); without it that
	// address is refused.
	DatabaseProxy func(ctx context.Context, conn net.Conn, pluginKey string)
	// CoreAddrs lists "host:port" targets no plugin may reach under any
	// policy, AllowPrivate included: the core's Redis (and PostgreSQL for
	// plugins without Database). Names are resolved, so an IP literal or
	// another name of the same address is refused too.
	CoreAddrs []string
	// AllowPrivate lets plugins reach private, loopback, link-local and
	// other non-public addresses (SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE).
	// Tests and development only; CoreAddrs stay refused.
	AllowPrivate bool
	// Dial overrides the outbound dialer (tests).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// LookupIP overrides the resolver used for dns.sub2api (tests).
	LookupIP      func(ctx context.Context, host string) ([]netip.Addr, error)
	FlushInterval time.Duration // log batch interval, default 2 s
	BatchSize     int           // default 500
	QueueSize     int           // default 10000; entries beyond are dropped
	Retention     time.Duration // default 7 days; 0 keeps the default, <0 disables pruning
	// DomainUpdateInterval bounds how often the plugin_egress_domains row of
	// a known host is updated by this node (default 1 minute). A host this
	// node has not written yet is written at the next flush.
	DomainUpdateInterval time.Duration
	// Events receives plugin.egress_new_domain when a plugin connects to a
	// host for the first time (optional; without it only a WARN is logged).
	Events core.EventPublisher
	// Bus carries config:changed: a save of plugin network access on any
	// node drops this node's cached copy (optional; single node, tests).
	Bus core.Bus
	// LoadHostPolicy overrides how plugin network access is read (tests).
	// Without it and without a database every host passes it.
	LoadHostPolicy func(context.Context) (HostPolicy, error)
	Logger         *slog.Logger
}

// Provider implements core.EgressProvider.
type Provider struct {
	opts     Options
	log      *slog.Logger
	logs     *logWriter
	database map[string]bool // normalized host:port
	core     *coreAddrs
	db       *store.DB     // plugin network access API; nil in unit tests
	hosts    *hostPolicies // plugin network access, cached
	unsub    func()
}

var (
	_ core.EgressProvider      = (*Provider)(nil)
	_ core.PluginNetworkAccess = (*Provider)(nil)
)

// New builds a provider and starts its log writer. db may be nil (logs are
// discarded). Call Close on shutdown to flush pending logs.
func New(db *store.DB, opts Options) *Provider {
	var st logStore
	if db != nil {
		st = pgStore{db: db}
		if opts.LoadHostPolicy == nil {
			opts.LoadHostPolicy = func(ctx context.Context) (HostPolicy, error) { return loadHostPolicy(ctx, db.Gorm) }
		}
	}
	p := newProvider(st, opts)
	p.db = db
	return p
}

func newProvider(st logStore, opts Options) *Provider {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Dial == nil {
		d := &net.Dialer{Timeout: opts.DialTimeout, KeepAlive: 30 * time.Second}
		opts.Dial = d.DialContext
	}
	if opts.LookupIP == nil {
		opts.LookupIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
			ips, err := netguard.DefaultLookup(ctx, host)
			if err != nil {
				return nil, err
			}
			addrs := make([]netip.Addr, 0, len(ips))
			for _, ip := range ips {
				if addr, ok := netip.AddrFromSlice(ip); ok {
					addrs = append(addrs, addr)
				}
			}
			return addrs, nil
		}
	}
	p := &Provider{opts: opts, log: opts.Logger, database: map[string]bool{}}
	for _, a := range opts.DatabaseAddrs {
		if k, ok := addrKey(a); ok {
			p.database[k] = true
		}
	}
	// The database is a core service too: reachable only through the
	// Database exception, by its configured name.
	p.core = newCoreAddrs(append(append([]string(nil), opts.CoreAddrs...), opts.DatabaseAddrs...), opts.LookupIP)
	p.logs = newLogWriter(st, opts)
	load := opts.LoadHostPolicy
	if load == nil {
		load = func(context.Context) (HostPolicy, error) { return DefaultHostPolicy(), nil }
	}
	p.hosts = newHostPolicies(load, opts.Logger)
	if opts.Bus != nil {
		p.unsub = opts.Bus.Subscribe(core.ChannelConfigChanged, func(payload []byte) {
			var msg struct{ Key string }
			if json.Unmarshal(payload, &msg) == nil && msg.Key == hostConfigKey {
				p.hosts.invalidate()
			}
		})
	}
	return p
}

// LogsPruneTask is the cluster task that deletes old egress log rows.
const LogsPruneTask = "core.egress.logs_prune"

// LogsPruneTaskSpec deletes the log rows past the retention hourly, on one
// node (every node used to).
func (p *Provider) LogsPruneTaskSpec() core.TaskSpec {
	return core.TaskSpec{
		Name:  LogsPruneTask,
		Title: core.LocalizedText{"en": "Delete old egress logs", "zh": "删除 7 天前出口日志"},
		Description: core.LocalizedText{
			"en": "Deletes plugin egress connection logs older than their retention.",
			"zh": "删除超过保留期的插件出口连接日志。",
		},
		Scope:   core.TaskCluster,
		Every:   time.Hour,
		Timeout: 5 * time.Minute,
		Handler: func(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
			return core.TaskResult{}, p.logs.prune(ctx)
		},
	}
}

// addrKey normalizes "host:port" for comparisons.
func addrKey(a string) (string, bool) {
	h, port, err := net.SplitHostPort(strings.TrimSpace(a))
	if err != nil || h == "" || port == "" {
		return "", false
	}
	return net.JoinHostPort(normalizeHost(h), port), true
}

// Close stops the log writer after flushing pending entries; rows of
// connections still open are marked reset.
func (p *Provider) Close() {
	if p.unsub != nil {
		p.unsub()
	}
	p.logs.shutdown()
}

// Flush writes pending log entries now.
func (p *Provider) Flush(ctx context.Context) error { return p.logs.flushNow(ctx) }

// ServerFor returns the EgressService served to one plugin instance.
// policy is read on every new connection so policy changes apply at once.
// A nil policy refuses everything.
func (p *Provider) ServerFor(pluginKey string, policy func() core.EgressPolicy) pluginv1.EgressServiceServer {
	if policy == nil {
		policy = func() core.EgressPolicy { return core.EgressPolicy{} }
	}
	return &server{p: p, key: pluginKey, policy: policy}
}

type server struct {
	pluginv1.UnimplementedEgressServiceServer
	p      *Provider
	key    string
	policy func() core.EgressPolicy
}

func sendResult(stream pluginv1.EgressService_DialServer, ok bool, errMsg, remote string) error {
	return stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Result{
		Result: &pluginv1.DialResult{Ok: ok, Error: errMsg, RemoteAddr: remote},
	}})
}

// Dial serves one tunnelled connection.
func (s *server) Dial(stream pluginv1.EgressService_DialServer) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	if open == nil {
		_ = sendResult(stream, false, "first frame must be open", "")
		return status.Error(codes.InvalidArgument, "egress: first frame must be open")
	}
	start := time.Now()
	entry := logEntry{PluginKey: s.key, NodeID: s.p.opts.NodeID, Network: open.Network, StartedAt: start}
	finish := func(result, msg string) {
		entry.Result, entry.Error = result, msg
		entry.DurationMS = int(time.Since(start).Milliseconds())
		s.p.logs.add(entry)
	}

	host, portStr, err := net.SplitHostPort(open.Address)
	port, perr := strconv.Atoi(portStr)
	if err != nil || perr != nil || host == "" || port <= 0 || port > 65535 {
		entry.Host = truncate(open.Address, 255)
		finish(ResultDenied, "invalid address")
		return sendResult(stream, false, "egress: invalid address "+strconv.Quote(open.Address), "")
	}
	host = normalizeHost(host)
	entry.Host, entry.Port = truncate(host, 255), port
	switch open.Network {
	case "tcp", "tcp4", "tcp6":
	default:
		finish(ResultDenied, "unsupported network")
		return sendResult(stream, false, "egress: only tcp is supported", "")
	}
	pol := s.policy()
	isDNS := host == DNSHost && port == DNSPort
	isDB := host == DatabaseHost && port == DatabasePort
	if !isDNS && !isDB {
		// Every connection attempt counts for the plugin's domain list
		// (denied ones included: they show what the plugin tries to reach).
		// The core's own services are not external hosts.
		s.p.logs.observe(s.key, entry.Host, port)
	}

	var conn net.Conn
	if isDNS {
		if !pol.Net {
			finish(ResultDenied, "no net grant")
			return sendResult(stream, false, "egress: the plugin has no net permission", "")
		}
		// In-process DNS-over-TCP; queries are logged individually.
		c1, c2 := net.Pipe()
		go s.p.serveDNS(ctx, c2, s.key, pol)
		conn = c1
	} else if isDB {
		if !pol.Database || s.p.opts.DatabaseProxy == nil {
			finish(ResultDenied, "no database")
			return sendResult(stream, false, "egress: the plugin has no database", "")
		}
		conn = s.serveDatabase(ctx)
	} else {
		resolved, reason, msg := s.p.check(ctx, pol, host, port)
		if reason != "" {
			result := ResultDenied
			if reason == "resolve" {
				result = ResultDialError
			}
			finish(result, msg)
			return sendResult(stream, false, "egress: "+msg, "")
		}
		err = errors.New("no address of " + host + " matches network " + open.Network)
		var targets []string
		if resolved == nil {
			// The configured database address: dialled by its name.
			targets = []string{net.JoinHostPort(host, portStr)}
		} else {
			targets = dialTargets(open.Network, resolved, portStr)
		}
		for _, target := range targets {
			dctx, cancel := context.WithTimeout(ctx, s.p.opts.DialTimeout)
			conn, err = s.p.opts.Dial(dctx, open.Network, target)
			cancel()
			if err == nil {
				break
			}
		}
		if err != nil {
			finish(ResultDialError, err.Error())
			return sendResult(stream, false, "egress: "+err.Error(), "")
		}
	}
	defer conn.Close()
	// The connection is logged at once (result 'open') and updated when it
	// closes, so long-lived connections are visible while they last.
	var oc *openConn
	if !isDNS {
		oc = s.p.logs.open(entry)
	}
	remote := ""
	if a := conn.RemoteAddr(); a != nil {
		remote = a.String()
	}
	if err := sendResult(stream, true, "", remote); err != nil {
		if oc != nil {
			s.p.logs.close(oc, ResultReset, err.Error(), 0, 0, start)
		}
		return err
	}
	in, out, ferr := pump(ctx, stream, conn)
	if isDNS {
		return nil
	}
	if ferr != nil {
		s.p.logs.close(oc, ResultReset, ferr.Error(), in, out, start)
	} else {
		s.p.logs.close(oc, ResultOK, "", in, out, start)
	}
	return nil
}

// pump copies both directions until both are finished or the stream ends.
// in counts remote->plugin bytes, out plugin->remote bytes.
func pump(ctx context.Context, stream pluginv1.EgressService_DialServer, conn net.Conn) (in, out int64, err error) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	upErr := make(chan error, 1)
	downErr := make(chan error, 1)

	// plugin -> remote
	go func() {
		for {
			f, err := stream.Recv()
			if err == io.EOF {
				closeWrite(conn)
				upErr <- nil
				return
			}
			if err != nil {
				_ = conn.Close()
				upErr <- err
				return
			}
			switch k := f.Kind.(type) {
			case *pluginv1.EgressFrame_Data:
				n, werr := conn.Write(k.Data)
				out += int64(n)
				if werr != nil {
					_ = conn.Close()
					upErr <- werr
					return
				}
			case *pluginv1.EgressFrame_Close:
				if k.Close.GetError() != "" {
					_ = conn.Close()
				} else {
					closeWrite(conn)
				}
				upErr <- nil
				return
			}
		}
	}()

	// remote -> plugin
	go func() {
		buf := make([]byte, MaxFrameData)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				if serr := stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Data{Data: data}}); serr != nil {
					_ = conn.Close()
					downErr <- serr
					return
				}
				in += int64(n)
			}
			if rerr != nil {
				msg := ""
				var res error
				if !errors.Is(rerr, io.EOF) {
					msg, res = rerr.Error(), rerr
				}
				_ = stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Close{Close: &pluginv1.DialClose{Error: msg}}})
				downErr <- res
				return
			}
		}
	}()

	e1 := <-upErr
	e2 := <-downErr
	if ctx.Err() != nil {
		// The plugin tore the stream down; not an error of the target.
		return in, out, nil
	}
	if e2 != nil && !errors.Is(e2, net.ErrClosed) {
		return in, out, e2
	}
	if e1 != nil && !errors.Is(e1, net.ErrClosed) && status.Code(e1) != codes.Canceled {
		return in, out, e1
	}
	return in, out, nil
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func truncate(s string, n int) string { return x.Truncate(s, n) }

// check decides one TCP connection. It returns the addresses to dial (nil
// with an empty reason: the configured database address, dialled by name),
// or a reason ("denied" or "resolve") and a message.
//
// Order: the database exception (Database policy, exact configured name);
// the net grant; the plugin's domain policy; the administrator's plugin
// network access (both must pass); then the resolved addresses - every one
// must be public (unless AllowPrivate) and none may be a core service. The
// addresses that were checked are the ones dialled: resolving the name again
// at dial time would let a DNS answer that changes in between (rebinding)
// reach an address that was never checked.
func (p *Provider) check(ctx context.Context, pol core.EgressPolicy, host string, port int) ([]netip.Addr, string, string) {
	hp := net.JoinHostPort(host, strconv.Itoa(port))
	if pol.Database && p.database[hp] {
		return nil, "", ""
	}
	if !pol.Net {
		return nil, ResultDenied, "the plugin has no net permission"
	}
	if !AllowedHost(pol, host) {
		return nil, ResultDenied, host + " is not allowed by the plugin egress policy"
	}
	if msg := p.hostRefusal(ctx, host); msg != "" {
		return nil, ResultDenied, msg
	}
	if p.core.matchName(hp) {
		return nil, ResultDenied, hp + " is a core service address"
	}
	var resolved []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		resolved = []netip.Addr{addr}
	} else {
		var err error
		if resolved, err = p.opts.LookupIP(ctx, host); err != nil {
			return nil, "resolve", "resolve " + host + ": " + err.Error()
		}
	}
	if len(resolved) == 0 {
		return nil, "resolve", "resolve " + host + ": no addresses"
	}
	for _, addr := range resolved {
		if !p.opts.AllowPrivate && netguard.BlockedAddr(addr) {
			return nil, ResultDenied, host + " resolved to non-public address " + addr.Unmap().String() + " and is not allowed"
		}
		if p.core.matchAddr(ctx, addr, port) {
			return nil, ResultDenied, host + " resolved to a core service address"
		}
	}
	return resolved, "", ""
}

// hostRefusal is why the administrator's plugin network access refuses
// host, "" when it allows it.
func (p *Provider) hostRefusal(ctx context.Context, host string) string {
	h, err := p.hosts.get(ctx)
	if err != nil {
		return err.Error()
	}
	return h.refusal(host)
}

// PluginRequestRefusal implements core.PluginNetworkAccess: the tunnel's
// decision (hostRefusal) for a request the core sends for the plugin. A
// refusal is queued as a denied row of the plugin's egress log, its network
// the URL's scheme (a tunnelled connection's is tcp); allowed requests are
// not logged here (they are the usage records' and the gateway's).
func (p *Provider) PluginRequestRefusal(ctx context.Context, pluginKey string, target *url.URL) string {
	host := normalizeHost(target.Hostname())
	msg := p.hostRefusal(ctx, host)
	if msg != "" {
		port, err := strconv.Atoi(target.Port())
		if err != nil {
			port = 443
			if target.Scheme == "http" {
				port = 80
			}
		}
		p.logs.add(logEntry{PluginKey: pluginKey, NodeID: p.opts.NodeID, Network: target.Scheme, Host: truncate(host, 255),
			Port: port, StartedAt: time.Now(), Result: ResultDenied, Error: msg})
	}
	return msg
}

// AllowedHost reports whether host may be reached under pol. In allowlist
// mode host must equal an entry or match a "*.example.com" wildcard (any
// subdomain depth, not the apex). Matching is case-insensitive.
func AllowedHost(pol core.EgressPolicy, host string) bool {
	if pol.Mode != PolicyAllowlist {
		return true
	}
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	for _, d := range pol.AllowedDomains {
		d = normalizeHost(d)
		if d == "" {
			continue
		}
		if d == "*" {
			return true
		}
		if suffix, ok := strings.CutPrefix(d, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == d {
			return true
		}
	}
	return false
}

// dialTargets turns resolved addresses into host:port targets for network
// ("tcp4" keeps IPv4, "tcp6" IPv6, "tcp" both, in resolver order).
func dialTargets(network string, addrs []netip.Addr, port string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap()
		if (network == "tcp4" && !a.Is4()) || (network == "tcp6" && !a.Is6()) {
			continue
		}
		out = append(out, net.JoinHostPort(a.String(), port))
	}
	return out
}
