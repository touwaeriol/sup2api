package gateway

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fail renders err (if nothing was written yet) and records it.
func (c *call) fail(e *gwError) {
	if c.rec != nil {
		c.rec.Success = false
		c.rec.StatusCode = clientError(c.format, e).Status
		if e.RecordType != "" {
			c.rec.ErrorType = e.RecordType
		} else if c.rec.ErrorType == "" {
			c.rec.ErrorType = errTypeInternal
		}
		if c.rec.ErrorMessage == "" {
			c.rec.ErrorMessage = truncateUTF8(e.Message, 1000)
		}
	}
	if e.Status == statusClientClosed || c.c.Writer.Written() {
		return
	}
	writeError(c.c, c.format, e)
}

// submit finalizes the usage record and hands it to the settler. A request
// whose usage the records of a waited relay own (executionOwnsUsage) has
// none to submit.
func (c *call) submit() {
	rec := c.rec
	if rec == nil || c.g.d.Settler == nil {
		return
	}
	rec.Conversion = c.conversionTrace()
	c.concludeScheduling(c.c.Request.Context())
	rec.Scheduling = c.schedulingTrace()
	rec.AttemptTrail = c.attemptTrail()
	c.statsCompletedAt = c.g.now()
	// A client that left once it was served did not cancel anything.
	c.statsCanceled = c.c.Request.Context().Err() != nil && !rec.Success
	c.observeRequest(observation.Completed, rec)
	if c.executionOwnsUsage {
		c.observeRequest(observation.UsageFinalized, rec)
		return
	}
	rec.LatencyMs = int(c.g.now().Sub(c.start) / time.Millisecond)
	if rec.StatusCode == 0 {
		rec.StatusCode = c.c.Writer.Status()
	}
	c.finishSubmit(context.Background(), rec, c.ep.Billing)
}

func (c *call) finishSubmit(ctx context.Context, rec *core.UsageRecord, billing string) {
	c.finalizeBillability(ctx, rec, billing)
	c.observeRequest(observation.UsageFinalized, rec)
	c.g.d.Settler.Submit(rec)
}

func (c *call) finalizeBillability(ctx context.Context, rec *core.UsageRecord, billing string) {
	// completed: the upstream did the work this record is priced for - it
	// answered, or the client left while it was answering and still owes for
	// it (a per-request price is due in both cases).
	owed, waived := c.settleCanceled(ctx, rec, billing)
	hasUsage, priced := usageOf(rec, rec.Success || owed)
	rec.Billable = !strings.EqualFold(billing, "free") && !waived && priced
	if !rec.Billable {
		c.dropReservation(ctx, rec, billing, hasUsage)
		rec.Price = nil
	}
}

// usageOf says whether rec holds usage, and whether it holds a price that is
// due: one for that usage, or a per-request one (core.PriceRule.PerRequest)
// when the upstream completed the work.
func usageOf(rec *core.UsageRecord, completed bool) (hasUsage, priced bool) {
	hasUsage = rec.Tokens != (core.UsageTokens{}) || len(rec.Metrics) > 0
	priced = rec.Price != nil && (hasUsage || (completed && rec.Price.PerRequest))
	for _, item := range append(append([]core.PricedUsage(nil), rec.Additional...), rec.Replacement...) {
		itemUsage := item.Tokens != (core.UsageTokens{}) || len(item.Metrics) > 0
		hasUsage = hasUsage || itemUsage
		priced = priced || (item.Price != nil && (itemUsage || (completed && item.Price.PerRequest)))
	}
	return hasUsage, priced
}

// errReadingEmpty is why a request is unreported whose usage is the host's
// reading - the core's own, or the one a plugin chose (hostUsageChosen) -
// when that reading holds nothing to bill.
var errReadingEmpty = errors.New("the upstream answered and the host's reading of it holds no usage")

