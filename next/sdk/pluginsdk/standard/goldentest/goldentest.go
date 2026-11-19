// Package goldentest holds the frozen golden cases of the API key platform
// plugins (openai, anthropic, gemini, relay) and replays them against an
// implementation (README.md).
//
// The cases were recorded from earlier implementations of the four plugins
// and name the final upstream request each input must produce
// (docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md §7.3).
//
// A plugin's test adapts its entry points to a Subject and calls Run:
//
//	func TestGolden(t *testing.T) {
//		goldentest.Run(t, "openai", goldentest.Subject{Execute: ..., Validate: ..., ...})
//	}
package goldentest

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

//go:embed testdata/golden/*.json
var files embed.FS

// The kinds of a case.
const (
	KindExecute  = "execute"  // the upstream request of a gateway request
	KindTest     = "test"     // BuildTestRequest
	KindModels   = "models"   // BuildModelsRequest
	KindValidate = "validate" // ValidateCredentials
	KindClassify = "classify" // ClassifyError
)

// File is one fixture file: the cases of one plugin's account type.
type File struct {
	Plugin      string `json:"plugin"`
	AccountType string `json:"account_type"`
	// PassHeaders are the client headers the account type receives
	// (accountTypes[].platforms[].passHeaders, else the platform's), as they
	// were when the cases were converted. An execute case's client headers
	// hold more than these on purpose: the rest must not reach the upstream.
	PassHeaders []string `json:"pass_headers"`
	Cases       []Case   `json:"cases"`
}

// Case is one recorded input and its answer. Exactly one of Want and Error
// is set. Input and Want are an ExecuteInput and an UpstreamRequest for
// KindExecute; for the other kinds they are the request and response
// messages of the RPC in proto JSON with proto field names.
type Case struct {
	Name  string          `json:"name"`
	Kind  string          `json:"kind"`
	Now   string          `json:"now,omitempty"` // the clock of a classify case, RFC 3339
	Input json.RawMessage `json:"input"`
	Want  json.RawMessage `json:"want,omitempty"`
	Error *Error          `json:"error,omitempty"`
	// Note is set on the few cases whose answer is not the recorded one,
	// and says why (README.md).
	Note string `json:"note,omitempty"`
}

// Error is a recorded failure. Message is compared, with the wrapping a
// plugin may put around it removed on both sides (Unwrap). Code is the gRPC
// code the plugin answered with when the case was recorded; it is kept for
// reference and not compared: the core treats every failure of these calls
// alike, and the codes already differ today (the anthropic plugin answered
// FailedPrecondition where the shared account check says InvalidArgument).
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ExecuteInput is what a plugin is handed for one attempt: the account, the
// request's metadata, the client's headers and the complete request body
// (already converted, mapped and overridden by the core).
type ExecuteInput struct {
	Account       *Account          `json:"account,omitempty"` // nil: the case of a missing account
	Meta          Meta              `json:"meta"`
	ClientHeaders map[string]string `json:"client_headers,omitempty"`
	Body          string            `json:"body"`
}

// Account is the account of an execute case.
type Account struct {
	ID              int64  `json:"id"`
	Type            string `json:"type,omitempty"`
	CredentialsJSON string `json:"credentials_json,omitempty"`
	SettingsJSON    string `json:"settings_json,omitempty"`
}

// Proto is the account as the plugin protocol carries it.
func (a *Account) Proto() *pluginv1.Account {
	if a == nil {
		return nil
	}
	return &pluginv1.Account{Id: a.ID, Type: a.Type, CredentialsJson: a.CredentialsJSON, SettingsJson: a.SettingsJSON}
}

// Meta is the part of the request metadata the cases vary.
type Meta struct {
	Protocol string `json:"protocol,omitempty"`
	Model    string `json:"model,omitempty"`
	Stream   bool   `json:"stream,omitempty"`
}

