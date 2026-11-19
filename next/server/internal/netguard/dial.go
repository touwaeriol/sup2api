package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// AddrError is returned by DialControl when a connection would reach a
// non-public address. It matches ErrPrivate with errors.Is. The check runs
// on the resolved address at dial time, so DNS rebinding cannot bypass it.
type AddrError struct {
	Address string // "ip:port" the dialer was about to connect to
}

func (e *AddrError) Error() string {
	return "address " + e.Address + " is private, loopback or otherwise non-public and not allowed"
}

// Is makes errors.Is(err, ErrPrivate) true.
func (e *AddrError) Is(target error) bool { return target == ErrPrivate }

// DialControl is a net.Dialer.Control that refuses non-public addresses.
// address is the resolved "ip:port"; anything unparsable is refused.
func DialControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || BlockedAddr(ap.Addr()) {
		return &AddrError{Address: address}
	}
	return nil
}

// Dialer returns a dialer that refuses non-public addresses unless
// allowPrivate.
func Dialer(timeout time.Duration, allowPrivate bool) *net.Dialer {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = DialControl
	}
	return d
}

// ClientOptions configure NewClient. Zero values select the defaults.
type ClientOptions struct {
	// AllowPrivate disables the address checks (test setups only).
	AllowPrivate bool
	// Timeout bounds a whole request (default 30 s).
	Timeout time.Duration
	// MaxRedirects is the number of redirects followed (default 5; <0 = none).
	MaxRedirects int
	// Lookup resolves names for the per-hop redirect check (default
	// DefaultLookup).
	Lookup Lookup
	// Proxy picks the proxy of each request as http.Transport.Proxy does
	// (default: none, every request direct). The proxy resolves a proxied
	// request's target, so only a literal private target is refused
	// (CheckHostVia), and the dial to the proxy is not guarded: the proxy is
	// the operator's, like an account proxy saved with allow_private.
	Proxy ProxyFunc
}

// ErrTooManyRedirects is returned when a guarded client stops following.
var ErrTooManyRedirects = errors.New("too many redirects")

var errNoProxy = errors.New("no proxy for a request routed through one")

// NewClient returns an http.Client for server-side fetches of addresses an
// operator or a plugin supplied: direct connections only unless o.Proxy
// (never the environment's proxy by itself), every direct dial refused for
// non-public addresses, and every redirect target checked again before it
// is followed.
func NewClient(o ClientOptions) *http.Client {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}
	var rt http.RoundTripper = newTransport(o.Timeout, o.AllowPrivate)
	if o.Proxy != nil {
		proxied := newTransport(o.Timeout, true)
		proxied.Proxy = func(r *http.Request) (*url.URL, error) {
			// This transport's dials are unguarded: going direct here is
			// never right, even if o.Proxy changed its mind.
			if p, err := o.Proxy(r.URL); err != nil || p != nil {
				return p, err
			}
			return nil, errNoProxy
		}
		rt = splitTransport{proxy: o.Proxy, direct: rt, proxied: proxied, allowPrivate: o.AllowPrivate}
	}
	return &http.Client{Transport: rt, Timeout: o.Timeout, CheckRedirect: redirectCheck(o)}
}

func newTransport(timeout time.Duration, allowPrivate bool) *http.Transport {
	return &http.Transport{
		DialContext:           Dialer(15*time.Second, allowPrivate).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
}

// splitTransport sends a request through proxied when proxy picks a proxy
// for it (refusing a literal private target unless allowPrivate), else
// through direct, whose dials are guarded.
type splitTransport struct {
	proxy           ProxyFunc
	direct, proxied http.RoundTripper
	allowPrivate    bool
}

func (s splitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, err := s.proxy(req.URL)
	switch {
	case err == nil && p == nil:
		return s.direct.RoundTrip(req)
	case err == nil && !s.allowPrivate && LiteralPrivate(req.URL.Hostname()):
		err = ErrPrivate
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close() // a RoundTripper always closes it
		}
		return nil, err
	}
	return s.proxied.RoundTrip(req)
}

func redirectCheck(o ClientOptions) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > o.MaxRedirects {
			return ErrTooManyRedirects
		}
		ctx := req.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		_, err := CheckURLProxy(ctx, req.URL.String(), o.AllowPrivate, o.Proxy, o.Lookup)
		return err
	}
}