// readingEmpty is errReadingEmpty when the request was served and the host's
// reading of the answer found nothing to bill: there are no facts and the
// core makes none up, so the missing usage shows instead of passing as a
// free request. A free endpoint, a task submission (its usage comes when
// the task ends) and a per-request price have something else to say about
// the money.
func (c *call) readingEmpty() error {
	r := c.rec
	if !r.Success || strings.EqualFold(c.ep.Billing, "free") || c.ep.TaskSubmit() {
		return nil
	}
	if hasUsage, priced := usageOf(r, true); hasUsage || priced {
		return nil
	}
	return errReadingEmpty
}

// endUnreported ends as unreported, when why says so, a request served by
// account whose answer the host read: the client keeps what it was sent and
// the record is the free usage_unreported one (submit releases the
// precharge), logged at ERROR as ExecutionRecords.CloseExecutionUnreported
// does for a relay that waited. It runs when the attempt ends, before the
// account is credited with the request: an unreported request neither
// renews its sticky binding nor touches the account's last use.
func (c *call) endUnreported(ctx context.Context, account int64, why error) {
	if why == nil {
		return
	}
	c.rec.SetUnreported(why.Error())
	slog.ErrorContext(ctx, "gateway: request unreported, not billed", "request_id", c.rid, "plugin", c.rec.PluginKey,
		"account_id", account, "account_type", c.rec.AccountType, "endpoint", c.ep.Path, "protocol", c.rec.UpstreamProtocol, "reason", why.Error())
}

// settleCanceled decides what a request pays whose client hung up while the
// upstream was answering, by the client disconnect policy (CONTRACTS
// §25.8). owed says the upstream's work is to be paid for, which is what
// makes a per-request price due; waived says the policy lets this record go
// free whatever it holds.
//
// An answer read to its end already holds the real usage and is billed as it
// stands. Any other one that is not waived pays the estimate it was
// pre-charged for instead of the nothing a cut-off stream reports: the usage
// event comes last in every streaming protocol, so hanging up just before it
// would otherwise release the hold and give the output away.
//
// Not applied when the client left before any upstream answer (nothing was
// consumed), when the endpoint bills nothing, or when a plugin accounted for
// the work itself (Replacement attempts, a Reservation).
func (c *call) settleCanceled(ctx context.Context, rec *core.UsageRecord, billing string) (owed, waived bool) {
	if rec.ErrorType != errTypeClientCanceled || !c.upstreamResponded {
		return false, false
	}
	if strings.EqualFold(billing, "free") || rec.Reservation != nil || len(rec.Replacement) > 0 {
		return false, false
	}
	if c.drained {
		return true, false
	}
	if c.disconnectPolicy(ctx) == core.DisconnectFree {
		return false, true
	}
	est := c.precharged
	if est == nil {
		return false, false
	}
	rec.Tokens = canceledTokens(rec.Tokens, est.tokens)
	if len(rec.Additional) == 0 && rec.SegmentsDropped == "" {
		// Segments that could not be read stay dropped (CONTRACTS §120.4).
		rec.Additional = est.additional
	}
	if len(rec.Metrics) == 0 && len(est.metrics) > 0 {
		rec.Metrics = est.metrics
	}
	if rec.UsageSemantics == "" {
		rec.UsageSemantics = est.semantics
	}
	rec.UsageExtract = core.UsageExtractPrecharge
	return true, false
}

// canceledTokens is the usage of a request cut off mid-answer: what the
// answer had reported, completed from the pre-charge estimate.
//
// The prompt side (input and the three cache counts) is taken from one source
// only. The estimate counts the whole prompt as input, while a response
// counts the cached part of the same prompt under the cache fields, so mixing
// the two per field bills the prompt twice wherever input excludes cache. A
// response that reported any of the four reported all of them (they arrive
// together, in the first event of the protocols that send them early), so it
// wins; the estimate stands only when the response had said nothing.
//
// Output is the larger of the two: the response's count is whatever had been
// produced when the client left, the estimate is the reserve.
func canceledTokens(reported, est core.UsageTokens) core.UsageTokens {
	out := reported
	if reported.Input == 0 && reported.CacheRead == 0 && reported.CacheCreation == 0 && reported.CacheCreation1h == 0 {
		out = est
	}
	out.Output = max(reported.Output, est.Output)
	return out
}