// Proto is the metadata as the plugin protocol carries it.
func (m Meta) Proto() *pluginv1.RequestMeta {
	return &pluginv1.RequestMeta{Protocol: m.Protocol, Model: m.Model, Stream: m.Stream}
}

// UpstreamRequest is the final request a plugin hands to the core's client
// for relaying. Headers are the authoritative ones (written after the
// operator's header settings: the credential), DefaultHeaders the ones the
// operator's settings may still change. Body is the final bytes; on a
// WebSocket route it is the turn's message.
type UpstreamRequest struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	DefaultHeaders map[string]string `json:"default_headers,omitempty"`
	Body           string            `json:"body"`
	UpstreamModel  string            `json:"upstream_model,omitempty"`
}

// Subject is the implementation under test: one function per kind. now is
// the clock a classification must use instead of the wall clock.
type Subject struct {
	Execute  func(ctx context.Context, in *ExecuteInput) (*UpstreamRequest, error)
	Test     func(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error)
	Models   func(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error)
	Validate func(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error)
	Classify func(ctx context.Context, now time.Time, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
}

// Plugins are the plugins that have a fixture file, sorted.
func Plugins() []string {
	entries, err := files.ReadDir("testdata/golden")
	if err != nil {
		panic(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	return out
}

// Load reads the fixture file of plugin.
func Load(plugin string) (*File, error) {
	b, err := files.ReadFile("testdata/golden/" + plugin + ".json")
	if err != nil {
		return nil, fmt.Errorf("goldentest: no golden cases for plugin %q", plugin)
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("goldentest: %s: %w", plugin, err)
	}
	return &f, nil
}

// Option adjusts Run.
type Option func(*config)

type config struct {
	except map[string]string
	keep   []func(*Case) bool
}

// Except declares that the case called name is known to differ and why.
// Run then requires it to differ: an exception for a case that passes, or
// for one that is not replayed (missing, or not selected), fails, so the
// list cannot outlive its reason.
func Except(name, reason string) Option {
	return func(c *config) { c.except[name] = reason }
}

// Select replays only the cases keep accepts (with several, those all
// accept): an implementation that is not the file's plugin, such as a type
// that also serves the file's protocols (standard.Merge), replays the
// cases it must answer alike. Run fails when none is left.
func Select(keep func(*Case) bool) Option {
	return func(c *config) { c.keep = append(c.keep, keep) }
}

// Forwarded accepts the execute cases of one of protocols on an account of
// accountType that names its own base URL (Case.BaseURL): what a type
// serving those protocols on the account's address must build as the
// file's plugin did, whatever its credential rules (validate, test and
// models cases, a missing or default base URL, another account type) and
// its default protocol.
func Forwarded(accountType string, protocols ...string) func(*Case) bool {
	return func(c *Case) bool {
		in, _, err := c.Execute()
		_, own := c.BaseURL()
		return err == nil && own && in.Account.Type == accountType && slices.Contains(protocols, in.Meta.Protocol)
	}
}

// BaseURL is the base_url an execute case's account names: a non-blank
// string in its settings, else its credentials (where the presets read
// it). ok is false for any other case.
func (c *Case) BaseURL() (baseURL string, ok bool) {
	in, _, err := c.Execute()
	if err != nil || in.Account == nil {
		return "", false
	}
	for _, doc := range []string{in.Account.SettingsJSON, in.Account.CredentialsJSON} {
		var fields map[string]any
		if json.Unmarshal([]byte(doc), &fields) != nil {
			continue
		}
		if v, isString := fields["base_url"].(string); isString && strings.TrimSpace(v) != "" {
			return v, true
		}
		if fields["base_url"] != nil {
			return "", false
		}
	}
	return "", false
}

// Run replays every case of plugin (the selected ones, see Select)
// against s and reports each difference. Every function of s must be set:
// a kind is never skipped silently.
func Run(t *testing.T, plugin string, s Subject, opts ...Option) {
	t.Helper()
	f, err := Load(plugin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{except: map[string]string{}}
	for _, o := range opts {
		o(&cfg)
	}
	replayed := 0
	for i := range f.Cases {
		c := &f.Cases[i]
		if slices.ContainsFunc(cfg.keep, func(keep func(*Case) bool) bool { return !keep(c) }) {
			continue
		}
		replayed++
		diffs := c.Check(t.Context(), s)
		reason, excepted := cfg.except[c.Name]
		delete(cfg.except, c.Name)
		switch {
		case excepted && len(diffs) == 0:
			t.Errorf("%s: declared an exception (%s) but answers as recorded", c.Name, reason)
		case excepted:
			t.Logf("%s: differs as declared (%s): %s", c.Name, reason, strings.Join(diffs, "; "))
		default:
			for _, d := range diffs {
				t.Errorf("%s: %s", c.Name, d)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.except)) {
		t.Errorf("exception for %q: %s has no such case, or it is not selected", name, plugin)
	}
	if replayed == 0 {
		t.Errorf("%s: no case selected", plugin)
	}
}

// Check replays the case against s and returns the differences, none when
// the answer is the recorded one.
func (c *Case) Check(ctx context.Context, s Subject) []string {
	got, err, problem := c.call(ctx, s)
	switch {
	case problem != "":
		return []string{problem}
	case c.Error != nil && err == nil:
		return []string{fmt.Sprintf("no error, want %q", c.Error.Message)}
	case c.Error != nil:
		if g := Unwrap(status.Convert(err).Message()); g != c.Error.Message {
			return []string{fmt.Sprintf("error %q, want %q", g, c.Error.Message)}
		}
		return nil
	case err != nil:
		return []string{fmt.Sprintf("error %v, want none", err)}
	}
	return got()
}

// call runs the case's entry point. diff compares a successful answer with
// Want; problem reports a case that could not be run at all.
func (c *Case) call(ctx context.Context, s Subject) (diff func() []string, err error, problem string) {
	switch c.Kind {
	case KindExecute:
		if s.Execute == nil {
			return nil, nil, "the subject has no Execute"
		}
		in, want, perr := c.Execute()
		if perr != nil {
			return nil, nil, perr.Error()
		}
		out, err := s.Execute(ctx, in)
		return func() []string { return diffRequest(out, want) }, err, ""
	case KindTest:
		return callProto(c, s.Test != nil, func(in *pluginv1.BuildTestRequestRequest) (proto.Message, error) { return s.Test(ctx, in) })
	case KindModels:
		return callProto(c, s.Models != nil, func(in *pluginv1.BuildModelsRequestRequest) (proto.Message, error) { return s.Models(ctx, in) })
	case KindValidate:
		return callProto(c, s.Validate != nil, func(in *pluginv1.ValidateCredentialsRequest) (proto.Message, error) {
			return s.Validate(ctx, in)
		})
	case KindClassify:
		now, perr := time.Parse(time.RFC3339, c.Now)
		if perr != nil {
			return nil, nil, "now: " + perr.Error()
		}
		return callProto(c, s.Classify != nil, func(in *pluginv1.ClassifyErrorRequest) (proto.Message, error) {
			return s.Classify(ctx, now, in)
		})
	}
	return nil, nil, fmt.Sprintf("unknown kind %q", c.Kind)
}

// callProto runs a case whose input and answer are protocol messages.
func callProto[Req proto.Message](c *Case, set bool, f func(Req) (proto.Message, error)) (func() []string, error, string) {
	if !set {
		return nil, nil, "the subject has no function for kind " + c.Kind
	}
	var zero Req
	in := zero.ProtoReflect().New().Interface().(Req)
	if err := protojson.Unmarshal(c.Input, in); err != nil {
		return nil, nil, "input: " + err.Error()
	}
	out, err := f(in)
	return func() []string { return diffProto(out, c.Want) }, err, ""
}

// Execute decodes an execute case: its input and, unless the case records
// an error, the request it expects (nil otherwise).
func (c *Case) Execute() (*ExecuteInput, *UpstreamRequest, error) {
	if c.Kind != KindExecute {
		return nil, nil, fmt.Errorf("case %q is of kind %s, not %s", c.Name, c.Kind, KindExecute)
	}
	in := &ExecuteInput{}
	if err := decodeStrict(c.Input, in); err != nil {
		return nil, nil, fmt.Errorf("input: %w", err)
	}
	if c.Error != nil {
		return in, nil, nil
	}
	want := &UpstreamRequest{}
	if err := decodeStrict(c.Want, want); err != nil {
		return nil, nil, fmt.Errorf("want: %w", err)
	}
	return in, want, nil
}

func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// wrapped matches what a plugin may put around a message: "account N: "
// and a nested gRPC status ("rpc error: code = X desc = ").
var wrapped = regexp.MustCompile(`^(account -?\d+: )?(rpc error: code = \w+ desc = )?`)

// Unwrap removes a plugin's wrapping from an error message.
func Unwrap(message string) string { return wrapped.ReplaceAllString(message, "") }

// diffRequest compares the request a subject built with the recorded one.
// The body is compared byte for byte; header names without regard to case.
func diffRequest(got, want *UpstreamRequest) []string {
	if got == nil {
		return []string{"no upstream request"}
	}
	var diffs []string
	text := func(what, g, w string) {
		if g != w {
			diffs = append(diffs, fmt.Sprintf("%s %q, want %q", what, g, w))
		}
	}
	text("method", got.Method, want.Method)
	text("url", got.URL, want.URL)
	text("upstream model", got.UpstreamModel, want.UpstreamModel)
	if got.Body != want.Body {
		d := fmt.Sprintf("body %s, want %s", got.Body, want.Body)
		if sameJSON(got.Body, want.Body) {
			d += " (the same JSON value in other bytes)"
		}
		diffs = append(diffs, d)
	}
	diffs = append(diffs, diffHeaders("headers", got.Headers, want.Headers)...)
	return append(diffs, diffHeaders("default headers", got.DefaultHeaders, want.DefaultHeaders)...)
}

func diffHeaders(what string, got, want map[string]string) []string {
	g, dup := lowerKeys(got)
	if dup != "" {
		return []string{fmt.Sprintf("%s name %q twice in different case", what, dup)}
	}
	w, _ := lowerKeys(want)
	if maps.Equal(g, w) {
		return nil
	}
	return []string{fmt.Sprintf("%s %s, want %s", what, sortedJSON(g), sortedJSON(w))}
}

func lowerKeys(h map[string]string) (out map[string]string, dup string) {
	out = make(map[string]string, len(h))
	for k, v := range h {
		k = strings.ToLower(k)
		if _, ok := out[k]; ok {
			dup = k
		}
		out[k] = v
	}
	return out, dup
}

// diffProto compares a response message with the recorded proto JSON.
func diffProto(got proto.Message, want json.RawMessage) []string {
	if got == nil || !got.ProtoReflect().IsValid() {
		return []string{"no answer"}
	}
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(got)
	if err != nil {
		return []string{"answer: " + err.Error()}
	}
	g, gerr := canonical(b)
	w, werr := canonical(want)
	switch {
	case gerr != nil || werr != nil:
		return []string{fmt.Sprintf("not JSON: answer %v, want %v", gerr, werr)}
	case !bytes.Equal(g, w):
		return []string{fmt.Sprintf("answer %s, want %s", g, w)}
	}
	return nil
}

// canonical re-encodes a JSON document with sorted keys, numbers as
// written, no HTML escaping and no insignificant space.
func canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return []byte(sortedJSON(v)), nil
}

func sortedJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func sameJSON(a, b string) bool {
	ca, aerr := canonical([]byte(a))
	cb, berr := canonical([]byte(b))
	return aerr == nil && berr == nil && bytes.Equal(ca, cb)
}
