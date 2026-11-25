// Package usagerules applies the declarative usage rules of a platform
// manifest (manifest.UsageRules) to upstream responses: it resolves which
// rules describe a protocol and extracts the token counts, model and metrics
// they declare. The gateway uses it on every forwarded response; the console
// "test account" action uses it on the plugin's test response.
package usagerules

import (
	"bytes"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// For resolves the rules describing responses of protocol: the account type's
// override for that protocol, else the endpoint speaking it, else the
// platform's defaults (ARCHITECTURE 6.6). ep may be nil (no endpoint known).
func For(ap manifest.AccountPlatform, ep *manifest.Endpoint, pf *manifest.Platform, protocol string) manifest.UsageRules {
	if u, ok := ap.Usage[protocol]; ok {
		return u
	}
	if ep != nil && ep.Usage != nil {
		return *ep.Usage
	}
	if pf == nil {
		return manifest.UsageRules{}
	}
	return pf.Usage
}

// Acc applies the platform's declarative usage rules. Later values win, so
// cumulative counters (message_delta) override earlier ones.
type Acc struct {
	cacheEvidence   *core.CacheWriteEvidence
	attempts        *attemptMeter
	primaryModel    string
	additional      map[string][]core.AdditionalUsage
	AdditionalError string
	rules           manifest.UsageRules

	input, output, cacheRead, cacheCreation, cacheCreation1h int64
	imageInput, imageOutput                                  int64

	// Model is the upstream model the response reported, when the rules map it.
	Model string
	// Metrics holds the extra facts the rules declare (usage_logs.metrics).
	Metrics map[string]any
	// StreamError is set when an SSE stream carried an error event.
	StreamError string
	// StreamErrorEvent and StreamErrorData are the last error event of the
	// stream as the upstream sent it, for the log: the one StreamError reads
	// or a response.failed, which leaves the record as it is.
	StreamErrorEvent string
	StreamErrorData  []byte
	// final is set once an SSE event reported the stream's final usage
	// (Final).
	final bool

	// logAttrs identify the request in the warnings below; warned keeps each
	// distinct warning to one line per accumulator. Both matter because the
	// rules run again for every event of an SSE stream: a rule that is wrong
	// is wrong on all of them, and a per-event log line would be a flood.
	logAttrs []any
	warned   map[string]bool
}

// New builds an accumulator applying rules.
func New(rules manifest.UsageRules) *Acc { return &Acc{rules: rules} }

// WithLog attaches slog attributes (request id, plugin, protocol) to the
// warnings the accumulator emits when a rule does not fit the response. It
// returns u so it can be chained onto New.
func (u *Acc) WithLog(attrs ...any) *Acc {
	u.logAttrs = attrs
	return u
}

// warn logs once per accumulator per key. The rules are re-applied to every
// SSE event, so without this a single bad declaration would log a line per
// chunk; key is the part of the declaration at fault, so one wrong fact does
// not silence another.
func (u *Acc) warn(key, msg string, attrs ...any) {
	if u.warned[key] {
		return
	}
	if u.warned == nil {
		u.warned = map[string]bool{}
	}
	u.warned[key] = true
	slog.Warn(msg, append(append([]any{}, u.logAttrs...), attrs...)...)
}

func (u *Acc) set(field string, r gjson.Result) {
	if field == core.CacheWriteEvidenceKey {
		return
	}
	if !r.Exists() || r.Type == gjson.Null {
		return
	}
	switch field {
	case manifest.UsageModel:
		u.Model = r.String()
	case manifest.UsageInputTokens, manifest.UsageOutputTokens, manifest.UsageCacheReadTokens,
		manifest.UsageCacheCreationTokens, manifest.UsageCacheCreation1h,
		manifest.UsageImageInputTokens, manifest.UsageImageOutputTokens:
		// gjson turns anything unparseable into 0, so a standard token field
		// pointed at a string ("high") or a bool counts nothing and says
		// nothing - the same silent under-billing as a misspelt path key, one
		// layer further in. The value is still stored as before (a numeric
		// string keeps working); only the silence goes.
		if !numeric(r) {
			u.warn("field:"+field, "usagerules: usage field is not a number, counting 0",
				"field", field, "value", truncate(r.Raw, 200), "json_type", r.Type.String())
		}
		switch field {
		case manifest.UsageInputTokens:
			u.input = r.Int()
		case manifest.UsageOutputTokens:
			u.output = r.Int()
		case manifest.UsageCacheReadTokens:
			u.cacheRead = r.Int()
		case manifest.UsageCacheCreationTokens:
			u.cacheCreation = r.Int()
		case manifest.UsageCacheCreation1h:
			u.cacheCreation1h = r.Int()
		case manifest.UsageImageInputTokens:
			u.imageInput = r.Int()
		case manifest.UsageImageOutputTokens:
			u.imageOutput = r.Int()
		}
	default:
		if fact, ok := u.rules.Facts[field]; ok {
			u.setMetric(field, r, &fact)
		} else {
			u.setMetric(field, r, nil)
		}
	}
}

// numeric reports whether r carries a number: a JSON number, or a string that
// parses as one (gjson's Int()/Float() read those too).
func numeric(r gjson.Result) bool {
	switch r.Type {
	case gjson.Number:
		return true
	case gjson.String:
		_, err := strconv.ParseFloat(strings.TrimSpace(r.Str), 64)
		return err == nil
	default:
		return false
	}
}

func truncate(s string, n int) string { return x.TruncateEllipsis(s, n) }

// setMetric files a value under usage_logs.metrics. f is the fact declaring
// it, nil for a usage map key that is not a standard usage field (those have
// no declaration to check against).
func (u *Acc) setMetric(key string, r gjson.Result, f *manifest.UsageFact) {
	if key == core.CacheWriteEvidenceKey {
		return
	}
	if !r.Exists() || r.Type == gjson.Null {
		return
	}
	typ := ""
	if f != nil {
		typ = f.Type
	}
	if typ == "string" && r.Type != gjson.String {
		u.warn("type:"+key, "usagerules: value is not a string, fact not recorded", "fact", key, "json_type", r.Type.String())
		return
	}
	var v any
	switch {
	case typ == "boolean" || r.Type == gjson.True || r.Type == gjson.False:
		v = r.Bool()
	case typ == "number" || r.Type == gjson.Number:
		v = r.Float()
	default:
		v = r.String()
	}
	// A declared enum is a closed set: the price expressions administrators
	// write against u("key") only ever test the declared values, so letting an
	// undeclared one through would store a value nothing can price and quietly
	// change what the fact means. Drop it and say so instead.
	if typ == "enum" {
		s, ok := v.(string)
		if !ok || !slices.Contains(f.Enum, s) {
			u.warn("enum:"+key, "usagerules: value outside the declared enum, fact not recorded",
				"fact", key, "value", truncate(r.String(), 200), "enum", f.Enum)
			return
		}
	}
	if u.Metrics == nil {
		u.Metrics = map[string]any{}
	}
	u.Metrics[key] = v
}

func (u *Acc) applyFacts(doc []byte) {
	for key, f := range u.rules.Facts {
		if f.Path != "" {
			u.setMetric(key, Path(doc, f.Path), &f)
		}
	}
}

// Path evaluates a usage map value: a gjson path, or "a+b+..." summing
// several numeric paths. Missing (or null) terms count as 0; when every term
// is missing the result does not exist, i.e. the value was not provided.
//
// Surrounding whitespace is trimmed in both forms. It used to be trimmed only
// for the terms of a sum, so " usage.total" as a lone path read a key that is
// literally named with a leading space - i.e. nothing, silently - while the
// same spelling inside a sum worked. Trimming is the forgiving half of that
// pair: no manifest in the tree names a key with an edge space, and a space in
// a declared path is a typo every time.
func Path(doc []byte, spec string) gjson.Result {
	if !strings.Contains(spec, "+") {
		return gjson.GetBytes(doc, strings.TrimSpace(spec))
	}
	var (
		sum   float64
		found bool
	)
	for _, p := range strings.Split(spec, "+") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r := gjson.GetBytes(doc, p)
		if !r.Exists() || r.Type == gjson.Null {
			continue
		}
		found = true
		sum += r.Float()
	}
	if !found {
		return gjson.Result{}
	}
	return gjson.Result{Type: gjson.Number, Num: sum, Raw: strconv.FormatFloat(sum, 'f', -1, 64)}
}