// dropReservation clears a Reservation from a record that turned out not to
// be billable, and says so - in the log, naming the plugin and the endpoint,
// and on the record, under usage_logs.anomalies.
//
// The settler would have dropped it anyway (initialStatus reads Billable
// first), silently: no pre-charge, no pending_settlements entry, no reconcile,
// for a job the upstream really started - the shape CONTRACTS §25.5 found in
// the first plugin to reserve, where the submit endpoint had been marked
// billing "free" because "the submit itself has no final usage". The manifest
// check now refuses that combination; this is what stands for packages
// installed before it did, and for the other way here: an estimate with no
// tokens and no facts on a per-token price, which is a plugin bug the plugin
// author has to hear about.
func (c *call) dropReservation(ctx context.Context, rec *core.UsageRecord, billing string, hasUsage bool) {
	rv := rec.Reservation
	if rv == nil {
		return
	}
	var why string
	switch {
	case strings.EqualFold(billing, "free"):
		why = "endpoint billing is \"free\""
	case !hasUsage:
		why = "the estimate has no tokens and no facts, and the price is not per-request"
	default:
		why = "record is not billable"
	}
	rec.Reservation = nil
	rec.ReservationDropped = why
	slog.WarnContext(ctx, "gateway: plugin reserved a pre-charge the request cannot carry; the reservation is DROPPED - "+
		"nothing is charged now and nothing will be reconciled",
		"request_id", c.rid, "plugin", rv.PluginKey, "platform", c.platform, "endpoint", c.ep.Path,
		"protocol", c.ep.Protocol, "ref_id", rv.RefID, "billing", billing, "reason", why)
}

// countTokens adds this request's tokens to the rate-limit windows of the
// account and of the API key.
func (c *call) countTokens(ctx context.Context, accountID int64) {
	n := c.tokensToCount()
	ctx = context.WithoutCancel(ctx)
	if lim := c.g.d.Limiter; lim != nil && accountID != 0 {
		if h := c.accountHold; h != nil && h.Subject == accountID {
			c.accountHold = nil
			lim.Settle(ctx, *h, n)
		} else if n > 0 {
			lim.AddTokens(ctx, accountID, "", n)
		}
		lim.CountModelTokens(ctx, accountID, c.model, n)
	}
	if w := c.g.d.KeyWindows; w != nil && c.principal != nil {
		if h := c.keyHold; h != nil {
			c.keyHold = nil
			w.Settle(ctx, *h, n)
		} else if n > 0 {
			w.AddTokens(ctx, c.principal.KeyID, n)
		}
	}
}

// tokensToCount is what the request counts against the token windows: its
// usage with its replacement and additional items.
func (c *call) tokensToCount() int64 {
	n := c.rec.Tokens.Throughput(c.rec.UsageSemantics)
	if len(c.rec.Replacement) > 0 {
		n = 0
		for _, item := range c.rec.Replacement {
			n += item.Tokens.Throughput(item.UsageSemantics)
		}
	}
	for _, item := range c.rec.Additional {
		n += item.Tokens.Throughput(item.UsageSemantics)
	}
	// A stream cut before its usage event: count the prompt, so that hanging
	// up does not escape the caps that made this estimate necessary. A request
	// that succeeded without usage (count_tokens) used nothing to count.
	if n <= 0 && !c.rec.Success {
		n = c.rateEstimate.prompt
	}
	return n
}

// releaseAccountHold gives back what the account's hold still reserves: its
// attempt ended with no tokens to count. The request stays counted in RPM.
func (c *call) releaseAccountHold(ctx context.Context) {
	if h := c.accountHold; h != nil {
		c.accountHold = nil
		if lim := c.g.d.Limiter; lim != nil {
			lim.Settle(context.WithoutCancel(ctx), *h, 0)
		}
	}
}

// releaseHolds gives back what the request still reserves.
func (c *call) releaseHolds(ctx context.Context) {
	c.releaseAccountHold(ctx)
	if h := c.keyHold; h != nil {
		c.keyHold = nil
		if w := c.g.d.KeyWindows; w != nil {
			w.Settle(context.WithoutCancel(ctx), *h, 0)
		}
	}
}
