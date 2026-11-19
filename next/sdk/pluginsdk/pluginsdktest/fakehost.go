package pluginsdktest

import (
	"cmp"
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// LogEntry is one record received through HostService.Log.
type LogEntry struct {
	Level   pluginv1.LogRequest_Level
	Message string
	Attrs   map[string]string
}

// LedgerEntry is one accepted ledger change.
type LedgerEntry struct {
	Credit bool
	Req    *pluginv1.LedgerChangeRequest
	ID     int64
}

// FakeHost is an in-memory HostService + EgressService for plugin tests;
// the comment of its HTTP client fields says where it differs from the host.
// Zero value is not usable; use NewFakeHost.
type FakeHost struct {
	pluginv1.UnimplementedHostServiceServer
	pluginv1.UnimplementedEgressServiceServer

	mu sync.Mutex
	// DSN returned by GetDSN ("" = FAILED_PRECONDITION).
	DSNValue    string
	SchemaValue string
	// Authz decides AuthzCheck; nil allows everything.
	Authz func(userID int64, permission string) bool
	// AccountScope decides AccountScopeCheck: whether the user reaches every
	// account for the action, or else which of ids are within their scope;
	// an error is returned as is (status.Error(codes.PermissionDenied, ...)
	// for a user holding the action at no scope). nil answers that the user
	// reaches all, so a test of a route that acts on accounts should set it
	// to cover a narrower scope and its 404. The arguments are checked as the core checks them
	// (known action, positive ids, batch size) before it is called. As on
	// the host, the call is PERMISSION_DENIED unless the user is the caller
	// of a request in flight: Harness.Do and DoAs see to that, a test that
	// calls a handler by itself uses Serve.
	AccountScope func(userID int64, action string, ids []int64) (all bool, allowed []int64, err error)
	// DialFunc is used by EgressService.Dial (default net.Dialer).
	DialFunc func(ctx context.Context, network, address string) (net.Conn, error)
	// The host's HTTP client (execution.go). A relay or fetch is accepted
	// only with the token of a live Execution, as Harness.Execute and
	// Harness.Monitor issue them; the fake applies the checks of the host
	// that need no network and records what it accepted. No real network is
	// used. Set these before Start.
	//
	// The checks are the host's: a relay or a fetch may reach the hosts the
	// manifest allows for the call's account (manifest.Manifest.
	// UpstreamHostAllowed: externalServices, or the hosts of the account's
	// upstreamFields; a relay to another host is refused with
	// INVALID_ARGUMENT, a fetch with PERMISSION_DENIED, as the host does), a
	// relay's method and headers are judged as the gateway judges them, and
	// Harness.Monitor refuses a terminal result without one complete,
	// successful fetch in the call (an HTTP status, no transport error).
	// Like the host, the fake bounds neither a relayed nor a fetched body,
	// does not count the fetches of a call and does not cut their answers. Where the
	// fake still differs from the host:
	//   - It cannot always tell a WebSocket turn from an HTTP request (the
	//     ExecuteRequest does not say): it refuses a ws(s) URL only when
	//     meta.protocol is a protocol it knows (the manifest's platforms or
	//     the built-in ones) with no WebSocket endpoint, and accepts one for
	//     an unknown protocol, which the host may refuse.
	//   - It makes no network check: the SSRF policy and the gateway's
	//     checks that need the account's binding (model availability, the
	//     response stream formats it decodes) are not simulated.
	//
	// RelayFunc scripts the upstream for an accepted relay. For one that
	// waits it returns the answer: Observed, UpstreamFailed or anything else
	// a test wants the plugin to cope with; nil answers UNIMPLEMENTED. For one
	// that does not wait only its error matters (nil accepts). An
	// INVALID_ARGUMENT error is a refusal: the request counts as not relayed.
	RelayFunc func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error)
	// ExecuteHTTPFunc answers a fetch; nil answers UNIMPLEMENTED. The answer
	// is returned whole, as the host does; a test may set transport_error
	// itself. A fetch of a route call
	// (Harness.DoAs) must carry a proxy ticket and one of an Execute or
	// Monitor call must not, as on the host; the fake cannot open a ticket
	// (the core seals it), so it takes any and the test sees it here.
	ExecuteHTTPFunc func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)
	// Manifest is the plugin's manifest as the host knows it, which decides
	// the hosts a relay or a fetch may reach and the protocols that are HTTP
	// routes. Start sets it from Options.Manifest when it is nil.
	// Without a manifest no relay or fetch is allowed unless
	// ExternalServices says so, and no account has upstream addresses.
	Manifest *manifest.Manifest
	// ExternalServices, when not nil, replaces the manifest's
	// externalServices (a subdomain of one matches), e.g. for a test that
	// runs the plugin without its manifest.
	ExternalServices []string
	// PriceUsageFunc answers PriceUsage; nil answers every model as not
	// priced ("no_price"). Set it before Start.
	PriceUsageFunc func(context.Context, *pluginv1.PriceUsageRequest) (*pluginv1.PriceUsageResponse, error)
	// ClearStickyBindingsFunc, ListNotificationChannelsFunc and NotifyFunc
	// answer the core calls of the same name; nil returns UNIMPLEMENTED. Set
	// them before Start.
	ClearStickyBindingsFunc      func(context.Context, *pluginv1.ClearStickyBindingsRequest) (*pluginv1.ClearStickyBindingsResponse, error)
	ListNotificationChannelsFunc func(context.Context, *pluginv1.ListNotificationChannelsRequest) (*pluginv1.ListNotificationChannelsResponse, error)
	NotifyFunc                   func(context.Context, *pluginv1.NotifyRequest) (*pluginv1.NotifyResponse, error)
	// Users answers GetUsers: the users among the requested ids, once each,
	// in request order. Set it before Start.
	Users map[int64]*pluginv1.UserInfo
	// Accounts answers GetAccount: the plugin's own accounts by id, as the
	// host returns them (credentials, settings, properties, proxy); any other
	// id is NOT_FOUND, as on the host. Each call is recorded in AccountReads,
	// the calls the host would audit. Set it before Start.
	Accounts     map[int64]*pluginv1.Account
	AccountReads []int64
	// OnPublish, when set, is called (outside the lock) for every accepted
	// Publish, e.g. to relay it to another Harness with Harness.Deliver and
	// simulate a second node. PublishErr, when set, fails every Publish.
	OnPublish  func(PublishedMessage)
	PublishErr error
	// LedgerErr, when set, fails every ledger change (set it with SetLedgerErr
	// while the plugin runs), e.g. UNAVAILABLE for a transient host error.
	LedgerErr error
	// Locks backs LockAcquire/LockRenew/LockRelease. Share one table between
	// two FakeHosts (set it before Start) to simulate two nodes; see
	// LockTable. LockErr, when set, fails every lock call (e.g. UNAVAILABLE
	// to simulate a Redis outage). The fake has no grants: lock calls are
	// never PERMISSION_DENIED unless LockErr says so. As on the host, a
	// PERMISSION_DENIED LockErr is returned before the arguments are checked
	// and any other LockErr after (invalid arguments win over it).
	//
	// LockAcquireLostReply, when set, makes LockAcquire take the lock as
	// usual (if it is free) and then answer with this error instead of the
	// result: an outcome the plugin cannot know, as when the host's reply is
	// lost. The lock stays held with the plugin's token until released.
	Locks                *LockTable
	LockErr              error
	LockAcquireLostReply error

	serving   map[int64]int // console users of the requests in flight
	logs      []LogEntry
	kv        map[string]kvItem
	kvCalls   map[string]int
	ledger    []LedgerEntry
	ledgerBy  map[string]LedgerEntry
	dials     []string
	published []PublishedMessage
	// settings: section -> values JSON (SetSettings).
	settings      map[string]string
	settingsReads int
	// executions: token -> the call it was issued for (execution.go).
	executions map[string]*Execution
}

