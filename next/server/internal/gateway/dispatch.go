package gateway

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
)

type attemptKind int

const (
	attemptDone     attemptKind = iota // response delivered (or stream ended)
	attemptFailover                    // try another account
	attemptReturn                      // send the error to the client
	attemptCanceled                    // client went away
)

type attemptResult struct {
	kind attemptKind
	err  *gwError
}

// dispatch schedules accounts and forwards the request, failing over up to
// max_attempts times. Once bytes reached the client it never fails over.
func (c *call) dispatch(ctx context.Context) {
	all, err := c.g.d.Accounts.Candidates(ctx, c.principal.Group.ID, c.routeKeys)
	if err != nil {
		c.fail(fromCore(core.ErrUnavailable.WithCause(err), errTypeInternal))
		return
	}
	if len(all) == 0 {
		// No account of the group serves the endpoint, now or after any
		// cooldown: answer 404 rather than an overload the client retries.
		if has, err := c.g.d.Accounts.GroupHasType(ctx, c.principal.Group.ID, c.routeKeys); err == nil && !has {
			c.fail(errEndpointNotServed)
			return
		}
	}
	cands, closed := c.eligibleAccounts(all)
	// Every account offered is closed to the request by its protocol
	// binding: a 404 like the one above, before anything is checked, bound
	// or converted for the request.
	if e := c.protocolNotServed(ctx, closed); e != nil {
		c.fail(e)
		return
	}
	eligible := cands
	cands, availabilityErr := c.filterAvailability(ctx, cands)
	c.noteDropped(eligible, cands, core.SchedAvailabilityBlocked)
	c.sched.eligible = len(cands)
	if availabilityErr != nil {
		c.fail(availabilityErr)
		return
	}
	c.sticky = c.resolveSticky(ctx)
	if c.failureState.stickyErr != nil {
		c.fail(fromCore(core.ErrUnavailable.WithMessage("sticky store unavailable"), errTypeInternal))
		return
	}
	c.session = c.sessionIdentity(core.RequestSession)
	excluded := map[int64]bool{}
	var last *gwError
	var lastAccount int64
	attempts := 0
	defer func() {
		c.rec.Attempts = attempts
	}()
	for attempts < c.attemptLimit() {
		available, err := c.policyCandidates(ctx, cands)
		if err != nil {
			c.fail(fromCore(core.ErrUnavailable.WithMessage("account selection unavailable"), errTypeInternal))
			return
		}
		ref, release, busy := c.pick(ctx, available, excluded)
		if ref == nil {
			c.endFailurePolicy("no_eligible_account")
			if last == nil {
				if ctx.Err() != nil {
					// The client left while queueing for an account.
					last = canceledErr()
				} else if busy {
					last = fromCore(core.ErrRateLimited.WithMessage("all accounts are busy or rate limited, please retry later"), errTypeNoAccount)
				} else if e := c.unsupportedConversion(); e != nil {
					// Every candidate was of a type the request does not
					// convert to, or refused by its loss policy (§88).
					last = e
				} else {
					last = fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
				}
			}
			break
		}
		defer release()
		if c.g.d.Limiter != nil {
			admitted, err := c.admitRate(ref)
			if err != nil {
				release()
				c.fail(fromCore(core.ErrUnavailable.WithMessage("account limits unavailable").WithCause(err), errTypeInternal))
				return
			}
			if !admitted {
				release()
				excluded[ref.ID] = true
				c.skipAccount(ref.ID, core.SchedRateLimited, "")
				if c.sticky != nil {
					c.sticky.hit = false
				}
				last = fromCore(core.ErrRateLimited.WithMessage("all accounts are busy or rate limited, please retry later"), errTypeNoAccount)
				continue
			}
		}
		// The account is chosen: plugins that decide per account (e.g. one
		// exempt from a check another is not) see it before anything is sent
		// to it, on every attempt (CONTRACTS §85).
		if e := c.runAttemptHooks(ctx, ref); e != nil {
			release()
			c.fail(e)
			c.finishSticky(ctx, 0, false)
			return
		}
		attempts++
		c.rec.Attempts = attempts
		stickyAttempt := c.sticky != nil && c.sticky.hit && c.sticky.bound == ref.ID
		c.startObservationAttempt(ref)
		c.beginFailureAttempt(ref)
		begun := c.g.now()
		c.rec.ErrorMessage = "" // each attempt states its own failure (noteAttempt)
		res := func() attemptResult { defer release(); return c.attempt(c.slotCtx, ref, attempts-1) }()
		c.noteAttempt(ref.ID, res.err, begun)
		res = c.planFailure(ctx, ref, res, attempts, excluded)
		c.observeAttemptResult(ref, res)
		lastAccount = ref.ID
		if res.kind != attemptDone {
			c.releaseAccountHold(ctx)
		}
		switch res.kind {
		case attemptDone:
			c.countTokens(ctx, ref.ID)
			c.finishSticky(ctx, ref.ID, c.rec.Success)
			if c.rec.Success {
				c.g.d.Accounts.TouchLastUsed(context.WithoutCancel(ctx), ref.ID)
			}
			return
		case attemptCanceled:
			c.fail(res.err)
			c.finishSticky(ctx, ref.ID, false)
			return
		case attemptReturn:
			c.fail(res.err)
			c.finishSticky(ctx, ref.ID, false)
			return
		case attemptFailover:
			last = res.err
			if c.failOver(ctx, ref, res, excluded, stickyAttempt) {
				c.fail(last)
				c.finishSticky(ctx, ref.ID, false)
				return
			}
		}
	}
	if last == nil || (last.RecordType != errTypeUpstream && last.RecordType != errTypeInvalidRequest && last.RecordType != errTypeModelAvailability &&
		last.Status != http.StatusTooManyRequests) {
		// Only upstream answers (and requests no upstream protocol can
		// express) are worth relaying; plugin or account problems surface
		// as "no available account".
		last = fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
	}
	if lastAccount != 0 && c.rec.AccountID == nil {
		id := lastAccount
		c.rec.AccountID = &id
	}
	c.fail(last)
	c.finishSticky(ctx, 0, false)
}