// ApplyJSON applies the JSON rules to a response body. A top-level array
// (Gemini's JSON array stream) applies them to each element in order.
func (u *Acc) ApplyJSON(body []byte) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	if r := gjson.ParseBytes(body); r.IsArray() {
		r.ForEach(func(_, v gjson.Result) bool {
			if v.IsObject() {
				u.applyJSONDoc([]byte(v.Raw))
			}
			return true
		})
		return
	}
	u.applyJSONDoc(body)
}

// HasJSON reports whether the rules describe non-streaming JSON responses.
func (u *Acc) HasJSON() bool { return u.rules.JSON != nil }

func (u *Acc) applyJSONDoc(body []byte) {
	u.applyAttempts("", body, false)
	u.applyAdditional("", body, false)
	if m := u.rules.JSON; m != nil {
		for field, path := range m.Map {
			u.set(field, Path(body, path))
		}
	}
	u.applyFacts(body)
	if u.rules.JSON != nil {
		u.observeCacheEvidence(body, u.rules.JSON.Map, "json")
	}
}

// ApplySSE applies the SSE rules to one upstream event.
func (u *Acc) ApplySSE(event string, data []byte) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return
	}
	name := EventName(event, data)
	u.applyAttempts(name, data, true)
	u.applyAdditional(name, data, true)
	u.applyStreamError(name, data)
	for _, rule := range u.rules.SSE {
		if rule.Event != "" && rule.Event != name {
			continue
		}
		for field, path := range rule.Map {
			u.set(field, Path(data, path))
		}
		u.final = u.final || finalUsage(rule, data)
	}
	u.applyFacts(data)
	for _, rule := range u.rules.SSE {
		if rule.Event == "" || rule.Event == name {
			u.observeCacheEvidence(data, rule.Map, "sse")
		}
	}
}