// PublishedMessage is one broadcast received through HostService.Publish.
type PublishedMessage struct {
	Topic   string
	Payload []byte
}

type kvItem struct {
	value   []byte
	expires time.Time
}

// NewFakeHost returns an empty fake host.
func NewFakeHost() *FakeHost {
	return &FakeHost{kv: map[string]kvItem{}, kvCalls: map[string]int{}, ledgerBy: map[string]LedgerEntry{}, Locks: NewLockTable()}
}

// Logs returns a copy of the received log records.
func (f *FakeHost) Logs() []LogEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]LogEntry(nil), f.logs...)
}

// Ledger returns the accepted ledger changes.
func (f *FakeHost) Ledger() []LedgerEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]LedgerEntry(nil), f.ledger...)
}

// Dials returns the addresses requested through EgressService.
func (f *FakeHost) Dials() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dials...)
}

// Published returns the broadcasts received through Publish.
func (f *FakeHost) Published() []PublishedMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PublishedMessage(nil), f.published...)
}

// Publish records the broadcast (the real host relays it to the other
// nodes). It enforces the same topic and size limits as the host.
func (f *FakeHost) Publish(_ context.Context, in *pluginv1.PublishRequest) (*pluginv1.PublishResponse, error) {
	if !topicRe.MatchString(in.GetTopic()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid topic %q", in.GetTopic())
	}
	if len(in.GetPayload()) > 64<<10 {
		return nil, status.Error(codes.InvalidArgument, "payload too large")
	}
	msg := PublishedMessage{Topic: in.GetTopic(), Payload: append([]byte(nil), in.GetPayload()...)}
	f.mu.Lock()
	if err := f.PublishErr; err != nil {
		f.mu.Unlock()
		return nil, err
	}
	f.published = append(f.published, msg)
	hook := f.OnPublish
	f.mu.Unlock()
	if hook != nil {
		hook(msg)
	}
	return &pluginv1.PublishResponse{}, nil
}