// failOver moves the request off ref after a failed attempt (HTTP and a
// WebSocket handshake alike). With a failure policy, planFailure has done
// so already (or kept ref for a retry). Without one ref is excluded, and the
// session's sticky binding to ref and its session slot there are released,
// unless the sticky rule that put the session on ref keeps it there on
// failure ("stick": protect the upstream cache): stick is true and the
// caller ends the request. stickyAttempt says the attempt was the binding's.
func (c *call) failOver(ctx context.Context, ref *core.AccountRef, res attemptResult, excluded map[int64]bool, stickyAttempt bool) (stick bool) {
	if c.failurePolicy != nil {
		return false
	}
	excluded[ref.ID] = true
	c.skipAccount(ref.ID, core.SchedAttemptFailed, attemptFailureDetail(res.err))
	if stickyAttempt && c.sticky.rule.OnFailure == onFailureStick {
		return true
	}
	if err := c.releaseBinding(ctx, ref.ID); err != nil {
		slog.WarnContext(ctx, "gateway: release sticky binding", "err", err)
	}
	c.releaseSession(ctx, ref)
	return false
}

// sessionIdentity is the request's session for the limiter's session caps
// (CONTRACTS §71): the sticky session key when a rule matched, else a new
// session of the request alone (as new-api does), which own names from the
// request ID: RequestSession, or ConnectionSession for a WebSocket.
func (c *call) sessionIdentity(own func(requestID string) string) string {
	if c.sticky != nil {
		return c.sticky.key
	}
	return own(c.rid)
}

// pick chooses the next account: the sticky binding first (when usable),
// then by priority, higher first, and within a priority by route grade,
// native first (CONVERT-MATRIX §5.2), with weighted random order inside a
// (priority, grade) tier (CONTRACTS §18), over the priority/weight the
// scheduler.rank plugins left for this request (CONTRACTS §24). Accounts
// whose rate-limit window is exhausted are skipped, and a converting route
// converts the request only when one of its accounts comes up (usable). It
// returns busy when candidates exist but none could be used right now.
func (c *call) pick(ctx context.Context, cands []core.AccountRef, excluded map[int64]bool) (*core.AccountRef, func(), bool) {
	exhausted := c.exhausted(ctx, cands, excluded)
	if ref, release, _ := c.tryStickyBinding(ctx, cands, excluded, exhausted); ref != nil {
		return ref, release, false
	}
	// CONTRACTS §18.1: a stick rule does not leave a bound account that has no
	// room, it answers 429.
	if c.sticky != nil && c.sticky.refused {
		return nil, nil, true
	}
	// Plugins may rewrite the priority/weight of the candidates for this
	// request (CONTRACTS §24); everything below still gates them. A plugin's
	// weight 0 takes the account out of this request for good: the ranking is
	// computed once and reused by failover, so a zeroed account is not tried
	// when its tier's weighted accounts failed, are rate-limited or busy, the
	// request moves on to the next tier (§24.3). A plugin that
	// wants a tier it zeroed entirely split evenly gives each account 1.
	over := c.rankOverrides(ctx, cands)
	c.readBalance(ctx, cands)
	var pool, limited []core.AccountRef
	for i := range cands {
		if excluded[cands[i].ID] {
			continue
		}
		if !c.routed(&cands[i]) {
			c.skipAccount(cands[i].ID, core.SchedConversionRefused, "")
			continue
		}
		ref := cands[i]
		if o, ok := over[ref.ID]; ok {
			if o.weight == 0 {
				c.skipAccount(ref.ID, core.SchedRankExcluded, "")
				continue
			}
			ref.Priority, ref.Weight = o.priority, o.weight
		}
		if exhausted[ref.ID] {
			c.skipAccount(ref.ID, core.SchedRateLimited, "")
			limited = append(limited, ref)
			continue
		}
		pool = append(pool, ref)
	}
	c.orderPool(pool)
	queued := c.queuedAccounts(ctx, pool)
	for i := range pool {
		ref := pool[i]
		if queued[ref.ID] {
			c.skipAccount(ref.ID, core.SchedQueued, "")
			continue
		}
		if !c.usable(ctx, &ref) {
			c.skipAccount(ref.ID, core.SchedConversionRefused, "")
			continue
		}
		if release, ok := c.acquireAccount(ctx, &ref); ok {
			return c.finishPick(ctx, &ref, release, cands, excluded)
		}
		c.skipAccount(ref.ID, core.SchedConcurrencyFull, "")
	}
	// Busy only when an account that could serve the request is full or
	// rate limited, not when every one was refused for its conversion.
	usable := func(r core.AccountRef) bool { return c.usable(ctx, &r) }
	if pool = slices.DeleteFunc(pool, func(r core.AccountRef) bool { return !usable(r) }); len(pool) == 0 {
		return nil, nil, slices.ContainsFunc(limited, usable)
	}
	if ref := c.queueTarget(ctx, pool); ref != nil {
		if release, ok := c.waitForAccount(ctx, ref, c.gw.QueueFallbackMaxWaiting, c.gw.queueFallbackWait()); ok {
			return c.finishPick(ctx, ref, release, cands, excluded)
		}
	}
	return nil, nil, true
}

