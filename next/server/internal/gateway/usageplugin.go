package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// Plugin-reported usage (CONTRACTS §25.3): endpoints whose usage rules
// declare usage.source "plugin" have their token usage read by the plugin
// declaring the platform, from the observation of its relay that waits
// (waited.go), instead of by the declarative gjson rules, which still run
// and are the fallback.
//
// THE STREAM IS NEVER BUFFERED. An SSE stream is unbounded, so a streaming
// response is relayed exactly as before - event by event, flushed as it
// goes - and the host keeps only the events whose name the endpoint listed
// in usage.streamEvents, all of them, of any number and size.

// usageCapture holds what the observation will show the plugin. It is
// nil for every endpoint that does not declare usage.source "plugin", which
// is how those endpoints pay exactly nothing for this feature - not one
// comparison per event, not one allocation, and never a call.
type usageCapture struct {
	want   map[string]bool
	events []*pluginv1.StreamEvent
	body   []byte
}

// newUsageCapture returns the capture for the route, or nil when the usage
// comes from the declarative rules.
//
// The knobs are read off the upstream ENDPOINT, not off the usage rules
// in effect: the rules follow the §13 override chain whose last link belongs
// to a third plugin, so who reads the usage would otherwise have had two
// readings (CONTRACTS §25.3, manifest.Endpoint.UsageSource).
func newUsageCapture(rt *typeRoute) *usageCapture {
	if !rt.pluginUsage {
		return nil
	}
	want := make(map[string]bool, len(rt.usageEvents))
	for _, n := range rt.usageEvents {
		want[n] = true
	}
	return &usageCapture{want: want}
}

// addEvent keeps one upstream SSE event when the endpoint asked for it. data
// belongs to the forwarding loop's reusable buffer, so it is copied.
func (u *usageCapture) addEvent(name string, data []byte) {
	if u == nil || !u.want[name] || len(data) == 0 {
		return
	}
	u.events = append(u.events, &pluginv1.StreamEvent{Name: name, Data: append([]byte(nil), data...)})
}

// setBody keeps a non-streaming response body, whole.
func (u *usageCapture) setBody(body []byte) {
	if u == nil {
		return
	}
	u.body = body
}

// pendingExtract is what the observation of a waited relay is built from:
// everything it needs, copied out of the request and the answer.
type pendingExtract struct {
	cap     *usageCapture
	meta    *pluginv1.RequestMeta
	account *pluginv1.Account
	headers map[string]string
	status  int
	// fields are the values of the endpoint's usageRequestFields, read out of
	// the request body at arming time; nil for endpoints declaring no request
	// fields. The body itself is NOT kept: this is the whole of what survives
	// the handler.
	fields map[string]string
}

// armUsageExtraction records what the observation will need, at the end of
// forwarding. upBody is the request body the plugin was handed (converted
// when the route converts) - the document the usageRequestFields are read
// from.
func (c *call) armUsageExtraction(rt *typeRoute, acct *pluginv1.Account, resp *http.Response, cap *usageCapture, upBody []byte) {
	if cap == nil || c.rec == nil {
		return
	}
	headers := execfetch.FirstHeaderValues(resp.Header)
	// The observation goes back to the plugin running Execute, the account
	// type's: the account is the one it was handed (CONTRACTS §124).
	c.usage = &pendingExtract{cap: cap, meta: c.metaFor(rt), account: acct,
		headers: headers, status: resp.StatusCode, fields: usageRequestFields(rt.usageRequestFields, upBody)}
}

// usageRequestFields reads the declared request paths out of body for
// ExtractUsageRequest.fields, every value whole whatever its size. The raw
// JSON comes straight from the client, so it is
// sanitised the way every other client byte that enters a proto string
// field is (CONTRACTS §25.1) - readBody already refuses a body with invalid
// UTF-8, this is the belt to that brace.
func usageRequestFields(paths []string, body []byte) (fields map[string]string) {
	if len(paths) == 0 || len(body) == 0 {
		return nil
	}
	for _, p := range paths {
		raw := getJSON(body, p)
		if raw == "" {
			continue
		}
		if fields == nil {
			fields = make(map[string]string, len(paths))
		}
		fields[p] = strings.ToValidUTF8(raw, "")
	}
	return fields
}