var topicRe = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// SetDSN sets the value returned by GetDSN.
func (f *FakeHost) SetDSN(dsn, schema string) {
	f.mu.Lock()
	f.DSNValue, f.SchemaValue = dsn, schema
	f.mu.Unlock()
}

func (f *FakeHost) Log(_ context.Context, in *pluginv1.LogRequest) (*pluginv1.LogResponse, error) {
	f.mu.Lock()
	f.logs = append(f.logs, LogEntry{Level: in.GetLevel(), Message: in.GetMessage(), Attrs: in.GetAttrs()})
	f.mu.Unlock()
	return &pluginv1.LogResponse{}, nil
}

func kvKey(ns, key string) string { return ns + "\x00" + key }

// checkKVEntry applies the host's rules to the namespace and key of a single
// KV call; the single calls answer a broken rule with the host's error, like
// the batch calls (kvbatch.go).
func checkKVEntry(ns, key string) error {
	return cmp.Or(protocol.CheckKVNamespace(ns), protocol.CheckKVKey(key))
}

func (f *FakeHost) KVGet(_ context.Context, in *pluginv1.KVGetRequest) (*pluginv1.KVGetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kvCalls["KVGet"]++
	if err := checkKVEntry(in.GetNamespace(), in.GetKey()); err != nil {
		return nil, err
	}
	it, ok := f.kv[kvKey(in.GetNamespace(), in.GetKey())]
	if !ok || (!it.expires.IsZero() && time.Now().After(it.expires)) {
		return &pluginv1.KVGetResponse{}, nil
	}
	return &pluginv1.KVGetResponse{Found: true, Value: it.value}, nil
}