// orderPool sorts the pool by priority, higher first, then by route grade,
// better first, each (priority, grade) tier in weighted random order over the
// weights the loads and the core balancing give it (balance.go).
func (c *call) orderPool(pool []core.AccountRef) {
	grade := func(r *core.AccountRef) convert.Grade { return c.route(r).grade }
	slices.SortStableFunc(pool, func(a, b core.AccountRef) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(grade(&a), grade(&b)))
	})
	for i := 0; i < len(pool); {
		j := i + 1
		for j < len(pool) && pool[j].Priority == pool[i].Priority && grade(&pool[j]) == grade(&pool[i]) {
			j++
		}
		weightedOrder(pool[i:j], c.tierWeights(pool[i:j]), c.g.randFloat)
		i = j
	}
	if c.balance != nil {
		c.balance.recorded = true // failover orders again without recording
	}
}

// finishPick settles an account picked outside the binding: a session without
// one is bound to it now, and when a concurrent request of the session got
// there first this one follows that request instead.
func (c *call) finishPick(ctx context.Context, ref *core.AccountRef, release func(), cands []core.AccountRef, excluded map[int64]bool) (*core.AccountRef, func(), bool) {
	if s := c.sticky; s != nil {
		if winner := c.claimBinding(ctx, ref.ID); winner != 0 && winner != ref.ID {
			release()
			s.bound, s.tried = winner, false
			return c.pick(ctx, cands, excluded)
		}
	}
	return ref, release, false
}

// queuedAccounts are the accounts a new session must not be bound to because
// requests are queueing for them: it would only add to their queue while other
// accounts are free. Every account being queued for still takes it in
// queueTarget.
func (c *call) queuedAccounts(ctx context.Context, pool []core.AccountRef) map[int64]bool {
	if s := c.sticky; s == nil || s.bound != 0 || !c.queueable() {
		return nil
	}
	ids := make([]int64, len(pool))
	for i := range pool {
		ids[i] = pool[i].ID
	}
	waiting, err := c.g.d.Slots.InUseMany(ctx, core.AccountWaitSlots, ids)
	if err != nil {
		slog.WarnContext(ctx, "gateway: read account queues", "err", err)
		return nil
	}
	queued := map[int64]bool{}
	for id, n := range waiting {
		if n > 0 {
			queued[id] = true
		}
	}
	return queued
}

// tryStickyBinding honours this request's sticky binding once. ok reports
// that a sticky binding was handled (hit or missed); ref is non-nil only on a
// hit. A bound account that is absent from the schedulable set for this
// request loses the binding unless it is merely cooling down.
func (c *call) tryStickyBinding(ctx context.Context, cands []core.AccountRef, excluded map[int64]bool, exhausted map[int64]bool) (*core.AccountRef, func(), bool) {
	s := c.sticky
	if s == nil || s.tried {
		return nil, nil, false
	}
	s.tried = true
	if s.bound == 0 || excluded[s.bound] {
		return nil, nil, true
	}
	for i := range cands {
		if cands[i].ID != s.bound {
			continue
		}
		bound := &cands[i]
		// An unusable type (request not convertible, or refused by the
		// account's loss policy) keeps the binding; an exhausted rate window
		// counts as "no free slot".
		if c.usable(ctx, bound) {
			if !exhausted[bound.ID] {
				release, ok := c.acquireAccount(ctx, bound)
				if !ok {
					release, ok = c.waitForAccount(ctx, bound, c.gw.QueueStickyMaxWaiting, c.gw.queueStickyWait())
				}
				if ok {
					s.hit = true
					return bound, release, true
				}
				c.skipAccount(bound.ID, core.SchedConcurrencyFull, "")
			} else {
				c.skipAccount(bound.ID, core.SchedRateLimited, "")
			}
			s.refused = s.rule.OnFailure == onFailureStick
		}
		return nil, nil, true
	}
	if cooling, err := c.g.d.Accounts.IsCoolingDown(ctx, s.bound); err == nil && !cooling {
		// Not in the group's schedulable set for this request and
		// not merely cooling down: disabled, deleted, unschedulable,
		// moved, out of quota, or of a type that cannot serve this
		// endpoint. The next account takes the session; only the binding
		// keeps its requests from scattering, and a request that overflows
		// does not move it.
		c.dropBinding(ctx, s)
	}
	return nil, nil, true
}

// exhausted asks the limiter which candidates reached a rate limit. Limiter
// failures do not block scheduling.
func (c *call) exhausted(ctx context.Context, cands []core.AccountRef, excluded map[int64]bool) map[int64]bool {
	lim := c.g.d.Limiter
	if lim == nil || c.tokenCounting() {
		return nil
	}
	var limited []core.AccountRef
	for i := range cands {
		if cands[i].Limited() && !excluded[cands[i].ID] {
			limited = append(limited, cands[i])
		}
	}
	if len(limited) == 0 {
		return nil
	}
	out, err := lim.Exhausted(ctx, limited, c.model, c.session)
	if err != nil {
		slog.WarnContext(ctx, "gateway: read rate limits", "err", err)
		return nil
	}
	return out
}

// weightedOrder reorders grp by weighted random sampling without
// replacement: an account is drawn first with probability proportional to
// its weight w[i] (> 0, reordered along with grp). rnd yields [0,1).
func weightedOrder(grp []core.AccountRef, w []float64, rnd func() float64) {
	for i := 0; i < len(grp)-1; i++ {
		total := 0.0
		for j := i; j < len(grp); j++ {
			total += w[j]
		}
		r := rnd() * total
		for j := i; j < len(grp); j++ {
			r -= w[j]
			if r < 0 || j == len(grp)-1 {
				grp[i], grp[j] = grp[j], grp[i]
				w[i], w[j] = w[j], w[i]
				break
			}
		}
	}
}