// errHostReadingEmpty is why a request is unreported whose plugin chose the
// host's reading when that reading holds no usage.
var errHostReadingEmpty = errors.New("the plugin took the host's reading, which holds no usage")

// hostUsageChosen marks the record of an endpoint whose usage the plugin
// reads when the plugin chose the host's reading instead (host_usage, or a
// relay that did not wait): the marker stays for the usage report
// (usage_logs.anomalies), but a deliberate choice is no failure. When the
// declarative rules found nothing in an answer that was served (no tokens,
// metrics or other usage) there are no facts, and the core makes none up:
// errHostReadingEmpty, and the caller ends the request as unreported.
func (c *call) hostUsageChosen() error {
	c.rec.UsageExtract = core.UsageExtractFallback
	r := c.rec
	if !r.Success || r.Tokens != (core.UsageTokens{}) || len(r.Metrics) > 0 || len(r.Additional) > 0 || len(r.Replacement) > 0 {
		return nil
	}
	return errHostReadingEmpty
}

func (c *call) usageFallback(ctx context.Context, rt *typeRoute, why string, err error) {
	c.rec.UsageExtract = core.UsageExtractFallback
	slog.WarnContext(ctx, "gateway: plugin usage extraction failed, billing from the declarative rules",
		"request_id", c.rid, "plugin", rt.binding.Plugin.Key, "platform", rt.platform,
		"protocol", rt.upstream, "endpoint", c.ep.Path, "reason", why, "err", err,
		"input_tokens", c.rec.Tokens.Input, "output_tokens", c.rec.Tokens.Output)
}

// applyUsageReport writes the upstream facts the plugin stated into the usage
// record - and nothing else.
//
// This is the boundary of PLUGIN-EXECUTES-CORE-RECORDS §4 in code: the only
// fields touched here are the token counts, the declared metering facts, the
// upstream model, the plugin's reading of an upstream error, and its opaque
// detail document. Attribution (user, api key, group, account), the request's
// own facts (status, attempts, latency, client, node, timestamps) and
// everything about money (multiplier, price, mode, tier, cost, billing
// status) are filled by the core elsewhere and are not reachable from here.
// UsageReport has no field for any of them, so a plugin cannot even ask.
func (c *call) applyUsageReport(ctx context.Context, rt *typeRoute, platformKey string, rep *pluginv1.UsageReport) {
	c.rec.UsageExtract = core.UsageExtractPlugin
	t := rep.GetTokens()
	c.rec.Tokens = usagerules.Tokens(t.GetInputTokens(), t.GetOutputTokens(), t.GetCacheReadTokens(),
		t.GetCacheCreationTokens(), t.GetCacheCreation_1HTokens())
	c.rec.Metrics = preserveHostCacheEvidence(c.rec.Metrics, c.reportedFacts(ctx, rt, rep.GetFacts()))
	if m := rep.GetUpstreamModel(); m != "" && m != c.model {
		c.rec.UpstreamModel = m
	}
	// An error the plugin read out of a 2xx response is recorded the same way
	// an error event in a stream already is: the client keeps the bytes it
	// got, the record stops claiming the request succeeded.
	if et := rep.GetErrorType(); et != "" {
		c.rec.Success = false
		c.rec.ErrorType = truncateUTF8(et, 50)
		c.rec.ErrorMessage = truncateUTF8(firstNonEmpty(rep.GetErrorMessage(), c.rec.ErrorMessage), 1000)
	}
	c.rec.PluginDetail = pluginDetail(ctx, c.rid, rt.binding.Plugin.Key, rep.GetDetailJson())
	c.applyReservation(ctx, rt, platformKey, rep.GetReserve())
}

