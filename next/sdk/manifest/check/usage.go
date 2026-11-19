package check

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Usage rules decide what the host bills for, so a rule the host cannot read
// is worse than a rejected package: the request is served and the money is
// never counted. Everything a usage rule declares is therefore checked against
// what server/internal/usagerules does with it at runtime.

// UsageSemantics are the two cache accounting models.
var UsageSemantics = []string{"exclusive", "inclusive"}

// UsageFields are the usage map keys the host understands. A key outside this
// set is not a token count at all: usagerules.Acc.set falls through to
// setMetric and files the value under usage_logs.metrics, where nothing can
// reach it - not billing (u() only sees declared facts) and not the token
// counters. A misspelt "output_tokens" would silently stop billing output.
var UsageFields = []string{
	manifest.UsageModel,
	manifest.UsageInputTokens,
	manifest.UsageOutputTokens,
	manifest.UsageCacheReadTokens,
	manifest.UsageCacheCreationTokens,
	manifest.UsageCacheCreation1h,
	manifest.UsageImageInputTokens,
	manifest.UsageImageOutputTokens,
}

// UsageFactTypes are the fact types usagerules.Acc.setMetric distinguishes;
// anything else would be stored as a string and reach price expressions as one.
var UsageFactTypes = []string{"number", "boolean", "enum", "string"}

// UsageSources are the accepted values of endpoint.usageSource ("" means the
// first).
var UsageSources = []string{manifest.UsageSourceRules, manifest.UsageSourcePlugin}

// Bounds on the plugin-source knobs. MaxUsageStreamEvents is the number of
// distinct event NAMES an endpoint may list; the host collects every event
// of a listed name at runtime. A declaration naming dozens of event names
// is asking for the whole stream, which is the one thing this design does
// not do.
const (
	MaxUsageStreamEvents = 8
	// MaxUsageRequestFields is how many request body paths an endpoint may
	// list in usageRequestFields. The list names the few scalars an estimate
	// is made from; a long one is asking for the request body.
	MaxUsageRequestFields = 16
)

// usageEventRe is what an SSE event name may look like. It is the name of an
// event the upstream sends, matched verbatim against the "event:" line (or
// the data object's "type"), so anything with whitespace or a newline in it
// could never match.
var usageEventRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// usageOwner says where a UsageRules block is declared, which decides one
// thing: whether semantics is required. (It used to decide a second - whether
// the block could pick the usage source - which stopped being a question when
// the source moved onto the endpoint.)
type usageOwner int

const (
	// ownerPlatform is Platform.usage: the defaults of every endpoint.
	ownerPlatform usageOwner = iota
	// ownerEndpoint is Endpoint.usage.
	ownerEndpoint
	// ownerAccountType is AccountPlatform.usage[protocol].
	ownerAccountType
)

// usageRules validates usage extraction rules. pluginSource says whether the
// endpoints these rules apply to read their usage from a plugin, which is the
// one case where a fact has no path of its own. It is a property of the
// ENDPOINT (Endpoint.UsageSource), not of this block, so it has to be handed
// in: a UsageRules block on a platform or on an account type override cannot
// tell on its own.
func (v *validator) usageRules(field string, u manifest.UsageRules, owner usageOwner, pluginSource bool) {
	v.additionalUsageRules(field+".additional", u.Additional)
	v.attemptUsageRule(field+".attempts", u.Attempts)
	switch {
	case u.Semantics == "":
		// A platform's own rules are the only place semantics is required:
		// an endpoint or account type override inherits it from the platform.
		if owner == ownerPlatform {
			v.add(field+".semantics", "required", "semantics is required (%s)", strings.Join(UsageSemantics, " or "))
		}
	case !slices.Contains(UsageSemantics, u.Semantics):
		v.add(field+".semantics", "invalid", "semantics must be exclusive or inclusive")
	}
	if u.JSON != nil {
		v.usageMap(field+".json.map", u.JSON.Map, u.Facts)
	}
	for i, s := range u.SSE {
		v.usageMap(fmt.Sprintf("%s.sse[%d].map", field, i), s.Map, u.Facts)
	}
	for _, key := range sortedKeys(u.Facts) {
		v.usageFact(field+".facts."+key, key, u.Facts[key], pluginSource)
	}
}

