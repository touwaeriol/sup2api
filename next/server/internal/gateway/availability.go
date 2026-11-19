package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"google.golang.org/protobuf/proto"
)

// Budgets of the model availability precheck
// (MODEL-AVAILABILITY-PRECHECK-DESIGN §5.2). Both precheck budgets derive
// from the request context, one after the other: a slow snapshot read never
// eats the plugin's time. Gateway fields override them in tests.
const (
	// availabilitySnapshotTimeout bounds the quota snapshot read.
	availabilitySnapshotTimeout = 500 * time.Millisecond
	// availabilityPluginTimeout starts after the read and is shared by every
	// plugin group and batch of one precheck.
	availabilityPluginTimeout = time.Second
	// availabilityGuardSnapshotTimeout bounds the guard's read: the guard
	// does not wait for a snapshot the precheck could not read either, it
	// keeps the admission's (§5.3). A cached entry answers without I/O, a
	// stale one waits at most the mirror's 40 ms.
	availabilityGuardSnapshotTimeout = 50 * time.Millisecond
	// quotaObservationTimeout bounds writing a quota observation back.
	quotaObservationTimeout = 2 * time.Second
	// availabilityBreakerOpen is how long a plugin whose availability call
	// failed is not asked again on this node (one probe after that).
	availabilityBreakerOpen = 10 * time.Second
)

const availabilityBatchSize = 128
const errTypeModelAvailability = "model_availability"

// Reason codes of the precheck and the guard (AvailabilitySample.Reason,
// AvailabilityTrace.Counts). "unavailable" and "plugin_unknown" carry the
// plugin's reason after a colon.
const (
	availReasonSnapshot      = "snapshot_unavailable"
	availReasonPluginError   = "plugin_error"
	availReasonPluginTimeout = "plugin_timeout"
	availReasonBadResponse   = "plugin_bad_response"
	availReasonUnknown       = "plugin_unknown"
	availReasonUnavailable   = "unavailable"
	availReasonIdentity      = "guard_identity_changed"
	availReasonModels        = "guard_models_changed"
	availReasonModelMismatch = "upstream_model_mismatch"
)

// Bounds of an AvailabilityTrace (§6.1).
const (
	availTraceSamples   = 20
	availTraceCountKeys = 16
	availTraceGuard     = 5
	availReasonBytes    = 64
	availModelBytes     = 100
	availLogSamples     = 5
	// availCountOther collects the accounts of reasons beyond the count keys.
	availCountOther = "other"
)

var (
	errAvailabilityNoChecker   = errors.New("plugin does not implement CheckModelAvailability")
	errAvailabilityBreakerOpen = errors.New("availability calls to this plugin failed recently; not asking for 10s")
	errAvailabilityFailedOpen  = errors.New("the plugin already failed in this request")
	errAvailabilityNoSource    = errors.New("no quota snapshot source")
	errAvailabilitySnapshotLag = errors.New("quota snapshot not read in time")
)

type availabilityAdmission struct {
	quota *pluginv1.AccountQuotaSnapshot
	// stale: quota predates the account's last change, so its generation
	// says nothing about the identity the account has now.
	stale  bool
	models []string
	now    int64
	// reason is the fail-open reason code, "" when the plugin said AVAILABLE.
	reason string
}

// generation is the identity the admission vouches for: 0 (unknown) for a
// stale snapshot, whose generation may predate the account's current one.
func (a availabilityAdmission) generation() int64 {
	if a.stale {
		return 0
	}
	return a.quota.GetGeneration()
}

// availabilityVerdict is the precheck's answer for one account.
type availabilityVerdict struct {
	blocked bool
	// retryAt: for a blocked account, when its last unavailable model frees
	// up; 0 when any of them did not say.
	retryAt int64
	// code is the reason code, "" for an account every model of which the
	// plugin said AVAILABLE with a snapshot at hand.
	code   string
	detail string // the plugin's reason (unavailable, plugin_unknown)
	model  string
	// whole: the code is about the whole plugin call or snapshot read, not
	// this account.
	whole bool
	err   error
}

type availabilityKey struct {
	id    int64
	model string
}

// quotaRead is a snapshot read as the precheck uses it.
type quotaRead struct {
	snapshots map[int64]*pluginv1.AccountQuotaSnapshot
	stale     map[int64]bool
	missing   map[int64]bool
	// whole: nothing could be read (no source, an error, or every id
	// missing); err says why.
	whole bool
	err   error
}

