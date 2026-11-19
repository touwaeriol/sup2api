package pluginsdk

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// transportHeaders belong to the host's transport: a request naming one is
// refused (host.proto, UpstreamRequest). Host is the URL's: the request
// goes to the host the URL names, which is the host checked.
var transportHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"accept-encoding":   true,
	"connection":        true,
	"transfer-encoding": true,
}

// Upstream builds the final upstream request of one execution
// (pluginv1.UpstreamRequest). Header names are lower-cased for the caller;
// what the host would refuse is reported by Build.
//
// The two kinds of header differ in who has the last word:
//
//   - Header writes an AUTHORITATIVE header, applied after the operator's
//     passthrough and override, which therefore cannot change it. Only for
//     what must reach the upstream exactly as built: the credential header
//     and the headers a signature covers (with a signature, the Content-Type
//     it was computed with); a plugin whose released versions sent every
//     header above the operator's settings may keep doing so.
//   - DefaultHeader writes a DEFAULT header, applied before the operator's
//     settings: a passed through client header only fills a name left
//     unset, the account's header override and parameter override may
//     replace or remove it. Everything else belongs here.
//
// A name in both takes the authoritative value. RemoveHeader keeps a
// header from being sent at all, whatever a layer below set.
type Upstream struct {
	request *pluginv1.UpstreamRequest
}

// NewUpstream starts a request. method "" is POST. rawURL is absolute,
// http(s) or, for a websocket turn, the URL of the handshake; write the path
// the way it goes on the wire, every segment already percent-encoded (the
// host parses it again before sending).
func NewUpstream(method, rawURL string) *Upstream {
	return &Upstream{request: &pluginv1.UpstreamRequest{Method: method, Url: rawURL}}
}

// WebSocketURL is the ws(s) URL of an http(s) one, for a WebSocket handshake
// on the address of an HTTP route: https becomes wss and http ws. Any other
// URL comes back unchanged, and Build refuses it.
func WebSocketURL(httpURL string) string {
	if rest, ok := strings.CutPrefix(httpURL, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(httpURL, "http://"); ok {
		return "ws://" + rest
	}
	return httpURL
}

// Header sets an authoritative header. An empty value writes nothing.
func (u *Upstream) Header(name, value string) *Upstream {
	u.request.Headers = u.set(u.request.Headers, name, value)
	return u
}

// RemoveHeader keeps the header name from being sent: it is written as an
// authoritative header with an empty value, which removes whatever a layer
// below set (a default header, a passed through client header, the
// operator's header or parameter override). For a header a signature
// must not see, e.g. a session token the operator added on an account
// whose signature has none. A later Header of the same name replaces it.
func (u *Upstream) RemoveHeader(name string) *Upstream {
	if u.request.Headers == nil {
		u.request.Headers = map[string]string{}
	}
	u.request.Headers[strings.ToLower(name)] = ""
	return u
}

// DefaultHeader sets a default header. An empty value writes nothing.
func (u *Upstream) DefaultHeader(name, value string) *Upstream {
	u.request.DefaultHeaders = u.set(u.request.DefaultHeaders, name, value)
	return u
}

// Headers sets every header of h as authoritative; several values of one
// name are joined with ", ". For a signed request pass only what the
// signature produced or covers (reqsign.AWSSigV4Headers), not req.Header:
// every other header there would become authoritative too.
func (u *Upstream) Headers(h http.Header) *Upstream {
	for name, values := range h {
		u.Header(name, strings.Join(values, ", "))
	}
	return u
}

// DefaultHeaders sets every header of h as a default header.
func (u *Upstream) DefaultHeaders(h http.Header) *Upstream {
	for name, values := range h {
		u.DefaultHeader(name, strings.Join(values, ", "))
	}
	return u
}

// HeaderMap sets every header of h as authoritative.
func (u *Upstream) HeaderMap(h map[string]string) *Upstream {
	for name, value := range h {
		u.Header(name, value)
	}
	return u
}

// DefaultHeaderMap sets every header of h as a default header.
func (u *Upstream) DefaultHeaderMap(h map[string]string) *Upstream {
	for name, value := range h {
		u.DefaultHeader(name, value)
	}
	return u
}

func (u *Upstream) set(headers map[string]string, name, value string) map[string]string {
	if value == "" {
		return headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers[strings.ToLower(name)] = value
	return headers
}

// Body sets the final body bytes, for a websocket turn the message of the
// turn. The host sends them as they are: keeping the model reference and
// the stream flag of the body Execute was given is the plugin's part. The
// slice is not copied.
func (u *Upstream) Body(body []byte) *Upstream {
	u.request.Body = body
	return u
}

// Model records the upstream model after the plugin's own mapping in the
// usage log (optional).
func (u *Upstream) Model(model string) *Upstream {
	u.request.UpstreamModel = model
	return u
}

// ResponseStream names the format of an answer that is neither SSE nor
// JSON, e.g. awseventstream.FormatChunk; the host decodes it into
// server-sent events. Not on a websocket turn.
func (u *Upstream) ResponseStream(format string) *Upstream {
	u.request.ResponseStream = format
	return u
}

// Build returns the request, or what makes the host refuse it as an
// INVALID_ARGUMENT error.
func (u *Upstream) Build() (*pluginv1.UpstreamRequest, error) {
	if err := ValidateUpstreamRequest(u.request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return u.request, nil
}

// ValidateUpstreamRequest checks what the host refuses in a relayed request
// without looking at the account: the URL is absolute http(s) (ws(s) for a
// websocket handshake), the method is empty or an HTTP token, header names
// are lower-case HTTP tokens and none is the transport's, values are what
// HTTP can carry (no control character but a tab). The host judges method, names and values with the same
// functions (golang.org/x/net/http/httpguts). Relay applies it before the
// host is asked.
func ValidateUpstreamRequest(r *pluginv1.UpstreamRequest) error {
	if r == nil {
		return errors.New("upstream request is required")
	}
	var errs []error
	if err := checkURL(r.GetUrl(), "http", "https", "ws", "wss"); err != nil {
		errs = append(errs, err)
	}
	if m := r.GetMethod(); m != "" && !httpguts.ValidHeaderFieldName(m) {
		errs = append(errs, fmt.Errorf("upstream method %q is not an HTTP method", m))
	}
	errs = append(errs, checkHeaders("headers", r.GetHeaders()), checkHeaders("default_headers", r.GetDefaultHeaders()))
	return errors.Join(errs...)
}

func checkURL(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("upstream url: %w", err)
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme && u.Host != "" {
			return nil
		}
	}
	return fmt.Errorf("upstream url %q is not an absolute %s URL", raw, strings.Join(schemes, "/"))
}

func checkHeaders(field string, headers map[string]string) error {
	var errs []error
	for name, value := range headers {
		switch {
		case name == "" || name != strings.ToLower(name):
			errs = append(errs, fmt.Errorf("%s: header name %q must be lower-case", field, name))
		case transportHeaders[name]:
			errs = append(errs, fmt.Errorf("%s: header %q belongs to the host's transport and cannot be set", field, name))
		case !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value):
			errs = append(errs, fmt.Errorf("%s: header %q is malformed", field, name))
		}
	}
	return errors.Join(errs...)
}
