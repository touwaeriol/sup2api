package standard

import (
	"encoding/json"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
)

// Request is what a path, a header value or an edit is computed from.
type Request struct {
	Account *pluginv1.Account
	// Meta is the gateway request's metadata; nil for the account test, the
	// model listing and the balance.
	Meta *pluginv1.RequestMeta
	// APIKey and BaseURL are the account's, normalized (apikey.Spec).
	APIKey  string
	BaseURL string
	// Model is the upstream model (Route.Model); "" for the model listing
	// and the balance.
	Model string
	// Stream is whether the answer streams (pluginsdk.RequestStream):
	// meta.stream, or the body's "stream" when it is true, which the core
	// sets for a stream-only account type while meta.stream keeps the
	// client's value. false for the account test, the listing and the
	// balance.
	Stream bool
	// Client are the client headers the core handed over (it has filtered
	// them by the manifest's passHeaders), lower-case names, trimmed values,
	// blank ones dropped; none for the account test, the listing and the
	// balance.
	Client map[string]string

	fields []map[string]any
}

// Field reads an account field: the credentials first, then the settings;
// a string as it is, a number or a boolean as its JSON text, anything else
// (or nothing) as "".
func (r *Request) Field(name string) string {
	if r.fields == nil {
		// Both decoded once already (apikey.Spec.FromAccount), so they are
		// JSON objects.
		creds, _ := apikey.DecodeObject(r.Account.GetCredentialsJson())
		settings, _ := apikey.DecodeObject(r.Account.GetSettingsJson())
		r.fields = []map[string]any{creds, settings}
	}
	switch v, _ := apikey.Lookup(name, r.fields...); v := v.(type) {
	case string:
		return v
	case float64, bool:
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

// upstreamURL is path on the base URL, with the ws(s) scheme for a websocket route.
func upstreamURL(path string, websocket bool, r *Request) string {
	u := r.BaseURL + path
	if websocket {
		u = pluginsdk.WebSocketURL(u) // the base URL is http(s) (apikey.NormalizeBaseURL)
	}
	return u
}

// headers are the request's default headers: the forwarded client headers,
// then the type's writes, then the route's. The credential is not among
// them (credential).
func (t *Type) headers(route []Header, r *Request) map[string]string {
	h := make(map[string]string, len(r.Client)+len(t.Headers)+len(route))
	for k, v := range r.Client {
		h[k] = v
	}
	for _, w := range t.Headers {
		w.write(h, r)
	}
	for _, w := range route {
		w.write(h, r)
	}
	return h
}

// credential is the authoritative header of the request: the key where a
// says, none when a names no header.
func credential(a Auth, r *Request) map[string]string {
	if a.Header == "" {
		return map[string]string{}
	}
	return map[string]string{a.Header: a.Prefix + r.APIKey}
}

// getHeaders are the headers of a GET without a body (the model listing,
// the balance): the type's writes but content-type, and the key where a
// says.
func (t *Type) getHeaders(a Auth, r *Request) map[string]string {
	h := t.headers(nil, r)
	delete(h, "content-type")
	for name, value := range credential(a, r) {
		h[name] = value
	}
	return h
}

func (w Header) write(h map[string]string, r *Request) {
	value := w.Value(r)
	if value == "" {
		return
	}
	cur := h[w.Name]
	switch {
	case w.Mode == Replace, cur == "":
		h[w.Name] = value
	case w.Mode == Append && !hasToken(cur, value):
		h[w.Name] = cur + "," + value
	}
}

// hasToken reports whether the comma-separated list cur holds value; for a
// "key=version" value any version of key counts.
func hasToken(cur, value string) bool {
	key, _, versioned := strings.Cut(value, "=")
	for _, t := range strings.Split(cur, ",") {
		t = strings.TrimSpace(t)
		if t == value || (versioned && strings.HasPrefix(t, key+"=")) {
			return true
		}
	}
	return false
}

// clientHeaders are the client headers of inbound (apikey.ForwardHeaders).
func clientHeaders(inbound map[string]string) map[string]string {
	out := make(map[string]string, len(inbound))
	apikey.ForwardHeaders(out, inbound)
	return out
}

// normalizeModel removes surrounding spaces and prefix.
func normalizeModel(name, prefix string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), prefix)
}