// availabilityCallFailure is why a plugin's availability answers are
// incomplete; zero when every batch was answered.
type availabilityCallFailure struct {
	code string
	err  error
	// unasked: the shared budget ran out before this batch was sent, so
	// the plugin itself did not fail; the breaker does not count it.
	unasked bool
}

func (g *Gateway) quotaSource() core.AccountQuotaSource {
	if source, ok := g.d.Quota.(core.AccountQuotaSource); ok {
		return source
	}
	source, _ := g.d.Accounts.(core.AccountQuotaSource)
	return source
}

func (g *Gateway) availabilitySnapshotBudget() time.Duration {
	if g.availabilitySnapshotTimeout > 0 {
		return g.availabilitySnapshotTimeout
	}
	return availabilitySnapshotTimeout
}

func (g *Gateway) availabilityPluginBudget() time.Duration {
	if g.availabilityPluginTimeout > 0 {
		return g.availabilityPluginTimeout
	}
	return availabilityPluginTimeout
}

func (g *Gateway) availabilityGuardSnapshotBudget() time.Duration {
	if g.availabilityGuardSnapshotTimeout > 0 {
		return g.availabilityGuardSnapshotTimeout
	}
	return availabilityGuardSnapshotTimeout
}

// availabilityFailure is the error of a blocked account: denied (the plugin
// said UNAVAILABLE) is a 429; otherwise the account's identity or models
// changed under the request, or the plugin built a request for another model.
func availabilityFailure(denied bool) *gwError {
	if denied {
		return &gwError{Status: http.StatusTooManyRequests, Code: "model_quota_exhausted", Message: "the requested model is unavailable for these accounts, please retry later", RecordType: errTypeModelAvailability}
	}
	return &gwError{Status: http.StatusServiceUnavailable, Code: "model_availability_unavailable", Message: "model availability could not be verified, please retry later", RecordType: errTypeModelAvailability}
}

// sameQuotaIdentity: an unknown generation (0) matches anything, two known
// ones must be equal. A different generation is a reauthorization (or a
// credential, settings or plugin change) of the account (§5.3).
func sameQuotaIdentity(old, cur *pluginv1.AccountQuotaSnapshot) bool {
	return sameQuotaGeneration(old.GetGeneration(), cur.GetGeneration())
}

func sameQuotaGeneration(old, cur int64) bool {
	return old <= 0 || cur <= 0 || old == cur
}

func (c *call) availabilityModels(ref *core.AccountRef) []*pluginv1.ModelAvailabilityCandidate {
	models := []string{c.model}
	for _, extra := range c.modelRefs {
		models = append(models, extra.model)
	}
	seen := map[string]bool{}
	var out []*pluginv1.ModelAvailabilityCandidate
	for _, model := range models {
		mapped := ref.MapModel(model)
		if seen[mapped] {
			continue
		}
		seen[mapped] = true
		out = append(out, &pluginv1.ModelAvailabilityCandidate{AccountId: ref.ID, AccountType: ref.Type, Model: mapped, ClientModel: model})
	}
	return out
}

// readQuota reads the snapshots of ids within budget. It never fails: what
// could not be read is missing, and the precheck goes on without it.
func (c *call) readQuota(ctx context.Context, ids []int64, budget time.Duration) quotaRead {
	r := quotaRead{missing: map[int64]bool{}}
	source := c.g.quotaSource()
	if source == nil {
		for _, id := range ids {
			r.missing[id] = true
		}
		r.whole, r.err = true, errAvailabilityNoSource
		return r
	}
	sctx, cancel := context.WithTimeout(ctx, budget)
	view, err := source.AccountQuotaView(sctx, ids)
	cancel()
	r.snapshots, r.stale = view.Snapshots, view.Stale
	for _, id := range view.Missing {
		r.missing[id] = true
	}
	// The contract: err != nil exactly when Missing is not empty, and the
	// other ids are still answered. A source that fails without naming the
	// ids cannot say which absent snapshot means "no row": all are unknown.
	if err != nil && len(view.Missing) == 0 {
		for _, id := range ids {
			if r.snapshots[id] == nil {
				r.missing[id] = true
			}
		}
	}
	r.err = err
	if len(r.missing) > 0 && r.err == nil {
		r.err = errAvailabilitySnapshotLag
	}
	r.whole = len(ids) > 0 && len(r.missing) >= len(ids)
	return r
}