// tokenCounting reports a token counting request. It generates nothing, so it
// consumes nothing: no key or account concurrency, no RPM or TPM, no quota, and
// a failed count says nothing about the account, which keeps its state
// (2026-11-04). An account that is cooling or disabled still does not serve it.
// It has nothing to do with sticky sessions: the account is picked by weight,
// and no binding is read or written (resolveSticky).
func (c *call) tokenCounting() bool {
	return strings.HasSuffix(c.ep.Protocol, ".count_tokens")
}

// slotSpec is one concurrency slot a request takes on an account.
type slotSpec struct {
	kind  string
	limit int // 0 = uncapped
}

// accountSlots are the slots a request for the call's model takes on ref:
// the account's, that of the model list holding the model (CONTRACTS §68),
// and the model's own, uncapped, which only counts it for the per-model
// breakdown of GET /accounts/:id/rate-models.
func (c *call) accountSlots(ref *core.AccountRef) []slotSpec {
	out := []slotSpec{{kind: "account", limit: ref.MaxConcurrency}}
	if l := ref.ModelLimitFor(c.model); l != nil {
		out = append(out, slotSpec{kind: core.ModelLimitSlots(l.Key()), limit: l.MaxConcurrency})
	}
	if c.model != "" {
		out = append(out, slotSpec{kind: core.ModelSlots(c.model)})
	}
	return out
}

// acquireAccount takes every slot of accountSlots, in order; one being full
// makes the account busy and gives back those already taken. The release
// gives them back in reverse order.
func (c *call) acquireAccount(ctx context.Context, ref *core.AccountRef) (func(), bool) {
	if c.tokenCounting() {
		c.slotCtx = ctx
		return func() {}, true
	}
	release := func() {}
	for _, spec := range c.accountSlots(ref) {
		leaseCtx, releaseSlot, ok, err := core.AcquireSlot(ctx, c.g.d.Slots, spec.kind, ref.ID, spec.limit, c.rid)
		if err != nil {
			slog.WarnContext(ctx, "gateway: acquire account slot", "account", ref.ID, "slot", spec.kind, "err", err)
		}
		if err != nil || !ok {
			release()
			return nil, false
		}
		releaseOuter := release
		ctx, release = leaseCtx, func() { releaseSlot(); releaseOuter() }
	}
	c.slotCtx = ctx
	return release, true
}

// admitRate asks the limiter to admit one request of the call's model and
// session on ref, reserving its estimated tokens where a TPM cap applies, and
// keeps the hold its tokens settle against.
func (c *call) admitRate(ref *core.AccountRef) (bool, error) {
	if c.tokenCounting() {
		return true, nil
	}
	c.releaseAccountHold(c.slotCtx)
	tokens := int64(0)
	if l := ref.ModelLimitFor(c.model); ref.TPMLimit > 0 || (l != nil && l.TPMLimit > 0) {
		tokens = c.rateTokens(c.slotCtx)
	}
	hold, ok, err := c.g.d.Limiter.Reserve(c.slotCtx, *ref, c.model, c.session, tokens)
	if ok && err == nil {
		c.accountHold, c.sessionAt = &hold, hold.At
	}
	return ok, err
}

// attempt runs one upstream attempt on account ref.
func (c *call) attempt(ctx context.Context, ref *core.AccountRef, n int) attemptResult {
	unavailable := func(msg string, err error) attemptResult {
		slog.WarnContext(ctx, "gateway: account unavailable", "account", ref.ID, "reason", msg, "err", err)
		return attemptResult{kind: attemptFailover,
			err: fromCore(core.ErrNoAvailableAccount.WithCause(err), errTypeNoAccount)}
	}
	acc, err := c.g.d.Accounts.Load(ctx, ref.ID)
	if err != nil {
		return unavailable("load account", err)
	}
	if c.failurePolicy != nil && proxyIdentity(acc.ProxyID) != proxyIdentity(ref.ProxyID) {
		return attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("account proxy changed during selection"), errTypeInternal)}
	}
	rt := c.route(ref)
	if rt == nil || rt.binding.Client == nil {
		return unavailable("account type not enabled", errors.New(ref.PluginKey+"/"+ref.Type))
	}
	c.env = c.environment(&acc.AccountRef)
	id := acc.ID
	c.rec.AccountID = &id
	c.rec.PluginKey, c.rec.PluginVersion = rt.binding.Plugin.Key, rt.binding.Plugin.Version
	c.rec.AccountType = rt.binding.Type.ID
	c.rec.UpstreamProtocol = rt.upstream
	c.rec.UsageSemantics = rt.usage.Semantics
	if c.rec.UsageSemantics == "" {
		c.rec.UsageSemantics = "exclusive"
	}
	c.serveRoute(rt, ref)
	if rt.local {
		return c.countLocally(ctx)
	}
	// The request was converted for the route when the account was picked;
	// the body is final by then, so this only reads that result.
	upBody := c.body
	if rt.conv != nil {
		if !c.prepareRoute(ctx, rt) {
			return unavailable("request conversion failed", rt.prepared.err)
		}
		upBody = rt.body
	}
	upBody, upModel, failure := c.upstreamBody(ctx, acc, ref, rt, upBody, n)
	if failure != nil {
		return *failure
	}
	if failure := c.guardAvailability(ctx, ref); failure != nil {
		return attemptResult{kind: attemptFailover, err: failure}
	}
	return c.execute(ctx, rt, acc, upBody, upModel, n)
}

