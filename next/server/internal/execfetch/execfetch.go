// Package execfetch is the fetch form of plugin execution
// (core.ExecutionHTTP): one HTTP exchange whose answer goes back to the
// plugin. The gateway serves the fetches of Execute and the usage module
// those of Monitor through this one implementation, each with its own
// binding (the account's client, the SSRF policy, its base timeouts, an
// admission), so the rules for the request and the answer cannot drift
// apart. It also holds the header rules every request a plugin builds
// shares with the relay (RelayTransportHeaders, ValidHeader).
package execfetch

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// TransportFailure is the plugin's text for an exchange that failed in
// transport; the cause, which may name the proxy, stays in the host's log.
const TransportFailure = "execution HTTP request failed"

// RelayTransportHeaders are the request headers the host's transport decides
// for a relayed request (UpstreamRequest); a relay naming one is refused.
// Host is the URL's: a request goes to, and names, the host that was checked
// (the SSRF policy, the account's proxy, the allowed upstream hosts).
var RelayTransportHeaders = map[string]bool{"host": true, "content-length": true, "accept-encoding": true, "connection": true, "transfer-encoding": true}

// FetchTransportHeaders are the headers a fetch may not set: they would
// change where or how the request travels (Host against the checked URL,
// hop-by-hop and proxy headers). A fetch naming one is refused.
// Accept-Encoding is not refused but dropped (fetchDroppedHeader).
var FetchTransportHeaders = map[string]bool{"host": true, "connection": true, "proxy-connection": true, "proxy-authorization": true,
	"proxy-authenticate": true, "upgrade": true, "transfer-encoding": true, "trailer": true, "te": true, "content-length": true}

// fetchDroppedHeader is left out of a fetch: the transport negotiates the
// encoding and decodes the answer.
const fetchDroppedHeader = "accept-encoding"

// ValidHeader reports whether name and value form a header net/http can
// send; one that is not would fail the exchange after it started.
func ValidHeader(name, value string) bool {
	return httpguts.ValidHeaderFieldName(name) && httpguts.ValidHeaderFieldValue(value)
}

// NoRedirects is a copy of client that returns a redirect as the answer,
// without a client-wide timeout: the exchange's own timers bound it.
func NoRedirects(client *http.Client) *http.Client {
	hc := *client
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Timeout = 0
	return &hc
}

// FirstHeaderValues is h as a map of lower-case names to their first value,
// the form plugins receive headers in.
func FirstHeaderValues(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[strings.ToLower(k)] = v[0]
		}
	}
	return out
}

// Binding is what the host binds a fetch to; none of it comes from the
// plugin.
type Binding struct {
	// CheckURL applies the SSRF policy (netguard) to the request's URL.
	CheckURL func(ctx context.Context, raw string) (*url.URL, error)
	// Client is the bound account's HTTP client (its proxy). The fetch uses
	// a copy that follows no redirect and has no client-wide timeout.
	Client func(ctx context.Context) (*http.Client, error)
	// HeaderTimeout and TotalTimeout are the host's. <= 0 is none.
	HeaderTimeout, TotalTimeout time.Duration
	// Admit, when set, is asked right before the exchange; its error is
	// returned as the fetch's error and nothing is sent.
	Admit func(context.Context) error
}

// Do performs one fetch: no redirects, no retries, the answer whole. A
// request the host refuses (a header that is not HTTP, Host, a hop-by-hop
// or proxy header) or an
// exchange that fails is an answer with TransportError set; an error is the
// context's or Admit's.
func Do(ctx context.Context, in *pluginv1.ExecutionHTTPRequest, b Binding) (*pluginv1.ExecutionHTTPResponse, error) {
	failure := func(msg string) (*pluginv1.ExecutionHTTPResponse, error) {
		return &pluginv1.ExecutionHTTPResponse{TransportError: msg}, nil
	}
	method, msg := checkRequest(in)
	if msg != "" {
		return failure(msg)
	}
	if b.TotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.TotalTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The way out first: without it nothing is sent, and the URL check
	// would resolve the host here for nothing.
	hc, err := b.Client(ctx)
	if err != nil || hc == nil {
		return failure("execution account proxy is unavailable")
	}
	u, err := b.CheckURL(ctx, in.GetUrl())
	if err != nil {
		return failure("execution URL rejected by host policy")
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(in.GetBody()))
	if err != nil {
		return failure("invalid execution HTTP request")
	}
	for k, v := range in.GetHeaders() {
		if !strings.EqualFold(k, fetchDroppedHeader) {
			req.Header.Set(k, v)
		}
	}
	if len(in.GetBody()) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := NoRedirects(hc)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if b.Admit != nil {
		if err := b.Admit(ctx); err != nil {
			return nil, err
		}
	}
	// A response header timeout cuts the exchange when no answer started in
	// time; it is the total timeout's to bound the rest.
	stop := func() bool { return false }
	if b.HeaderTimeout > 0 {
		stop = time.AfterFunc(b.HeaderTimeout, cancel).Stop
	}
	resp, err := client.Do(req)
	stop()
	return answer(ctx, u, resp, err)
}

// checkRequest is the request's method, or why the host refuses it: a
// header that is not HTTP, or one that would change where or how the
// request travels (Host against the checked URL, hop-by-hop and proxy
// headers). The method is net/http's to judge.
func checkRequest(in *pluginv1.ExecutionHTTPRequest) (method, refused string) {
	if in == nil {
		return "", "execution request is missing"
	}
	method = strings.ToUpper(strings.TrimSpace(in.GetMethod()))
	if method == "" {
		method = http.MethodGet
	}
	for k, v := range in.GetHeaders() {
		if !ValidHeader(k, v) {
			return "", "invalid execution request headers"
		}
		if FetchTransportHeaders[strings.ToLower(k)] {
			return "", "execution request contains a forbidden transport header"
		}
	}
	return method, ""
}

// answer reads the exchange's answer.
func answer(ctx context.Context, u *url.URL, resp *http.Response, err error) (*pluginv1.ExecutionHTTPResponse, error) {
	if err != nil {
		slog.InfoContext(ctx, "execfetch: exchange failed", "host", u.Host, "err", err)
		return &pluginv1.ExecutionHTTPResponse{TransportError: TransportFailure}, nil
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	out := &pluginv1.ExecutionHTTPResponse{Status: int32(resp.StatusCode), Headers: FirstHeaderValues(resp.Header), Body: raw}
	if readErr != nil {
		out.TransportError = "incomplete execution HTTP response"
	}
	return out, nil
}