// filterAvailability is the model availability precheck: it removes the
// candidates the plugin of their account type says cannot serve the model
// now (MODEL-AVAILABILITY-PRECHECK-DESIGN §5.1). Only a definite UNAVAILABLE
// removes an account; a check that cannot be made lets it through, so the
// precheck answers 429 when every candidate is unavailable and never 503.
// A client that went away ends it as canceled.
func (c *call) filterAvailability(ctx context.Context, refs []core.AccountRef) ([]core.AccountRef, *gwError) {
	groups := map[string][]core.AccountRef{}
	var order []string
	var ids []int64
	for _, ref := range refs {
		rt := c.route(&ref)
		if rt == nil || !rt.binding.Type.ModelAvailability {
			continue
		}
		if _, ok := groups[ref.PluginKey]; !ok {
			order = append(order, ref.PluginKey)
		}
		groups[ref.PluginKey] = append(groups[ref.PluginKey], ref)
		ids = append(ids, ref.ID)
	}
	if len(ids) == 0 {
		return refs, nil
	}
	read := c.readQuota(ctx, ids, c.g.availabilitySnapshotBudget())
	if ctx.Err() != nil {
		return nil, canceledErr()
	}
	pctx, cancel := context.WithTimeout(ctx, c.g.availabilityPluginBudget())
	defer cancel()
	verdicts := make(map[int64]availabilityVerdict, len(ids))
	for _, key := range order {
		for id, v := range c.checkAvailability(ctx, pctx, groups[key], &read) {
			verdicts[id] = v
		}
		if ctx.Err() != nil {
			return nil, canceledErr()
		}
	}
	out := make([]core.AccountRef, 0, len(refs))
	var entries []availabilityEntry
	blocked := 0
	var retryAt int64
	retryKnown := true
	for _, ref := range refs {
		v, checked := verdicts[ref.ID]
		if checked && v.code != "" {
			entries = append(entries, availabilityEntry{id: ref.ID, plugin: ref.PluginKey, v: v})
		}
		if v.blocked {
			blocked++
			retryKnown = retryKnown && v.retryAt > 0
			if retryAt == 0 || v.retryAt < retryAt {
				retryAt = v.retryAt
			}
			continue
		}
		out = append(out, ref)
	}
	if !retryKnown {
		retryAt = 0
	}
	c.availabilityChecked, c.availabilityAdmitted = len(ids), len(ids)-blocked
	tr := buildAvailabilityTrace(entries, len(ids), len(ids)-blocked, retryAt)
	c.setAvailabilityTrace(tr)
	c.logAvailability(ctx, entries, tr, len(out) == 0)
	if len(out) > 0 {
		return out, nil
	}
	failure := availabilityFailure(true)
	if now := c.g.now().Unix(); retryAt > now {
		failure.Headers = http.Header{"Retry-After": {itoa(min(retryAt-now, 3600))}}
	}
	return out, failure
}