// usageSource validates endpoint.usageSource and the two knobs that only
// mean anything with it, source "plugin": usageStreamEvents and
// usageRequestFields.
//
// Both are rejected outside that source rather than ignored. An
// endpoint that lists usageStreamEvents while the host reads its usage from
// the declarative rules is a manifest whose author believes something the
// host does not do - and the symptom (usage counted from the gjson rules, as
// always) looks exactly like success.
//
// There is no ban to write here any more. The field used to live on
// UsageRules, where an account type's per-protocol override could reach it:
// that needed an explicit "an account type may not choose the source" rule,
// and even with it an override that simply omitted the source switched the
// endpoint back to the declarative rules, silently, because an override
// replaces the whole block. On the endpoint the question has one answer and
// only the plugin declaring the platform can write it.
func (v *validator) usageSource(f string, e manifest.Endpoint) {
	if e.UsageSource != "" && !slices.Contains(UsageSources, e.UsageSource) {
		v.add(f+".usageSource", "invalid", "usageSource must be one of %s", strings.Join(UsageSources, ", "))
		return
	}
	if !e.PluginUsage() {
		if len(e.UsageStreamEvents) > 0 {
			v.add(f+".usageStreamEvents", "unsupported",
				"usageStreamEvents is only read when usageSource is %q; the declarative sse rules decide which events are read otherwise",
				manifest.UsageSourcePlugin)
		}
		if len(e.UsageRequestFields) > 0 && !e.TaskSubmit() {
			v.add(f+".usageRequestFields", "unsupported",
				"usageRequestFields is only read when usageSource is %q; no plugin is asked for the usage otherwise",
				manifest.UsageSourcePlugin)
		}
		if !e.TaskSubmit() {
			return
		}
	}
	// billing "free" and a plugin-reported usage cannot both be meant. With
	// "free" the host never prices this endpoint, so every token, fact and -
	// the case that hurts - every Reservation the plugin returns is dropped
	// before it reaches the settler: no pre-charge, no pending_settlements
	// entry, no reconcile, and until this rule nothing that said so. The
	// endpoint that only starts a job is exactly the one an author is tempted
	// to mark free ("the submit itself has no final usage"); the right value
	// is "usage", the estimate is the charge and the reconcile corrects it.
	//
	// What this rule can and cannot see: it cannot know whether the plugin
	// will return a Reservation - that is a runtime answer - so it refuses
	// the whole combination, including a free endpoint that only wanted the
	// plugin's token counts for statistics. That case is deliberately not
	// carved out: the host would pay one hot-path RPC per request for numbers
	// it never prices, and an author who really wants it should be told here
	// rather than discover the silent half later. The runtime keeps its own
	// guard for packages installed before this rule (gateway warns and marks
	// the record when a reservation is dropped).
	if e.Billing == "free" && e.PluginUsage() {
		v.add(f+".billing", "conflict",
			"billing %q discards everything ExtractUsage reports for this endpoint, including a Reservation "+
				"(no pre-charge, no reconcile); an endpoint with usageSource %q must bill by usage",
			e.Billing, manifest.UsageSourcePlugin)
	}
	if n := len(e.UsageStreamEvents); n > MaxUsageStreamEvents {
		v.add(f+".usageStreamEvents", "too_many",
			"at most %d event names may be collected for ExtractUsage (declared %d); the host never buffers a whole stream",
			MaxUsageStreamEvents, n)
	}
	seen := map[string]bool{}
	for i, name := range e.UsageStreamEvents {
		ef := fmt.Sprintf("%s.usageStreamEvents[%d]", f, i)
		if !usageEventRe.MatchString(name) {
			v.add(ef, "invalid_format", "event name %q must match %s", name, usageEventRe.String())
			continue
		}
		if seen[name] {
			v.add(ef, "duplicate", "event name %q is declared twice", name)
		}
		seen[name] = true
	}
	v.usageRequestFields(f, e)
}

// usageRequestFields validates endpoint.usageRequestFields: a short list of
// distinct gjson paths that each read one value. The path syntax is checked
// the way usage map values are (ValidUsagePath), because the host reads them
// with the same gjson call and a typo would not fail, it would read nothing -
// and a plugin estimating a video from a "resolution" it never receives
// estimates the cheapest tier every time.
func (v *validator) usageRequestFields(f string, e manifest.Endpoint) {
	if n := len(e.UsageRequestFields); n > MaxUsageRequestFields {
		v.add(f+".usageRequestFields", "too_many",
			"at most %d request fields may be handed to ExtractUsage (declared %d); the request body is never handed over whole",
			MaxUsageRequestFields, n)
	}
	seen := map[string]bool{}
	for i, p := range e.UsageRequestFields {
		pf := fmt.Sprintf("%s.usageRequestFields[%d]", f, i)
		switch {
		case strings.TrimSpace(p) == "":
			v.add(pf, "required", "a gjson path is required")
		case p != strings.TrimSpace(p) || !ValidUsageRequestPath(p):
			v.add(pf, "invalid_path", "%q is not a gjson path that reads one value", p)
		case seen[p]:
			v.add(pf, "duplicate", "request field %q is declared twice", p)
		}
		seen[p] = true
	}
}

// Request observations may project one property from an array. This carries
// content types without handing over large images, URLs or prompt bodies.
// Values remain subject to the host's existing per-field and total byte caps.
// Token/fact extraction rules still require the scalar ValidUsagePath syntax.
func ValidUsageRequestPath(p string) bool {
	if ValidUsagePath(p) {
		return true
	}
	array, property, ok := strings.Cut(p, ".#.")
	return ok && ValidUsagePath(array) && ValidUsagePath(property) && !strings.Contains(property, "#") && !strings.Contains(array, "#")
}

// usageMap validates one "usage field -> path" map of usage.json or usage.sse.
func (v *validator) usageMap(field string, m map[string]string, facts map[string]manifest.UsageFact) {
	for _, key := range sortedKeys(m) {
		f := field + "." + key
		_, declaredFact := facts[key]
		if !slices.Contains(UsageFields, key) && !declaredFact {
			v.add(f, "unknown_field", "unknown usage field %q; one of %s", key, strings.Join(UsageFields, ", "))
			continue
		}
		v.usagePath(f, m[key])
	}
}