// upstreamBody is body as the plugin receives it for attempt n on ref
// (design §4.5): the account's model mapping (CONTRACTS §18; billing and
// hooks saw the client model), the referenced models' mapping, stream: true
// for an upstream that only streams (§83), then the built-in override
// (claude_max_tokens, §84.1) and the account's parameter override (§84),
// which also decide the headers (param_override.go). A body the overrides
// changed gets the forced stream again.
// It returns the body and the model it goes upstream with, or the
// attempt's end.
func (c *call) upstreamBody(ctx context.Context, acc *core.Account, ref *core.AccountRef, rt *typeRoute, body []byte, n int) ([]byte, string, *attemptResult) {
	var err error
	upModel := ref.MapModel(c.model)
	if upModel != c.model {
		if rt.modelPath != "" && gjson.GetBytes(body, rt.modelPath).Exists() {
			if body, err = sjson.SetBytes(body, rt.modelPath, upModel); err != nil {
				return nil, "", &attemptResult{kind: attemptFailover, err: fromCore(core.ErrInternal.WithCause(err), errTypeInternal)}
			}
		}
		c.rec.UpstreamModel = upModel
	}
	if body, err = c.mapReferencedModels(body, ref, rt); err != nil {
		return nil, "", &attemptResult{kind: attemptFailover, err: invalidModelReference(err.Error())}
	}
	if body, err = c.forceStream(rt, body); err != nil {
		return nil, "", &attemptResult{kind: attemptFailover, err: fromCore(core.ErrInternal.WithCause(err), errTypeInternal)}
	}
	rewritten, upModel, failure := c.rewriteBody(ctx, acc, rt, body, upModel, n)
	if failure != nil {
		return nil, "", failure
	}
	if bytes.Equal(rewritten, body) {
		return body, upModel, nil
	}
	// An override may not undo the forced stream; set it again on what the
	// overrides made of the body.
	if rewritten, err = c.forceStream(rt, rewritten); err != nil {
		return nil, "", &attemptResult{kind: attemptFailover, err: fromCore(core.ErrInternal.WithCause(err), errTypeInternal)}
	}
	return rewritten, upModel, nil
}

// forceStream sets stream: true on body for an upstream that only streams
// (forcesStream); any other body is returned as it is.
func (c *call) forceStream(rt *typeRoute, body []byte) ([]byte, error) {
	if !c.forcesStream(rt) {
		return body, nil
	}
	return forceStreamPath(body, rt.streamPath)
}

// layerHeaders writes the headers of a plugin's relay onto h in the layers
// of CONTRACTS §112.4, each over the one before: the plugin's default
// headers, then operator (the operator's layers), then its authoritative
// headers, where an empty value removes the header so that it is not sent
// at all (§112.17). Names owned reports are left to the transport (nil:
// none). The transport's own headers, Host among them, never get here: the
// runtime refuses a relay naming them, so a request always goes to, and
// names, the host of its checked URL.
func layerHeaders(h http.Header, req *pluginv1.UpstreamRequest, operator func(http.Header), owned func(name string) bool) {
	for k, v := range req.GetDefaultHeaders() {
		if owned == nil || !owned(k) {
			h.Set(k, v)
		}
	}
	operator(h)
	for k, v := range req.GetHeaders() {
		switch {
		case owned != nil && owned(k):
		case v == "":
			h.Del(k)
		default:
			h.Set(k, v)
		}
	}
}

// passthroughHeaders fills in the client headers the account's passthrough
// (CONTRACTS §81), the passthrough keys of its header override (§108) and
// the matched sticky rule (§5.6; the built-in Claude Code rule forwards the
// session and sub-agent headers) forward, where h has none yet.
func (c *call) passthroughHeaders(h http.Header, acc *core.Account) {
	if c.c == nil || c.c.Request == nil {
		return
	}
	var rules [][]string
	if acc != nil {
		rules = append(rules, acc.HeaderPassthrough, acc.HeaderOverride.Passthrough())
	}
	if c.sticky != nil {
		rules = append(rules, c.sticky.rule.PassHeaders)
	}
	for _, r := range rules {
		for k, vs := range core.PassthroughHeaders(r, c.c.Request.Header) {
			if _, set := h[k]; !set {
				h[k] = vs
			}
		}
	}
}

// upstreamRequest is the plugin's relay as it goes upstream, the body its
// bytes as they are. Headers in layers (CONTRACTS §86.5, layerHeaders):
// the plugin's default headers; the client headers passed through, which
// only fill what is unset; the account's header override (§108) and
// parameter override (§84), which may replace or remove; the plugin's
// authoritative headers, which always win (an empty one removes); then a
// Content-Type when none.
func (c *call) upstreamRequest(ctx context.Context, req *pluginv1.UpstreamRequest, target *url.URL, acc *core.Account) (*http.Request, error) {
	method := cmp.Or(strings.ToUpper(req.GetMethod()), http.MethodPost)
	var body io.Reader
	if method != http.MethodGet && method != http.MethodHead {
		body = bytes.NewReader(req.GetBody())
	}
	r, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	layerHeaders(r.Header, req, func(h http.Header) {
		c.passthroughHeaders(h, acc)
		c.paramOverride.headers.Apply(h)
	}, nil)
	if r.Header.Get("Content-Type") == "" && body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, nil
}