// checkAvailability asks the plugin of refs (all of one plugin) about them
// and admits every account it does not say is UNAVAILABLE, recording the
// admission (c.availability). pctx bounds the plugin call, ctx is the
// request.
func (c *call) checkAvailability(ctx, pctx context.Context, refs []core.AccountRef, read *quotaRead) map[int64]availabilityVerdict {
	plugin := refs[0].PluginKey
	now := c.g.now()
	in := &pluginv1.CheckModelAvailabilityRequest{NowUnix: now.Unix()}
	candidates := make(map[int64][]*pluginv1.ModelAvailabilityCandidate, len(refs))
	for i := range refs {
		ref := &refs[i]
		list := c.availabilityModels(ref)
		candidates[ref.ID] = list
		for _, candidate := range list {
			if q := read.snapshots[ref.ID]; q != nil {
				candidate.Quota = proto.Clone(q).(*pluginv1.AccountQuotaSnapshot)
			}
			in.Candidates = append(in.Candidates, candidate)
		}
	}
	var seen map[availabilityKey]*pluginv1.ModelAvailability
	var failure availabilityCallFailure
	rt := c.route(&refs[0])
	checker, ok := rt.binding.Client.(core.AccountModelAvailabilityPlugin)
	switch {
	case !ok:
		failure = availabilityCallFailure{code: availReasonPluginError, err: errAvailabilityNoChecker}
	case c.availabilityOpen[plugin]:
		// Asking again would wait for the same failure on every attempt.
		failure = availabilityCallFailure{code: availReasonPluginError, err: errAvailabilityFailedOpen}
	default:
		allowed, probe := c.g.availabilityBreakers.allow(plugin, now)
		if !allowed {
			failure = availabilityCallFailure{code: availReasonPluginError, err: errAvailabilityBreakerOpen}
			break
		}
		seen, failure = availabilityResults(pctx, checker, in)
		if ctx.Err() != nil || failure.unasked {
			// The client went away, or the budget was spent before the
			// plugin was asked: no verdict on the plugin.
			c.g.availabilityBreakers.abandon(plugin, probe)
		} else {
			c.g.availabilityBreakers.done(plugin, c.g.now(), failure.code != "", probe)
		}
	}
	if failure.code != "" {
		if c.availabilityOpen == nil {
			c.availabilityOpen = map[string]bool{}
		}
		c.availabilityOpen[plugin] = true
	}
	if c.availability == nil {
		c.availability = map[int64]availabilityAdmission{}
	}
	out := make(map[int64]availabilityVerdict, len(refs))
	for _, ref := range refs {
		var v availabilityVerdict
		retryKnown := true
		unknown := availabilityVerdict{}
		var models []string
		for _, candidate := range candidates[ref.ID] {
			models = append(models, candidate.Model)
			result := seen[availabilityKey{ref.ID, candidate.Model}]
			switch {
			case result == nil:
				if !v.blocked && v.code == "" {
					v = availabilityVerdict{code: failure.code, whole: true, err: failure.err}
				}
			case result.State == pluginv1.ModelAvailability_AVAILABLE:
			case result.State == pluginv1.ModelAvailability_UNAVAILABLE:
				if !v.blocked {
					v = availabilityVerdict{blocked: true, code: availReasonUnavailable, detail: result.Reason, model: candidate.Model}
				}
				retryKnown = retryKnown && result.RetryAtUnix > in.NowUnix
				v.retryAt = max(v.retryAt, result.RetryAtUnix)
			default:
				if unknown.code == "" {
					unknown = availabilityVerdict{code: availReasonUnknown, detail: result.Reason, model: candidate.Model}
				}
			}
		}
		if v.blocked {
			if !retryKnown {
				v.retryAt = 0
			}
			out[ref.ID] = v
			continue
		}
		// A failed plugin call is why the account went through unchecked;
		// otherwise a missing snapshot is the likely reason the plugin
		// could not tell (or told without the facts).
		if v.code == "" && read.missing[ref.ID] {
			v = availabilityVerdict{code: availReasonSnapshot, whole: read.whole, err: read.err}
		}
		if v.code == "" {
			v = unknown
		}
		admission := availabilityAdmission{models: models, now: in.NowUnix, stale: read.stale[ref.ID], reason: v.code}
		if q := read.snapshots[ref.ID]; q != nil {
			admission.quota = proto.Clone(q).(*pluginv1.AccountQuotaSnapshot)
		}
		c.availability[ref.ID] = admission
		out[ref.ID] = v
	}
	return out
}

// availabilityResults asks checker in bounded batches, one after the other
// under the shared deadline of ctx. Each batch is validated on its own and
// merged only when it is whole; the first failed batch ends the calls and
// says why the rest has no answer.
func availabilityResults(ctx context.Context, checker core.AccountModelAvailabilityPlugin, in *pluginv1.CheckModelAvailabilityRequest) (map[availabilityKey]*pluginv1.ModelAvailability, availabilityCallFailure) {
	seen := map[availabilityKey]*pluginv1.ModelAvailability{}
	for offset := 0; offset < len(in.Candidates); offset += availabilityBatchSize {
		if err := ctx.Err(); err != nil {
			return seen, availabilityCallFailure{code: availReasonPluginTimeout, err: err, unasked: true}
		}
		batch := &pluginv1.CheckModelAvailabilityRequest{NowUnix: in.NowUnix, Candidates: in.Candidates[offset:min(offset+availabilityBatchSize, len(in.Candidates))]}
		out, err := checker.CheckModelAvailability(ctx, batch)
		if err != nil {
			if ctx.Err() != nil {
				return seen, availabilityCallFailure{code: availReasonPluginTimeout, err: err}
			}
			return seen, availabilityCallFailure{code: availReasonPluginError, err: err}
		}
		got, err := validAvailabilityBatch(batch, out)
		if err != nil {
			return seen, availabilityCallFailure{code: availReasonBadResponse, err: err}
		}
		for key, result := range got {
			seen[key] = result
		}
	}
	return seen, availabilityCallFailure{}
}

