package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Bounds of a SchedulingTrace (CONTRACTS §24.6).
const (
	maxSchedSkips     = 40
	maxSchedCountKeys = 16
	maxSchedDetail    = 100
)

// schedTrace collects, while a request is scheduled, each account that was
// not used and why. It costs a slice append per skipped account and is only
// turned into a record when the request found no account or passed one over
// for a passing reason (schedulingTrace).
type schedTrace struct {
	candidates, eligible int
	skips                []core.SchedulingSkip
	seen                 map[schedSkipKey]struct{}
	omitted              int
	// passing: an account was passed over for something that changes from
	// one request to the next (rate limit, concurrency, a failed attempt),
	// which is worth a record even when another account served.
	passing bool
	// unschedulable: the accounts of the group that were never offered, read
	// once when no account could be picked.
	unschedulable []core.UnschedulableAccount
	concluded     bool
}

type schedSkipKey struct {
	id     int64
	reason string
}

// passingSchedReason reports whether reason is a state of the moment rather
// than of the account's configuration and the request's shape.
func passingSchedReason(reason string) bool {
	switch reason {
	case core.SchedRateLimited, core.SchedConcurrencyFull, core.SchedQueued, core.SchedAttemptFailed, core.SchedFailurePolicy:
		return true
	}
	return false
}

// skipAccount notes that account id is not used for this request. An account
// is noted once per reason; the first detail stands.
func (c *call) skipAccount(id int64, reason, detail string) {
	t := &c.sched
	t.passing = t.passing || passingSchedReason(reason)
	key := schedSkipKey{id, reason}
	if _, ok := t.seen[key]; ok {
		return
	}
	if t.seen == nil {
		t.seen = map[schedSkipKey]struct{}{}
	}
	t.seen[key] = struct{}{}
	if len(t.skips) >= maxSchedSkips {
		t.omitted++
		return
	}
	t.skips = append(t.skips, core.SchedulingSkip{AccountID: id, Reason: reason, Detail: truncateUTF8(detail, maxSchedDetail)})
}

// protocolSchedReason reports whether reason is one an account's protocol
// binding leaves a request out for (core.ProtocolRules.Serves).
func protocolSchedReason(reason string) bool {
	switch reason {
	case core.SchedProtocolClosed, core.SchedConversionNotEnabled, core.SchedModelProtocolClosed:
		return true
	}
	return false
}

// ineligibleReason says why ref cannot serve this request whatever its load;
// ok is true when it can. It is the one test of that for the first pick, a
// refreshed snapshot and a later turn of a WebSocket (candidateEligible), and
// only compares what the snapshot holds: an account it refuses is not
// converted for, checked for availability, bound or counted as an attempt.
func (c *call) ineligibleReason(ref *core.AccountRef) (reason, detail string, ok bool) {
	rt := c.route(ref)
	if rt == nil {
		return core.SchedNoRoute, ref.PluginKey + "/" + ref.Type, false
	}
	if reason, detail, ok := c.protocolClosedReason(ref, rt); !ok {
		return reason, detail, false
	}
	if !ref.ServesModel(c.model) {
		return core.SchedModelNotServed, c.model, false
	}
	if kind := c.missingRouteUsage(rt); kind != "" {
		return core.SchedUsageUnsupported, kind, false
	}
	for _, extra := range c.modelRefs {
		if !ref.ServesModel(extra.model) {
			return core.SchedReferencedModelNotServed, extra.model, false
		}
		if c.ep.Billing != "free" && !routeAccountsFor(rt, extra.kind) {
			return core.SchedUsageUnsupported, extra.kind, false
		}
	}
	return "", "", true
}

// missingRouteUsage is the usageGap of the request's body on rt, judged once
// per route and body, not once per candidate: it never reads the account,
// and it scans the body, a megabyte for a long conversation. setBody drops
// the verdicts.
func (c *call) missingRouteUsage(rt *typeRoute) string {
	gap, ok := c.routeUsage[rt]
	if !ok {
		gap = c.usageGap(rt, c.body)
		if c.routeUsage == nil {
			c.routeUsage = map[*typeRoute]string{}
		}
		c.routeUsage[rt] = gap
	}
	return gap
}

// protocolClosedReason says why the account's protocol binding leaves the
// request out: the client endpoint's protocol, reached over rt, is not one
// the account serves the request's model, or a model it references, on.
func (c *call) protocolClosedReason(ref *core.AccountRef, rt *typeRoute) (reason, detail string, ok bool) {
	model := c.model
	reason, ok = ref.ProtocolRules.Serves(model, c.ep.Protocol, rt.cross)
	for i := 0; ok && i < len(c.modelRefs); i++ {
		model = c.modelRefs[i].model
		reason, ok = ref.ProtocolRules.Serves(model, c.ep.Protocol, rt.cross)
	}
	switch {
	case ok:
		return "", "", true
	case reason == core.SchedProtocolClosed:
		return reason, c.ep.Protocol, false
	}
	return reason, model, false
}