// ErrorEvent reports whether an event of a stream is an error event: a named
// "error" event or an unnamed {"error": {...}} chunk.
func ErrorEvent(name string, data []byte) bool {
	return name == "error" || (name == "" && gjson.GetBytes(data, "error").IsObject())
}

// applyStreamError notes an error event. Anthropic/Responses name error
// events; OpenAI chat and Gemini send an unnamed {"error": {...}} chunk.
// A Responses response.failed is kept for the log only.
func (u *Acc) applyStreamError(name string, data []byte) {
	errorEvent := ErrorEvent(name, data)
	if errorEvent {
		msg := gjson.GetBytes(data, "error.message").String()
		if msg == "" {
			msg = gjson.GetBytes(data, "message").String()
		}
		if msg == "" {
			msg = string(data)
		}
		u.StreamError = "upstream stream error: " + msg
	}
	if errorEvent || name == "response.failed" {
		u.StreamErrorEvent, u.StreamErrorData = name, bytes.Clone(data)
	}
}

// Final reports whether the stream's final usage has been read: an event an
// sse rule names explicitly reported the output count. Rules for any event
// never make a stream final (CONTRACTS §25.8).
func (u *Acc) Final() bool { return u.final }

// finalUsage reports whether data, matched by rule, reported the final usage
// (Final).
func finalUsage(rule manifest.SSEUsageMap, data []byte) bool {
	path := rule.Map[manifest.UsageOutputTokens]
	if rule.Event == "" || path == "" {
		return false
	}
	r := Path(data, path)
	return r.Exists() && r.Type != gjson.Null
}

// Tokens converts to core.UsageTokens. cache_creation_tokens is the total
// cache write; known 1h writes and the compatibility default-write bucket are
// charged separately. Missing TTL classification is recorded in Metrics.
// The image counts are the image share of input and output (CONTRACTS §118).
func (u *Acc) Tokens() core.UsageTokens {
	t := Tokens(u.input, u.output, u.cacheRead, u.cacheCreation, u.cacheCreation1h)
	t.ImageInput, t.ImageOutput = max(u.imageInput, 0), max(u.imageOutput, 0)
	return t
}

// EventName is the name an SSE event is matched by: the "event:" line when
// the upstream sent one, otherwise the data object's "type" field. Both the
// declarative sse rules and the usage.streamEvents allow-list handed to
// PlatformService.ExtractUsage use it, so a plugin and a rule see the same
// events under the same names.
func EventName(event string, data []byte) string {
	if event != "" {
		return event
	}
	return gjson.GetBytes(data, "type").String()
}

// Tokens normalises one set of raw upstream counts into core.UsageTokens:
// cacheCreation is the TOTAL cache write and cacheCreation1h the part of it
// explicitly reported with a one hour TTL. The remainder uses the platform
// default cache-write rate for compatibility; it is not evidence of a 5m TTL.
// Negative inputs count as 0.
//
// It is shared by the declarative accumulator and by the counts a plugin
// reports through ExtractUsage, so "cache_creation_tokens" cannot come to
// mean two different things depending on who produced it.
func Tokens(input, output, cacheRead, cacheCreation, cacheCreation1h int64) core.UsageTokens {
	cc := cacheCreation - cacheCreation1h
	if cc < 0 {
		cc = 0
	}
	return core.UsageTokens{
		Input:           max(input, 0),
		Output:          max(output, 0),
		CacheRead:       max(cacheRead, 0),
		CacheCreation:   cc,
		CacheCreation1h: max(cacheCreation1h, 0),
	}
}

// Fact converts one value a plugin reported for a declared usage fact into
// what the host stores in usage_logs.metrics, using the declaration's type -
// the same shapes setMetric produces from the gjson rules, so a price
// expression reading u("key") cannot tell the two sources apart.
//
// ok is false when the value does not fit the declaration: a "number" that
// does not parse, a "boolean" that is not true/false, an "enum" value outside
// the declared set. The caller drops it and says so; storing a string where a
// price expression expects a number would silently change what it computes.
func Fact(f manifest.UsageFact, raw string) (any, bool) {
	s := strings.TrimSpace(raw)
	switch f.Type {
	case "number":
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, false
		}
		return v, true
	case "boolean":
		v, err := strconv.ParseBool(s)
		if err != nil {
			return nil, false
		}
		return v, true
	case "enum":
		if !slices.Contains(f.Enum, raw) {
			return nil, false
		}
		return raw, true
	default:
		return raw, true
	}
}