// validAvailabilityBatch checks that out answers every candidate of batch
// exactly once with a known state; the whole batch is refused otherwise.
func validAvailabilityBatch(batch *pluginv1.CheckModelAvailabilityRequest, out *pluginv1.CheckModelAvailabilityResponse) (map[availabilityKey]*pluginv1.ModelAvailability, error) {
	if out == nil {
		return nil, errors.New("empty response")
	}
	want := make(map[availabilityKey]bool, len(batch.Candidates))
	for _, candidate := range batch.Candidates {
		want[availabilityKey{candidate.AccountId, candidate.Model}] = true
	}
	if len(out.Results) != len(want) {
		return nil, fmt.Errorf("%d results for %d candidates", len(out.Results), len(want))
	}
	got := make(map[availabilityKey]*pluginv1.ModelAvailability, len(want))
	for _, result := range out.Results {
		if result == nil {
			return nil, errors.New("nil result")
		}
		key := availabilityKey{result.GetAccountId(), result.GetModel()}
		switch {
		case !want[key]:
			return nil, fmt.Errorf("result for account %d model %q was not asked for", key.id, key.model)
		case got[key] != nil:
			return nil, fmt.Errorf("duplicate result for account %d model %q", key.id, key.model)
		case result.State < pluginv1.ModelAvailability_UNKNOWN || result.State > pluginv1.ModelAvailability_UNAVAILABLE:
			return nil, fmt.Errorf("unknown state %d", result.State)
		case result.RetryAtUnix < 0:
			return nil, errors.New("negative retry_at_unix")
		}
		got[key] = result
	}
	return got, nil
}

// guardAvailability re-checks the chosen account right before sending
// (§5.3): another request may have just seen its model window run out. It
// reads the snapshot without waiting for one the precheck could not read
// either, blocks a changed identity or model list, and otherwise asks the
// plugin again - unless the plugin already failed in this request or its
// breaker is open, which let the account through.
func (c *call) guardAvailability(ctx context.Context, ref *core.AccountRef) *gwError {
	rt := c.route(ref)
	if rt == nil || !rt.binding.Type.ModelAvailability {
		return nil
	}
	old, admitted := c.availability[ref.ID]
	cur, curStale := old.quota, old.stale
	read := c.readQuota(ctx, []int64{ref.ID}, c.g.availabilityGuardSnapshotBudget())
	if ctx.Err() != nil {
		return nil // the send that follows ends the attempt as canceled
	}
	missing := read.missing[ref.ID]
	if !missing {
		cur, curStale = read.snapshots[ref.ID], read.stale[ref.ID]
	}
	models := c.availabilityModels(ref)
	// A WebSocket turn stays on the identity its connection was opened with.
	if c.quotaPinned && !curStale && !sameQuotaGeneration(c.quotaPin, cur.GetGeneration()) {
		return c.guardBlocked(ctx, ref, availReasonIdentity, "", c.model, 0, availabilityFailure(false))
	}
	if admitted {
		// A stale or absent admission snapshot has no trustworthy
		// generation: the current one is adopted below, not compared.
		if !old.stale && old.quota != nil && !sameQuotaIdentity(old.quota, cur) {
			return c.guardBlocked(ctx, ref, availReasonIdentity, "", c.model, 0, availabilityFailure(false))
		}
		if !sameModelList(old.models, models) {
			return c.guardBlocked(ctx, ref, availReasonModels, "", c.model, 0, availabilityFailure(false))
		}
		if old.now == c.g.now().Unix() && proto.Equal(old.quota, cur) {
			return nil
		}
	}
	pctx, cancel := context.WithTimeout(ctx, c.g.availabilityPluginBudget())
	defer cancel()
	r := quotaRead{
		snapshots: map[int64]*pluginv1.AccountQuotaSnapshot{ref.ID: cur},
		stale:     map[int64]bool{ref.ID: curStale},
		missing:   map[int64]bool{ref.ID: missing && cur == nil},
		whole:     missing && cur == nil,
		err:       read.err,
	}
	v := c.checkAvailability(ctx, pctx, []core.AccountRef{*ref}, &r)[ref.ID]
	if ctx.Err() != nil {
		return nil
	}
	if v.blocked {
		return c.guardBlocked(ctx, ref, availReasonUnavailable, v.detail, v.model, v.retryAt, availabilityFailure(true))
	}
	// A failure the precheck already recorded for this account (or plugin)
	// is not recorded again.
	if v.code != "" && !errors.Is(v.err, errAvailabilityFailedOpen) && !(admitted && v.code == old.reason) {
		c.appendGuardSample(core.AvailabilitySample{AccountID: ref.ID, Plugin: ref.PluginKey, Model: clipText(v.model, availModelBytes),
			Decision: core.AvailabilityFailOpen, Reason: availabilityReason(v.code, v.detail)})
		c.logFailOpen(ctx, ref.PluginKey, v.code, 1, v.err)
	}
	return nil
}

func sameModelList(old []string, cur []*pluginv1.ModelAvailabilityCandidate) bool {
	if len(old) != len(cur) {
		return false
	}
	for i, model := range cur {
		if old[i] != model.Model {
			return false
		}
	}
	return true
}