func (f *FakeHost) KVSet(_ context.Context, in *pluginv1.KVSetRequest) (*pluginv1.KVSetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kvCalls["KVSet"]++
	if err := cmp.Or(checkKVEntry(in.GetNamespace(), in.GetKey()), protocol.CheckKVValue(len(in.GetValue())), protocol.CheckKVTTL(in.GetTtlSeconds())); err != nil {
		return nil, err
	}
	it := kvItem{value: append([]byte(nil), in.GetValue()...)}
	if in.GetTtlSeconds() > 0 {
		it.expires = time.Now().Add(time.Duration(in.GetTtlSeconds()) * time.Second)
	}
	f.kv[kvKey(in.GetNamespace(), in.GetKey())] = it
	return &pluginv1.KVSetResponse{}, nil
}

func (f *FakeHost) KVDelete(_ context.Context, in *pluginv1.KVDeleteRequest) (*pluginv1.KVDeleteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kvCalls["KVDelete"]++
	if err := checkKVEntry(in.GetNamespace(), in.GetKey()); err != nil {
		return nil, err
	}
	delete(f.kv, kvKey(in.GetNamespace(), in.GetKey()))
	return &pluginv1.KVDeleteResponse{}, nil
}

func (f *FakeHost) KVList(_ context.Context, in *pluginv1.KVListRequest) (*pluginv1.KVListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kvCalls["KVList"]++
	if err := protocol.CheckKVNamespace(in.GetNamespace()); err != nil {
		return nil, err
	}
	var keys []string
	nsPrefix := in.GetNamespace() + "\x00"
	for k := range f.kv {
		if !strings.HasPrefix(k, nsPrefix) {
			continue
		}
		key := strings.TrimPrefix(k, nsPrefix)
		if strings.HasPrefix(key, in.GetPrefix()) && key > in.GetCursor() {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	limit := int(in.GetLimit())
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	next := ""
	if len(keys) > limit {
		keys = keys[:limit]
		next = keys[limit-1]
	}
	return &pluginv1.KVListResponse{Keys: keys, NextCursor: next}, nil
}

func (f *FakeHost) GetDSN(context.Context, *pluginv1.GetDSNRequest) (*pluginv1.GetDSNResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DSNValue == "" {
		return nil, status.Error(codes.FailedPrecondition, "fake host: no DSN configured")
	}
	return &pluginv1.GetDSNResponse{Dsn: f.DSNValue, Schema: f.SchemaValue}, nil
}

func (f *FakeHost) AuthzCheck(_ context.Context, in *pluginv1.AuthzCheckRequest) (*pluginv1.AuthzCheckResponse, error) {
	f.mu.Lock()
	fn := f.Authz
	f.mu.Unlock()
	return &pluginv1.AuthzCheckResponse{Allowed: fn == nil || fn(in.GetUserId(), in.GetPermission())}, nil
}

func (f *FakeHost) ledgerChange(in *pluginv1.LedgerChangeRequest, credit bool) (*pluginv1.LedgerChangeResponse, error) {
	if in.GetIdempotencyKey() == "" || in.GetAmount() == "" {
		return nil, status.Error(codes.InvalidArgument, "amount and idempotency_key are required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LedgerErr != nil {
		return nil, f.LedgerErr
	}
	if e, ok := f.ledgerBy[in.GetIdempotencyKey()]; ok {
		return &pluginv1.LedgerChangeResponse{LedgerId: e.ID, Duplicate: true}, nil
	}
	e := LedgerEntry{Credit: credit, Req: in, ID: int64(len(f.ledger) + 1)}
	f.ledger = append(f.ledger, e)
	f.ledgerBy[in.GetIdempotencyKey()] = e
	return &pluginv1.LedgerChangeResponse{LedgerId: e.ID}, nil
}

func (f *FakeHost) LedgerCredit(_ context.Context, in *pluginv1.LedgerChangeRequest) (*pluginv1.LedgerChangeResponse, error) {
	return f.ledgerChange(in, true)
}

func (f *FakeHost) LedgerDebit(_ context.Context, in *pluginv1.LedgerChangeRequest) (*pluginv1.LedgerChangeResponse, error) {
	return f.ledgerChange(in, false)
}

// errDialReported marks a dial failure that dialAndOpen already sent to the
// plugin as a DialResult. The stream ends normally in that case (a failed dial
// is not a stream error); Dial must not treat the missing conn as usable.
var errDialReported = errors.New("fake host: dial failure already reported")

// Dial implements a direct (unfiltered) egress tunnel.
func (f *FakeHost) Dial(stream pluginv1.EgressService_DialServer) error {
	conn, err := f.dialAndOpen(stream)
	if errors.Is(err, errDialReported) {
		return nil
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Result{Result: &pluginv1.DialResult{Ok: true, RemoteAddr: conn.RemoteAddr().String()}}}); err != nil {
		return err
	}
	var sendMu sync.Mutex
	done := make(chan struct{})
	go f.pumpToHost(stream, conn, &sendMu, done)
	f.pumpToConn(stream, conn, done)
	return nil
}

// dialAndOpen reads the open frame, records the dial and opens the connection.
// A failed dial is reported to the plugin as a DialResult and is not an error
// for the stream.
func (f *FakeHost) dialAndOpen(stream pluginv1.EgressService_DialServer) (net.Conn, error) {
	first, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	open := first.GetOpen()
	if open == nil {
		return nil, status.Error(codes.InvalidArgument, "first frame must be open")
	}
	f.mu.Lock()
	f.dials = append(f.dials, open.GetAddress())
	dial := f.DialFunc
	f.mu.Unlock()
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	conn, err := dial(stream.Context(), open.GetNetwork(), open.GetAddress())
	if err != nil {
		if serr := stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Result{Result: &pluginv1.DialResult{Ok: false, Error: err.Error()}}}); serr != nil {
			return nil, serr
		}
		return nil, errDialReported
	}
	return conn, nil
}