// send makes the request the plugin relayed for x: after Execute returned,
// or inside the plugin's call for a relay that waits (x.waited).
func (c *call) send(ctx context.Context, x *execution, req *pluginv1.UpstreamRequest) attemptResult {
	rt, acc := x.rt, x.acc
	if m := req.GetUpstreamModel(); m != "" {
		c.rec.UpstreamModel = m
	}
	target, refused := x.targetOf(ctx, req)
	if refused != nil {
		return attemptResult{kind: attemptFailover, err: refused}
	}
	client := x.egress.Client

	// The upstream request does not hang off ctx: when the client goes the
	// watcher cancels it - at once, or not at all when the answer is being
	// read to its end for billing, within the timeouts below
	// (disconnect.go). A relay that waits is kept alike; the plugin giving
	// up its call cuts the exchange as an upstream interruption, never as a
	// hang-up. Task submissions settle on their own and are never kept.
	// The cause names what cut the request (forward records it).
	uctx, ucancel := context.WithCancelCause(context.WithoutCancel(ctx))
	defer ucancel(nil)
	c.drain = c.watchClient(ctx, ucancel, !c.ep.TaskSubmit())
	defer c.drain.stop()
	if w := x.waited; w != nil {
		stop := context.AfterFunc(w.ctx, func() { ucancel(errExecutionAborted) })
		defer stop()
	}
	hreq, err := c.upstreamRequest(uctx, req, target, acc)
	if err != nil {
		return attemptResult{kind: attemptFailover, err: fromCore(core.ErrInternal.WithCause(err), errTypeInternal)}
	}
	if c.ep.TaskSubmit() {
		if err := c.beginTask(ctx); err != nil {
			return attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("task registration unavailable").WithCause(err), errTypeInternal)}
		}
	}

	forced := c.forcesStream(rt)
	headerWait, total := c.upstreamTimeouts(c.stream || forced)
	// The relay timeout covers the upstream exchange, not the local
	// preparation above, so it starts with the header timer.
	if total > 0 {
		relayTimer := time.AfterFunc(total, func() { ucancel(errRelayTimeout) })
		defer relayTimer.Stop()
	}
	timer := time.AfterFunc(headerWait, func() { ucancel(errHeaderTimeout) })
	var requestWritten atomic.Bool
	if c.failurePolicy != nil {
		hreq = hreq.WithContext(httptrace.WithClientTrace(hreq.Context(), &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { requestWritten.Store(true) }}))
	}
	resp, err := execfetch.NoRedirects(client).Do(hreq)
	timer.Stop()
	if err != nil {
		if c.failurePolicy != nil {
			c.failureState.unsafeTransport = requestWritten.Load()
		}
		if c.ep.TaskSubmit() {
			return c.taskFailure(ctx, "task submission outcome is uncertain", err)
		}
		if ctx.Err() != nil {
			return attemptResult{kind: attemptCanceled, err: canceledErr()}
		}
		msg := err.Error()
		if cause := context.Cause(uctx); errors.Is(cause, errRelayTimeout) || errors.Is(cause, errHeaderTimeout) || errors.Is(cause, errExecutionAborted) {
			msg = cause.Error()
		}
		return c.classifyUpstream(ctx, x, 0, nil, nil, msg)
	}
	if c.stream || forced {
		resp.Body = newIdleBody(resp.Body, c.streamIdle(), ucancel)
	}
	defer resp.Body.Close()
	if c.g.d.Observations != nil {
		rec := *c.rec
		rec.StatusCode = resp.StatusCode
		c.observeRequest(observation.ResponseStarted, &rec)
	}
	c.g.observeQuota(rt, acc.ID, resp.StatusCode, resp.Header)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, readErr := c.readUpstreamError(resp.Body)
		if readErr != nil {
			if ctx.Err() != nil {
				return attemptResult{kind: attemptCanceled, err: canceledErr()}
			}
			return attemptResult{kind: attemptReturn, err: &gwError{Status: http.StatusBadGateway, Code: "upstream_response_incomplete",
				RecordType: errTypeUpstream, Message: "Could not read the complete upstream error response"}}
		}
		return c.upstreamFailure(ctx, x, resp.StatusCode, resp.Header, body)
	}
	adaptResponseStream(resp, req.GetResponseStream())
	if c.stream || forced {
		sniffEventStream(resp)
	}
	if forced {
		if res, failed := c.collectStreamOnly(ctx, uctx, x, resp); failed {
			return res
		}
	}
	if w := x.waited; w != nil {
		return w.answer(ctx, resp)
	}
	return c.forward(ctx, rt, x.acct, resp, x.body, nil)
}

// classifyUpstream decides what an upstream failure of x means: the
// plugin's ClassifyError now, or for a relay that waits the plugin's
// classification in Execute's return value (waitedRelay.fail).
func (c *call) classifyUpstream(ctx context.Context, x *execution, status int, header http.Header, body []byte, transportErr string) attemptResult {
	if w := x.waited; w != nil {
		return w.fail(status, header, body, transportErr)
	}
	return c.classify(ctx, x.rt, x.acct, status, header, body, transportErr)
}

// upstreamFailure classifies an upstream error answer - an HTTP error status,
// or the error event of a stream-only upstream (CONTRACTS §83) - and keeps
// its body for the client.
func (c *call) upstreamFailure(ctx context.Context, x *execution, status int, header http.Header, body *upstreamErrorBody) attemptResult {
	raw := body.prefix
	ct := header.Get("Content-Type")
	if status >= 500 {
		// A server error names no cause in the usage log; who answered
		// (a proxy on the way or the upstream itself) is in these.
		slog.WarnContext(ctx, "gateway: upstream server error", "request_id", c.rid, "plugin", x.rt.binding.Plugin.Key,
			"account", x.acc.ID, "status", status, "content_type", ct, "server", header.Get("Server"),
			"upstream_request_id", header.Get("Request-Id"), "body", truncateUTF8(string(raw), 300))
	}
	if w := x.waited; w != nil {
		w.errorBody = body
	}
	res := c.classifyUpstream(ctx, x, status, header, raw, "")
	if c.ep.TaskSubmit() {
		c.recordTaskFailure(ctx, status, raw, "upstream rejected task submission")
		res.kind = attemptReturn
	}
	c.preserveHTTPError(res.err, status, header, body)
	return res
}

