// Package standard implements the platform of "API key" account types: an
// account holds an API key and a base URL, and a request is a route per
// upstream protocol on that base URL with the key in a header.
// openai, gemini, relay, azure_openai and the apikey type of anthropic are
// such types (docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md §7.3).
//
// A plugin describes each type as a Type, usually starting from a provider
// preset (sdk/pluginsdk/providers/{openai,anthropic,gemini,typesafe}), or
// combining several with Merge (a relay site that speaks every vendor's
// protocol), and serves New(types...): the result is a pluginsdk.Platform
// and UpstreamBuilder, run with pluginsdk.ExecuteDefault, plus
// BuildModelsRequest and the balance (pluginsdk.BalanceReader). README.md
// has complete plugin main packages.
//
// The library holds only what every such type does the same way. What
// differs between protocols - paths, the model in the path, body edits, the
// test body, the error policy - is Go code in the Type, written by the
// provider presets; nothing here knows a vendor.
package standard

import (
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

// Type is the whole behaviour of one account type. Its maps and slices are
// read, never modified, once the type is passed to New.
type Type struct {
	// ID is the account type id (accountTypes[].id); New copies it into
	// Spec.AccountType.
	ID string
	// Spec validates the credentials and normalizes the base URL: default
	// base URL, whether it is required, the version segments a base URL may
	// end in, the wording of the errors.
	Spec apikey.Spec
	// Auth is where the API key goes: on every route without its own, the
	// model listing and the balance.
	Auth Auth
	// Headers are written on every request of the type, in order, before
	// the route's.
	Headers []Header
	// Routes are keyed by upstream protocol id ("openai.chat").
	Routes map[string]Route
	// DefaultProtocol is the route of a request without a protocol; ""
	// rejects such a request.
	DefaultProtocol string
	// FilterFields are removed from every request body (also a websocket
	// turn's message) before the route's edit. They run after the core's
	// rewrites, so a field the operator's parameter override set is
	// removed too (design §12.5).
	FilterFields []string
	// Classify classifies the upstream's errors of a protocol whose route
	// has no policy of its own, and of a request without a known protocol.
	Classify classify.Policy
	// Test is the console's account test; nil answers UNIMPLEMENTED.
	Test *Test
	// Tests are further tests, one per protocol: the console's test uses
	// the first protocol the core lists (BuildTestRequestRequest.protocols)
	// that Test or one of Tests is on, else Test. Tests needs Test.
	Tests []Test
	// Models is the model listing on the base URL; nil answers
	// UNIMPLEMENTED.
	Models *apikey.ModelList
	// Balance is the account's balance (manifest accountTypes[].balance);
	// nil answers UNIMPLEMENTED.
	Balance *Balance
}

// Auth is the header that carries the API key, the authoritative header of
// the request, with Prefix before the key ("Bearer "). An empty Header
// sends no credential.
type Auth struct {
	Header string
	Prefix string
}

// Route is the request of one upstream protocol.
type Route struct {
	// Path is appended to the base URL: the path, written the way it goes
	// on the wire (escape what the request supplies with url.PathEscape),
	// and an optional query. Path fixes a constant one.
	Path func(*Request) string
	// WebSocket makes the route a websocket turn, opened with GET (any other
	// route is a POST): the URL is the handshake's (ws, wss), the headers the handshake's (no
	// content-type), the body the turn's message.
	WebSocket bool
	// Model says where the upstream model comes from.
	Model Model
	// Headers are written after the type's.
	Headers []Header
	// Auth is where the key goes on this route; nil: the type's.
	Auth *Auth
	// Classify classifies the upstream's errors of this route's protocol;
	// nil: the type's. Vendors differ in their error bodies and in what a
	// 429 or a 529 means, and a relay answers each protocol in its own.
	Classify *classify.Policy
	// Edit changes the body after FilterFields are deleted, in the same
	// jsonedit pass (nil: none). Not on a websocket route.
	Edit func(d *jsonedit.Doc, r *Request)
}

// Model is the rule for a route's upstream model.
type Model struct {
	// FromMeta takes the request's meta.model; otherwise the body's
	// "model" when it is a non-empty string, else meta.model
	// (pluginsdk.RequestModel).
	FromMeta bool
	// TrimPrefix is removed, with surrounding spaces, from the model
	// ("models/" of a Gemini resource name).
	TrimPrefix string
	// Required rejects a request without a model.
	Required bool
	// Missing is the error of a missing required model; default
	// "model is required".
	Missing string
}

// Header is a header write. Its value is computed per request; an empty
// value writes nothing.
type Header struct {
	Name  string
	Mode  Mode
	Value Value
}

// Mode is how a header write treats a value already there (a forwarded
// client header or an earlier write).
type Mode int

const (
	// Replace writes the value over any other.
	Replace Mode = iota
	// Keep writes the value only where there is none.
	Keep
	// Append adds the value to a comma-separated list unless the list
	// holds it; for "key=version" any version of key counts, so a client
	// that names its own version keeps it.
	Append
)

// Set, Default and AppendTo are the three header writes.
func Set(name string, v Value) Header      { return Header{Name: name, Mode: Replace, Value: v} }
func Default(name string, v Value) Header  { return Header{Name: name, Mode: Keep, Value: v} }
func AppendTo(name string, v Value) Header { return Header{Name: name, Mode: Append, Value: v} }

// Value computes a header value from the request.
type Value func(*Request) string

// Fixed is a constant value.
func Fixed(s string) Value { return func(*Request) string { return s } }

// Field is the account's field name (Request.Field).
func Field(name string) Value { return func(r *Request) string { return r.Field(name) } }

// Path is a constant route path.
func Path(p string) func(*Request) string { return func(*Request) string { return p } }

// Test is the console's account test: a request on the route of Protocol.
type Test struct {
	Protocol string
	// Model is tested when the console names none.
	Model string
	// Body is the request body for model.
	Body func(model string) string
}

// Balance is the balance of an account: GETs on its base URL, sent in this
// order, whose answers Read turns into the result.
type Balance struct {
	Requests []BalanceRequest
	// Read reads the answers, one per request in the same order, into the
	// result (BalanceFailure and Micros help). r holds the account, its key
	// and base URL. It states the upstream's facts: its number as it is,
	// with the unit it gives if any, never converted (CONTRACTS §52); an
	// answer it cannot read is an ERROR_TYPE_TRANSIENT result, never an
	// error.
	Read func(r *Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult
}

// BalanceRequest is one GET of a balance.
type BalanceRequest struct {
	// Path is appended to the base URL, with an optional query.
	Path string
	// Auth is where the key goes; nil: the type's, &Auth{}: no key (a
	// public endpoint).
	Auth *Auth
}