// pumpToHost copies what the upstream connection returns into Egress Data
// frames, ending with one Close frame (an empty error at EOF). It stops
// silently when the stream is gone.
func (f *FakeHost) pumpToHost(stream pluginv1.EgressService_DialServer, conn net.Conn, sendMu *sync.Mutex, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := conn.Read(buf)
		if n > 0 {
			sendMu.Lock()
			serr := stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Data{Data: append([]byte(nil), buf[:n]...)}})
			sendMu.Unlock()
			if serr != nil {
				return
			}
		}
		if rerr != nil {
			msg := ""
			if !errors.Is(rerr, io.EOF) {
				msg = rerr.Error()
			}
			sendMu.Lock()
			_ = stream.Send(&pluginv1.EgressFrame{Kind: &pluginv1.EgressFrame_Close{Close: &pluginv1.DialClose{Error: msg}}})
			sendMu.Unlock()
			return
		}
	}
}

// pumpToConn copies Egress Data frames into the upstream connection until the
// stream ends or a write fails; both close the connection and wait for the
// read goroutine to finish. A Close frame half-closes the write side when the
// connection supports it.
func (f *FakeHost) pumpToConn(stream pluginv1.EgressService_DialServer, conn net.Conn, done <-chan struct{}) {
	for {
		fr, err := stream.Recv()
		if err != nil {
			_ = conn.Close()
			<-done
			return
		}
		switch k := fr.GetKind().(type) {
		case *pluginv1.EgressFrame_Data:
			if _, err := conn.Write(k.Data); err != nil {
				_ = conn.Close()
				<-done
				return
			}
		case *pluginv1.EgressFrame_Close:
			if cw, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			} else {
				_ = conn.Close()
			}
		}
	}
}

// SetLedgerErr sets LedgerErr while the plugin may be calling the host.
func (f *FakeHost) SetLedgerErr(err error) {
	f.mu.Lock()
	f.LedgerErr = err
	f.mu.Unlock()
}