// refIDRe is what a Reservation.ref_id may look like: the kind of opaque id
// an upstream hands out for a job. It has to survive a varchar(200) column
// and a log line, and it is compared for equality and nothing else.
var refIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$`)

// applyReservation records that this request only STARTED the work upstream
// (CONTRACTS §25.4). The estimate the plugin gave replaces the report's own
// token counts and facts - it is what will be priced, charged and stored,
// because until a reconcile says otherwise it is this request's usage.
//
// Everything else about the reservation is the core's: which plugin will be
// asked (the one declaring this platform - the one that just answered, not
// something the plugin can name), when, for how long, and what any of it
// costs. UsageReport has no field for a plugin to say otherwise.
func (c *call) applyReservation(ctx context.Context, rt *typeRoute, platformKey string, rv *pluginv1.Reservation) {
	if rv == nil {
		return
	}
	ref := rv.GetRefId()
	if !refIDRe.MatchString(ref) {
		// Without a usable id the entry could never be looked up again, and
		// pre-charging something that can never be reconciled is strictly
		// worse than billing it now. Drop the reservation, keep the usage.
		slog.WarnContext(ctx, "gateway: plugin reserved with an unusable ref_id, the request is billed normally",
			"request_id", c.rid, "plugin", platformKey, "platform", rt.platform, "ref_id", truncateUTF8(ref, 100))
		return
	}
	if platformKey == "" {
		slog.WarnContext(ctx, "gateway: a built-in platform cannot reserve, the request is billed normally",
			"request_id", c.rid, "platform", rt.platform)
		return
	}
	if t := rv.GetTokens(); t != nil {
		c.rec.Tokens = usagerules.Tokens(t.GetInputTokens(), t.GetOutputTokens(), t.GetCacheReadTokens(),
			t.GetCacheCreationTokens(), t.GetCacheCreation_1HTokens())
	}
	if f := rv.GetFacts(); len(f) > 0 {
		c.rec.Metrics = preserveHostCacheEvidence(c.rec.Metrics, c.reportedFacts(ctx, rt, f))
	}
	c.rec.Reservation = &core.UsageReservation{
		PluginKey:      platformKey,
		RefID:          ref,
		NextCheckAfter: time.Duration(rv.GetNextCheckAfterSec()) * time.Second,
		Deadline:       time.Duration(rv.GetDeadlineSec()) * time.Second,
	}
}

// reportedFacts turns the plugin's string facts into the shapes price
// expressions read, using the endpoint's own usage.facts declarations.
//
// Only declared keys survive. The manifest is what tells administrators which
// u("key") exist and what type each one has; a plugin that could add keys at
// runtime would make that list a guess, and a fact nobody declared is one no
// price expression was written against anyway.
func (c *call) reportedFacts(ctx context.Context, rt *typeRoute, facts map[string]string) map[string]any {
	if len(facts) == 0 {
		return nil
	}
	out := make(map[string]any, len(facts))
	for key, raw := range facts {
		if key == core.CacheWriteEvidenceKey {
			continue
		}
		f, ok := rt.usage.Facts[key]
		if !ok {
			slog.WarnContext(ctx, "gateway: plugin reported a usage fact the manifest does not declare, dropped",
				"request_id", c.rid, "plugin", rt.binding.Plugin.Key, "protocol", rt.upstream, "fact", key)
			continue
		}
		v, ok := usagerules.Fact(f, raw)
		if !ok {
			slog.WarnContext(ctx, "gateway: plugin reported a usage fact that does not fit its declared type, dropped",
				"request_id", c.rid, "plugin", rt.binding.Plugin.Key, "protocol", rt.upstream,
				"fact", key, "type", f.Type, "value", truncateUTF8(raw, 200))
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pluginDetail validates UsageReport.detail_json for usage_logs.plugin_detail:
// a JSON object, of any size (the column is jsonb).
func pluginDetail(ctx context.Context, rid, pluginKey, raw string) core.RawJSON {
	if raw == "" {
		return nil
	}
	if !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		slog.WarnContext(ctx, "gateway: plugin detail_json is not a JSON object, dropped",
			"request_id", rid, "plugin", pluginKey, "value", truncateUTF8(raw, 200))
		return nil
	}
	return core.RawJSON(raw)
}