// guardBlocked records and logs a guard's refusal of the chosen account.
func (c *call) guardBlocked(ctx context.Context, ref *core.AccountRef, code, detail, model string, retryAt int64, failure *gwError) *gwError {
	reason := availabilityReason(code, detail)
	c.appendGuardSample(core.AvailabilitySample{AccountID: ref.ID, Plugin: ref.PluginKey, Model: clipText(model, availModelBytes),
		Decision: core.AvailabilityBlock, Reason: reason, RetryAt: retryAt})
	if ok, suppressed := c.g.availabilityWarns.allow("guard|"+ref.PluginKey+"|"+code, c.g.now()); ok {
		slog.InfoContext(ctx, "gateway: model availability guard blocked the account", "request_id", c.rid, "account", ref.ID,
			"model", model, "reason", reason, "attempt", c.attemptNumber(), "suppressed", suppressed)
	}
	return failure
}

// availabilityMismatch refuses an upstream request a plugin built for
// another model than the availability check admitted. It is a plugin bug,
// never a timing effect, so it is logged as an error.
func (c *call) availabilityMismatch(ctx context.Context, ref *core.AccountRef, expected, actual string) *gwError {
	c.appendGuardSample(core.AvailabilitySample{AccountID: ref.ID, Plugin: ref.PluginKey, Model: clipText(actual, availModelBytes),
		Decision: core.AvailabilityBlock, Reason: availReasonModelMismatch})
	if ok, suppressed := c.g.availabilityWarns.allow("mismatch|"+ref.PluginKey, c.g.now()); ok {
		slog.ErrorContext(ctx, "gateway: plugin built the upstream request for another model than availability admitted",
			"request_id", c.rid, "plugin", ref.PluginKey, "account", ref.ID, "expected", expected, "actual", clipText(actual, availModelBytes),
			"reason", availReasonModelMismatch, "suppressed", suppressed)
	}
	return availabilityFailure(false)
}

func (c *call) attemptNumber() int {
	if c.rec == nil {
		return 0
	}
	return c.rec.Attempts
}

// ---- trace and logs (§6.1, §6.3)

type availabilityEntry struct {
	id     int64
	plugin string
	v      availabilityVerdict
}

func availabilityReason(code, detail string) string {
	if detail = clipText(detail, availReasonBytes); detail != "" {
		return code + ":" + detail
	}
	return code
}