// observeQuota hands the response headers to the quota observer when the
// account type declares quota headers (CONTRACTS §44). The observer reads a
// few headers and queues the write; types without the declaration (API keys)
// cost one nil check.
func (g *Gateway) observeQuota(rt *typeRoute, accountID int64, status int, h http.Header) {
	if g.d.Quota == nil || rt == nil {
		return
	}
	if q := rt.binding.Type.Quota; q != nil && len(q.Headers) > 0 {
		g.d.Quota.ObserveQuotaHeaders(accountID, q.Headers, status, h)
	}
}

func canceledErr() *gwError {
	return &gwError{Status: statusClientClosed, Message: "client canceled", RecordType: errTypeClientCanceled}
}

const (
	maxErrorBody    = 64 << 10
	defaultCooldown = 60 * time.Second
)

// classify asks the account type's plugin what an upstream failure means
// and applies the answer. Under a failure policy a transport error (no
// status) is the policy's alone: there is no answer for the plugin to read.
func (c *call) classify(ctx context.Context, rt *typeRoute, acct *pluginv1.Account,
	status int, header http.Header, body []byte, transportErr string) attemptResult {
	var cls *pluginv1.ClassifyErrorResponse
	if status > 0 || c.failurePolicy == nil {
		cls = c.classifyWithPlugin(ctx, rt, acct, status, header, body, transportErr)
	}
	return c.applyClassification(ctx, rt, acct, status, header, body, transportErr, cls)
}

// classifyWithPlugin asks the account type's plugin what an upstream failure
// means; defaults when the call fails.
func (c *call) classifyWithPlugin(ctx context.Context, rt *typeRoute, acct *pluginv1.Account,
	status int, header http.Header, body []byte, transportErr string) *pluginv1.ClassifyErrorResponse {
	headers := execfetch.FirstHeaderValues(header)
	prefix := core.ClassifyPrefix(body)
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.gw.platformTimeout())
	cls, err := rt.binding.Client.ClassifyError(cctx, &pluginv1.ClassifyErrorRequest{
		Meta: c.metaFor(rt), Account: acct, Status: int32(status), Headers: headers, BodyPrefix: prefix, TransportError: transportErr,
	})
	cancel()
	if err != nil || cls == nil {
		slog.WarnContext(ctx, "gateway: classify error failed, using defaults", "plugin", rt.binding.Plugin.Key, "err", err)
		cls = defaultClassification(status)
	}
	return cls
}

// applyClassification turns the plugin's classification of an upstream
// failure into the attempt's result: under a failure policy the policy
// decides retries and the reply, and the plugin still tells what the answer
// means for the account (CONTRACTS §69); otherwise the plugin decides.
func (c *call) applyClassification(ctx context.Context, rt *typeRoute, acct *pluginv1.Account, status int, header http.Header,
	body []byte, transportErr string, cls *pluginv1.ClassifyErrorResponse) attemptResult {
	if c.failurePolicy != nil && !c.tokenCounting() {
		res := c.classifyFailure(ctx, acct.GetId(), status, header, body, transportErr, rt.conv != nil)
		if res.err != nil {
			res.err.LocalFailure = cls.GetLocalFailure()
		}
		if status > 0 {
			c.applyPluginAccountState(ctx, acct.GetId(), cls)
		}
		return res
	}
	reason, failover := cls.GetReason(), false
	if !c.tokenCounting() {
		c.applyQuotaObservation(ctx, acct.GetId(), cls.GetQuota())
		reason, failover = c.applyAccountEffect(ctx, acct, status, body, transportErr, cls)
	}

	clientStatus := int(cls.GetClientStatus())
	if clientStatus == 0 {
		clientStatus = status
	}
	if clientStatus < 400 || clientStatus > 599 {
		clientStatus = http.StatusBadGateway
	}
	e := &gwError{Status: clientStatus, Type: cls.GetClientErrorType(), Message: cls.GetClientMessage(), LocalFailure: cls.GetLocalFailure(),
		RecordType: errTypeUpstream, Code: clientErrorCode(ctx, cls.GetClientErrorCode(), rt.binding.Plugin.Key)}
	if rt.conv != nil {
		// The upstream speaks another protocol: its error body and error
		// types do not fit the endpoint's errorFormat, which always wins.
		e.Type = ""
	} else if e.Type == "" && e.Message == "" && e.Code == codeUpstreamError && len(body) > 0 {
		// Nothing the plugin said would survive rendering, so the upstream
		// body is returned verbatim. A plugin that did name a code is asking
		// for the rendered envelope instead - passing the body through would
		// throw that code away, which is the very thing client_error_code
		// exists to stop. (A rejected code leaves e.Code generic and the old
		// passthrough applies, as before this field existed.)
		e.Raw = body
	}
	if e.Message == "" {
		if transportErr != "" {
			e.Message = "upstream request failed"
		} else {
			e.Message = "upstream returned HTTP " + itoa(int64(status))
		}
	}
	c.rec.ErrorMessage = truncateUTF8(firstNonEmpty(reason, transportErr, e.Message), 1000)
	if !failover && cls.GetAction() == pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT {
		return attemptResult{kind: attemptReturn, err: e}
	}
	return attemptResult{kind: attemptFailover, err: e}
}