// eligibleAccounts keeps the accounts of all able to serve the request
// whatever their load, and notes why each of the others is not. closed is
// set when accounts were offered and every one is left out by its protocol
// binding.
func (c *call) eligibleAccounts(all []core.AccountRef) (cands []core.AccountRef, closed bool) {
	c.sched.candidates = len(all)
	cands = make([]core.AccountRef, 0, len(all))
	unserved := 0
	for i := range all {
		reason, detail, ok := c.ineligibleReason(&all[i])
		if ok {
			cands = append(cands, all[i])
			continue
		}
		c.skipAccount(all[i].ID, reason, detail)
		if protocolSchedReason(reason) {
			unserved++
		}
	}
	return cands, unserved > 0 && unserved == len(all)
}

// protocolNotServed is the answer to a request every account offered is
// closed to by its protocol binding (closed, from eligibleAccounts): a 404,
// which clients do not retry, since no account serves the model on the
// endpoint until one is configured to. The accounts of the group that are
// only out for a while (cooling down, or disabled until a time) are judged
// like the offered ones, from the same snapshot: nil when one of them is not
// closed by its binding, since it may serve the request once back, or when
// they cannot be read. The request then ends as any passing shortage does.
func (c *call) protocolNotServed(ctx context.Context, closed bool) *gwError {
	if !closed {
		return nil
	}
	resting, err := c.g.d.Accounts.Resting(ctx, c.principal.Group.ID, c.routeKeys)
	if err != nil {
		slog.WarnContext(ctx, "gateway: read resting accounts", "err", err)
		return nil
	}
	for i := range resting {
		if reason, _, ok := c.ineligibleReason(&resting[i]); ok || !protocolSchedReason(reason) {
			return nil
		}
	}
	return errProtocolNotServed
}

// noteDropped notes the accounts of before that are not in after.
func (c *call) noteDropped(before, after []core.AccountRef, reason string) {
	for i := range before {
		id := before[i].ID
		if !slices.ContainsFunc(after, func(r core.AccountRef) bool { return r.ID == id }) {
			c.skipAccount(id, reason, "")
		}
	}
}

// concludeScheduling runs when the record is finished. A request that found no account
// reads the accounts of the group that were never offered (disabled, cooling
// down), so the record says what the group holds, and logs one line: the
// client only sees "no available account" or "busy".
func (c *call) concludeScheduling(ctx context.Context) {
	t := &c.sched
	if t.concluded || c.rec == nil || c.rec.ErrorType != errTypeNoAccount {
		return
	}
	t.concluded = true
	if c.principal != nil && c.g.d.Accounts != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stickyRedisTimeout)
		off, err := c.g.d.Accounts.Unschedulable(rctx, c.principal.Group.ID)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "gateway: read unschedulable accounts", "err", err)
		}
		t.unschedulable = off
	}
	tr := c.schedulingTrace()
	reasons := make([]string, 0, len(tr.Skipped))
	for _, s := range tr.Skipped {
		text := strconv.FormatInt(s.AccountID, 10) + ":" + s.Reason
		if s.Detail != "" {
			text += ":" + s.Detail
		}
		reasons = append(reasons, text)
	}
	slog.WarnContext(ctx, "gateway: no account for the request", "request_id", c.rid, "group", c.principal.Group.ID,
		"model", c.model, "protocol", c.ep.Protocol, "outcome", tr.Outcome, "candidates", tr.Candidates,
		"eligible", tr.Eligible, "skipped", strings.Join(reasons, " "), "omitted", tr.Omitted)
}

// schedulingTrace is usage_logs.anomalies.scheduling: nil unless the request
// found no account or passed one over for a passing reason.
func (c *call) schedulingTrace() *core.SchedulingTrace {
	t := &c.sched
	none := c.rec != nil && c.rec.ErrorType == errTypeNoAccount
	if !none && !t.passing {
		return nil
	}
	tr := &core.SchedulingTrace{V: 1, Outcome: core.SchedulingPicked, Candidates: t.candidates, Eligible: t.eligible,
		Counts: map[string]int{}, Skipped: slices.Clone(t.skips), Omitted: t.omitted}
	if none {
		tr.Outcome = core.SchedulingNone
		if c.rec.StatusCode == http.StatusTooManyRequests {
			tr.Outcome = core.SchedulingBusy
		}
	}
	for _, u := range t.unschedulable {
		s := core.SchedulingSkip{AccountID: u.ID, Reason: core.SchedNotSchedulable, Detail: u.Reason}
		if !u.Until.IsZero() {
			s.Until = u.Until.Unix()
		}
		// An account that left during the request is already listed, without
		// what it left for.
		if i := slices.IndexFunc(tr.Skipped, func(o core.SchedulingSkip) bool {
			return o.AccountID == u.ID && o.Reason == core.SchedNotSchedulable
		}); i >= 0 {
			tr.Skipped[i] = s
		} else if len(tr.Skipped) >= maxSchedSkips {
			tr.Omitted++
		} else {
			tr.Skipped = append(tr.Skipped, s)
		}
	}
	for _, s := range tr.Skipped {
		if _, ok := tr.Counts[s.Reason]; ok || len(tr.Counts) < maxSchedCountKeys {
			tr.Counts[s.Reason]++
		}
	}
	if tr.Skipped == nil {
		tr.Skipped = []core.SchedulingSkip{}
	}
	return tr
}

// attemptFailureDetail is the detail of an attempt_failed skip: the status the
// attempt ended with and how the record classifies it.
func attemptFailureDetail(e *gwError) string {
	if e == nil {
		return ""
	}
	return strconv.Itoa(e.Status) + " " + e.RecordType
}