// factKeyRe constrains a usage.facts key. The key is used verbatim in two
// places administrators and operators read: the literal inside u("…") in a
// price expression, and a JSON key of usage_logs.metrics. Both want a plain
// snake_case identifier - a key with a dot, a quote or a Chinese character is
// awkward in one and confusing in the other.
var factKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// usageFact validates one entry of usage.facts: an extra metering value that
// administrators read in price expressions as u("<key>") and that the host
// stores in usage_logs.metrics. pluginSource is true when the surrounding
// rules ask the plugin for the usage, which is the one case where a fact has
// no path of its own.
func (v *validator) usageFact(field, key string, f manifest.UsageFact, pluginSource bool) {
	switch {
	case !factKeyRe.MatchString(key):
		v.add(field, "invalid_format", "fact key %q must match %s", key, factKeyRe.String())
	case slices.Contains(UsageFields, key):
		// usagerules.Acc.set files a standard field as a token count and
		// anything else under metrics, so a fact named like one of them means
		// two different numbers wear the same name in usage_logs.
		v.add(field, "reserved", "fact key %q is a standard usage field; one of %s is reserved",
			key, strings.Join(UsageFields, ", "))
	}
	switch f.Type {
	case "number", "boolean", "string":
		if len(f.Enum) > 0 {
			v.add(field+".enum", "unsupported", `enum is only allowed with type "enum"`)
		}
	case "enum":
		if len(f.Enum) == 0 {
			v.add(field+".enum", "required", `type "enum" requires a non-empty enum`)
		}
	case "":
		v.add(field+".type", "required", "type is required (%s)", strings.Join(UsageFactTypes, ", "))
	default:
		v.add(field+".type", "invalid", "type must be one of %s", strings.Join(UsageFactTypes, ", "))
	}
	// A fact without a path is never extracted (usagerules.applyFacts skips
	// it) yet still offers administrators a u() key that is always 0 - unless
	// the plugin supplies the value itself through ExtractUsage, where a path
	// would be the thing that means nothing. The declaration is still
	// required either way: it is what makes the key (and its type) exist for
	// price expressions and for the host's type check on what the plugin
	// reports.
	if pluginSource && f.Path == "" {
		return
	}
	v.usagePath(field+".path", f.Path)
}

// usagePath validates one usage map value or fact path. The host reads both
// with usagerules.Path: a gjson path, or several paths summed with "+" (a
// missing term counts as 0), so every term is checked on its own.
// usagerules.Path trims surrounding whitespace in both forms, so this trims
// before checking too - otherwise " a.b" would be accepted here as a path to
// a key literally named " a" and read as "a.b" at runtime.
func (v *validator) usagePath(field, spec string) {
	if strings.TrimSpace(spec) == "" {
		v.add(field, "required", "a gjson path is required")
		return
	}
	if !strings.Contains(spec, "+") {
		if !ValidUsagePath(strings.TrimSpace(spec)) {
			v.add(field, "invalid_path", "%q is not a gjson path that reads one value", spec)
		}
		return
	}
	// usagerules.Path trims the terms of a sum.
	for _, term := range strings.Split(spec, "+") {
		term = strings.TrimSpace(term)
		if term == "" {
			v.add(field, "invalid_path", "sum %q has an empty term", spec)
			return
		}
		if !ValidUsagePath(term) {
			v.add(field, "invalid_path", "%q in sum %q is not a gjson path that reads one value", term, spec)
			return
		}
	}
}

// usagePathProbe is the value ValidUsagePath writes and reads back. Any JSON
// value works; a string stays out of numeric coercions.
const usagePathProbe = `"sub2api probe"`

// ValidUsagePath reports whether p is a gjson path that reads one value out of
// a document - what usagerules needs of a usage map value or a fact path.
//
// gjson exports no path validator and treats a typo as a lookup that simply
// never matches ("usage..[[" is three keys, "@nope" an unknown modifier),
// which is exactly the silent miscounting this check exists to stop. The path
// is therefore checked by a round trip through the same path syntax: sjson
// materialises it in an empty document and gjson must read the probe back out.
// A path no value can be written to cannot name a single value either.
//
// This admits every key/index path, including escapes ("a\\.b"), and the array
// element count "a.#". It rejects gjson's reader-only operators - queries
// "#(...)", modifiers "@ugly", pipes "a|b", wildcards "a.*" and multipaths
// "{a,b}" - none of which yields the one number or string the host stores.
func ValidUsagePath(p string) bool {
	// "a.#" is the element count of the array a: a number the host can use,
	// but not a place sjson can write to, so the array itself is checked.
	if rest, ok := strings.CutSuffix(p, ".#"); ok {
		p = rest
	}
	if p == "" {
		return false
	}
	doc, err := sjson.SetRaw("{}", p, usagePathProbe)
	if err != nil {
		return false
	}
	return gjson.Get(doc, p).Raw == usagePathProbe
}