// validModelName: valid UTF-8, of any length, no whitespace and no control
// characters (CONTRACTS §15.2). The name is a key of account selection, the
// usage record, logs and Redis keys: whitespace or a control character in
// it would split or forge those, and proto3 requires valid UTF-8. Before the
// core checked this the only guard was a plugin's availability check
// answering UNKNOWN, which now lets a request through.
func validModelName(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// clipText drops control characters and invalid UTF-8 and cuts s to at most
// n bytes on a rune boundary.
func clipText(s string, n int) string {
	clean := true
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			clean = false
			break
		}
	}
	if !clean {
		s = strings.Map(func(r rune) rune {
			if r == utf8.RuneError || unicode.IsControl(r) {
				return -1
			}
			return r
		}, strings.ToValidUTF8(s, ""))
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// buildAvailabilityTrace sums up the accounts that were blocked or let
// through unchecked, nil when there were none.
func buildAvailabilityTrace(entries []availabilityEntry, candidates, admitted int, retryAt int64) *core.AvailabilityTrace {
	if len(entries) == 0 {
		return nil
	}
	tr := &core.AvailabilityTrace{V: 1, Candidates: candidates, Admitted: admitted, Counts: map[string]int{}, RetryAt: retryAt, Samples: []core.AvailabilitySample{}}
	blocked := false
	var samples []core.AvailabilitySample
	wholeSeen := map[string]bool{}
	for _, e := range entries {
		reason := availabilityReason(e.v.code, e.v.detail)
		key := reason
		if _, ok := tr.Counts[key]; !ok && len(tr.Counts) >= availTraceCountKeys-1 && key != availCountOther {
			key = availCountOther
		}
		tr.Counts[key]++
		decision := core.AvailabilityFailOpen
		if e.v.blocked {
			blocked, decision = true, core.AvailabilityBlock
		}
		if e.v.whole {
			// One sample per plugin call that failed, without accounts.
			if k := e.plugin + "|" + reason; !wholeSeen[k] {
				wholeSeen[k] = true
				samples = append(samples, core.AvailabilitySample{Plugin: e.plugin, Decision: decision, Reason: reason, Batch: true})
			}
			continue
		}
		samples = append(samples, core.AvailabilitySample{AccountID: e.id, Plugin: e.plugin, Model: clipText(e.v.model, availModelBytes),
			Decision: decision, Reason: reason, RetryAt: e.v.retryAt})
	}
	switch {
	case admitted == 0:
		tr.Outcome = core.AvailabilityBlockedAll
	case blocked:
		tr.Outcome = core.AvailabilityPartial
	default:
		tr.Outcome = core.AvailabilityFailOpen
	}
	// At least one sample per reason, the rest in candidate order.
	keep := make([]bool, len(samples))
	kept := 0
	reasons := map[string]bool{}
	for i, s := range samples {
		if kept < availTraceSamples && !reasons[s.Reason] {
			reasons[s.Reason], keep[i] = true, true
			kept++
		}
	}
	for i := range samples {
		if kept < availTraceSamples && !keep[i] {
			keep[i] = true
			kept++
		}
	}
	for i, s := range samples {
		if keep[i] {
			tr.Samples = append(tr.Samples, s)
		}
	}
	tr.Omitted = len(samples) - kept
	return tr
}

// setAvailabilityTrace records the precheck's trace on the usage record,
// keeping guard samples already there.
func (c *call) setAvailabilityTrace(tr *core.AvailabilityTrace) {
	if c.rec == nil {
		return
	}
	if old := c.rec.Availability; old != nil && len(old.Guard) > 0 {
		if tr == nil {
			old.Outcome, old.Candidates, old.Admitted = core.AvailabilityPass, c.availabilityChecked, c.availabilityAdmitted
			old.Counts, old.Samples, old.Omitted, old.RetryAt = map[string]int{}, []core.AvailabilitySample{}, 0, 0
			return
		}
		tr.Guard = old.Guard
	}
	c.rec.Availability = tr
}

func (c *call) appendGuardSample(s core.AvailabilitySample) {
	if c.rec == nil {
		return
	}
	s.Attempt = c.rec.Attempts
	tr := c.rec.Availability
	if tr == nil {
		tr = &core.AvailabilityTrace{V: 1, Outcome: core.AvailabilityPass, Candidates: c.availabilityChecked, Admitted: c.availabilityAdmitted,
			Counts: map[string]int{}, Samples: []core.AvailabilitySample{}}
		c.rec.Availability = tr
	}
	if len(tr.Guard) < availTraceGuard {
		tr.Guard = append(tr.Guard, s)
	}
}

// logAvailability writes the precheck's logs: one sampled WARN per plugin
// and reason that let accounts through unchecked, a sampled WARN when no
// candidate passed, a DEBUG when some did not.
func (c *call) logAvailability(ctx context.Context, entries []availabilityEntry, tr *core.AvailabilityTrace, none bool) {
	if tr == nil {
		return
	}
	type openKey struct{ plugin, code string }
	var order []openKey
	open := map[openKey]int{}
	errs := map[openKey]error{}
	for _, e := range entries {
		if e.v.blocked {
			continue
		}
		k := openKey{e.plugin, e.v.code}
		if _, ok := open[k]; !ok {
			order = append(order, k)
			errs[k] = e.v.err
		}
		open[k]++
	}
	for _, k := range order {
		c.logFailOpen(ctx, k.plugin, k.code, open[k], errs[k])
	}
	if tr.Outcome != core.AvailabilityBlockedAll && tr.Outcome != core.AvailabilityPartial {
		return
	}
	primary, most := "", 0
	for _, s := range tr.Samples {
		if n := tr.Counts[s.Reason]; n > most {
			primary, most = s.Reason, n
		}
	}
	now := c.g.now().Unix()
	var samples []string
	for _, s := range tr.Samples {
		if len(samples) == availLogSamples {
			break
		}
		line := fmt.Sprintf("account=%d reason=%s", s.AccountID, s.Reason)
		if s.Batch {
			line = fmt.Sprintf("plugin=%s reason=%s", s.Plugin, s.Reason)
		}
		if s.RetryAt > now {
			line += fmt.Sprintf(" retry_in=%ds", s.RetryAt-now)
		}
		samples = append(samples, line)
	}
	var group int64
	if c.principal != nil {
		group = c.principal.Group.ID
	}
	status := http.StatusTooManyRequests
	if !none {
		slog.DebugContext(ctx, "gateway: model availability blocked some candidates", "request_id", c.rid, "group", group,
			"model", c.model, "candidates", tr.Candidates, "admitted", tr.Admitted, "counts", tr.Counts, "samples", samples)
		return
	}
	// The key leaves out the model: it comes from the client, and a key per
	// model name would let a client defeat the sampling.
	ok, suppressed := c.g.availabilityWarns.allow(fmt.Sprintf("blocked|%d|%s", group, primary), c.g.now())
	if !ok {
		return
	}
	slog.WarnContext(ctx, "gateway: no candidate passed model availability", "request_id", c.rid, "group", group,
		"model", c.model, "status", status, "candidates", tr.Candidates, "counts", tr.Counts, "samples", samples, "suppressed", suppressed)
}

func (c *call) logFailOpen(ctx context.Context, plugin, code string, accounts int, err error) {
	if code == availReasonUnknown && err == nil {
		// The plugin answered; an UNKNOWN is its statement, not a failure,
		// but still a contract mismatch worth a sampled line.
		err = errors.New("plugin answered UNKNOWN")
	}
	ok, suppressed := c.g.availabilityWarns.allow("open|"+plugin+"|"+code, c.g.now())
	if !ok {
		return
	}
	slog.WarnContext(ctx, "gateway: model availability check failed open", "request_id", c.rid, "plugin", plugin,
		"reason", code, "accounts", accounts, "err", err, "suppressed", suppressed)
}

// ---- breaker (§5.2)

// availabilityBreakers is the node's circuit breaker per plugin for
// CheckModelAvailability: after a failed call (error, timeout, bad answer)
// the plugin is not asked for availabilityBreakerOpen, then one request
// probes it and a success closes the breaker. The zero value is ready.
type availabilityBreakers struct {
	mu    sync.Mutex
	state map[string]*availabilityBreaker
}

type availabilityBreaker struct {
	openUntil time.Time
	probing   bool
}

// allow says whether plugin may be called now and whether that call is the
// probe of an open breaker.
func (b *availabilityBreakers) allow(plugin string, now time.Time) (allowed, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[plugin]
	if s == nil {
		return true, false
	}
	if now.Before(s.openUntil) || s.probing {
		return false, false
	}
	s.probing = true
	return true, true
}

func (b *availabilityBreakers) done(plugin string, now time.Time, failed, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if failed {
		if b.state == nil {
			b.state = map[string]*availabilityBreaker{}
		}
		b.state[plugin] = &availabilityBreaker{openUntil: now.Add(availabilityBreakerOpen)}
		return
	}
	if probe {
		delete(b.state, plugin)
	}
}

// abandon gives up a probe whose request went away, so the next request
// probes instead.
func (b *availabilityBreakers) abandon(plugin string, probe bool) {
	if !probe {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.state[plugin]; s != nil {
		s.probing = false
	}
}

// ---- quota observations

var observationQuotaKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)

func validQuotaObservation(q *pluginv1.QuotaResult) bool {
	if q == nil || len(q.Windows) > manifest.MaxQuotaHeaders || q.NextCheckAtUnix < 0 || q.ErrorType < pluginv1.QuotaResult_ERROR_TYPE_UNSPECIFIED || q.ErrorType > pluginv1.QuotaResult_ERROR_TYPE_TRANSIENT {
		return false
	}
	seen := map[string]bool{}
	for _, w := range q.Windows {
		if w == nil || !observationQuotaKey.MatchString(w.Key) || seen[w.Key] || math.IsNaN(w.Utilization) || math.IsInf(w.Utilization, 0) || w.Utilization < 0 || w.Used < 0 || w.Limit < 0 || w.ResetsAtUnix < 0 || w.Status != manifest.NormalizeQuotaStatus(w.Status) {
			return false
		}
		if w.UtilizationUnknown && (w.Utilization != 0 || w.Used != 0 || w.Limit != 0) {
			return false
		}
		seen[w.Key] = true
	}
	return true
}

// applyQuotaObservation writes what the plugin learned from an upstream
// answer back to the account's snapshot, under the generation the request
// was admitted with (CONTRACTS §44.7): an admission without a snapshot, and
// a WebSocket turn whose generation is not the one the connection was made
// with, write nothing.
func (c *call) applyQuotaObservation(ctx context.Context, id int64, q *pluginv1.QuotaResult) {
	generation := c.availability[id].quota.GetGeneration()
	if q == nil || generation <= 0 || c.quotaPinned && generation != c.quotaPin {
		return
	}
	if !validQuotaObservation(q) {
		slog.WarnContext(ctx, "gateway: invalid quota observation", "account", id)
		return
	}
	if source := c.g.quotaSource(); source != nil {
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaObservationTimeout)
		defer cancel()
		if err := source.ApplyAccountQuotaObservation(qctx, id, generation, q); err != nil {
			slog.WarnContext(ctx, "gateway: apply quota observation", "account", id, "err", err)
		}
	}
}