// applyAccountEffect cools down or disables the account as the plugin and the
// administrator's auto-disable rules ask (CONTRACTS §42.1). It returns the
// reason recorded for the attempt and whether the attempt must fail over
// regardless of the plugin's action (an administrator rule disabled the
// account).
func (c *call) applyAccountEffect(ctx context.Context, acct *pluginv1.Account, status int, body []byte,
	transportErr string, cls *pluginv1.ClassifyErrorResponse) (reason string, failover bool) {
	ctx = context.WithoutCancel(ctx)
	effect, reason := cls.GetAccountEffect(), cls.GetReason()
	automatic := cls.GetAutomaticState()
	if automatic != nil && automatic.GetAction() != pluginv1.AccountAutomaticState_ACTION_DISABLE {
		automatic = nil
	}
	if automatic != nil {
		effect, reason = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, automatic.GetReason()
		// A disable with a release time is applied as such; without one, or
		// when the account may not be disabled, it stays a cooldown.
		if c.pluginAutoDisable(ctx, acct.GetId(), automatic) {
			effect = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED
		}
	}
	if effect == pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE {
		effect = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
	}
	if status > 0 && !cls.GetLocalFailure() {
		if rule := c.autoDisable.rule(status, body, transportErr); rule != "" {
			effect, reason, failover = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE, rule, true
		}
	}
	switch effect {
	case pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN:
		c.cooldown(ctx, acct.Id, time.Unix(cls.GetCooldownUntilUnix(), 0), reason)
	case pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE:
		if !c.autoDisableAccount(ctx, acct.Id, reason) {
			c.cooldown(ctx, acct.Id, time.Time{}, "auto-disable off, cooling down: "+reason)
		}
	}
	return reason, failover
}

// pluginAutoDisable applies a plugin's temporary disable (automatic_state
// DISABLE with a release time, e.g. a 429 until the quota resets):
// the account is auto_disabled until then, so neither another attempt of this
// request nor a sticky session picks it, and the core's sweep makes it active
// again at the release time (CONTRACTS §69). A disable without a release time
// or with one already past is not applied here. It reports whether the
// account is disabled now.
func (c *call) pluginAutoDisable(ctx context.Context, id int64, st *pluginv1.AccountAutomaticState) bool {
	if st == nil || st.GetAction() != pluginv1.AccountAutomaticState_ACTION_DISABLE {
		return false
	}
	now := c.g.now()
	var release time.Time
	switch r := st.GetRelease().(type) {
	case *pluginv1.AccountAutomaticState_UntilUnix:
		release = time.Unix(r.UntilUnix, 0)
	case *pluginv1.AccountAutomaticState_DurationSeconds:
		release = now.Add(time.Duration(r.DurationSeconds) * time.Second)
	}
	if !release.After(now) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	reason := st.GetReason()
	if reason == "" {
		reason = "disabled by the account plugin until " + release.UTC().Format(time.RFC3339)
	}
	disabled, err := c.g.d.Accounts.AutoDisableUntil(ctx, id, reason, release)
	if err != nil {
		slog.WarnContext(ctx, "gateway: disable account until release", "account", id, "err", err)
		return false
	}
	if s := c.sticky; disabled && s != nil && s.bound == id {
		if c.failurePolicy != nil {
			if !s.keep {
				c.failureState.stickyErr = c.releaseBinding(ctx, id)
			}
		} else {
			c.dropBinding(ctx, s)
		}
	}
	return disabled
}

// applyPluginAccountState is the part of a plugin's classification a
// published failure policy still takes (CONTRACTS §69): the quota the answer
// names and a temporary disable. Retries, cooldowns and the client reply stay
// the policy's.
func (c *call) applyPluginAccountState(ctx context.Context, id int64, cls *pluginv1.ClassifyErrorResponse) {
	c.applyQuotaObservation(ctx, id, cls.GetQuota())
	s := &c.failureState
	if s.blocked {
		return
	}
	if c.pluginAutoDisable(ctx, id, cls.GetAutomaticState()) {
		s.blocked, s.accountResult = true, "disabled_by_plugin"
		if n := len(s.events); n > 0 && s.events[n-1].AccountID == id {
			s.events[n-1].AccountResult = s.accountResult
		}
	}
}

// autoDisableAccount disables the account when both the global switch and
// the account allow it, dropping a sticky binding to it. It reports whether
// the account is disabled.
func (c *call) autoDisableAccount(ctx context.Context, id int64, reason string) bool {
	if !c.autoDisable.Enabled {
		return false
	}
	disabled, err := c.g.d.Accounts.AutoDisable(ctx, id, reason)
	if err != nil {
		slog.WarnContext(ctx, "gateway: disable account", "account", id, "err", err)
		return false
	}
	if s := c.sticky; disabled && s != nil && s.bound == id {
		c.dropBinding(ctx, s)
	}
	return disabled
}

// cooldown excludes the account until the given time, or for defaultCooldown
// when it is not in the future.
func (c *call) cooldown(ctx context.Context, id int64, until time.Time, reason string) {
	if !until.After(c.g.now()) {
		until = c.g.now().Add(defaultCooldown)
	}
	if err := c.g.d.Accounts.SetCooldown(ctx, id, until, reason); err != nil {
		slog.WarnContext(ctx, "gateway: set cooldown", "account", id, "err", err)
	}
}

// defaultClassification is used when ClassifyError itself fails.
func defaultClassification(status int) *pluginv1.ClassifyErrorResponse {
	r := &pluginv1.ClassifyErrorResponse{}
	if status == 0 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500 {
		r.Action = pluginv1.ClassifyErrorResponse_ACTION_FAILOVER
	} else {
		r.Action = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT
	}
	return r
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
