package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/background"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// The reconcile loop closes pre-charged usage rows (CONTRACTS §25.4).
//
// Plugins supply upstream facts in the result of Monitor, the only
// observation call. The host owns account admission, network policy,
// observation fencing, pricing and all financial transactions.
// core.usage.reconcile runs the sweep on one node at a time, PG claims fence
// per-task outcomes, and the shared background executor bounds work and
// shutdown.
// Giving up keeps the estimate and must never requeue a reserved debit as
// ordinary failed billing; see abandon().
const (
	// reconcileInterval is the period of core.usage.reconcile.
	reconcileInterval = 10 * time.Second
	// reconcileBatch is how many entries one sweep claims.
	reconcileBatch = 100
	// reconcileWorkers process the claimed entries in parallel.
	reconcileWorkers = 4
	// reconcileLease hides a claimed entry from other sweeps while it is
	// being worked on. It is longer than the per-entry budget so a live node
	// always finishes before the lease lapses, and short enough that a dead
	// one is forgiven quickly.
	reconcileLease = 3 * time.Minute
	// reconcileSweepBudget bounds one sweep. Whatever is not reached stays
	// due (its lease lapses) and is picked up next time, in order.
	reconcileSweepBudget = 2 * time.Minute
	// reconcileEntryBudget bounds one observation including network and accounting.
	reconcileEntryBudget = 30 * time.Second
	// reconcileHTTPTimeout bounds one fetch of a Monitor call.
	reconcileHTTPTimeout = 20 * time.Second
)

// ReconcileDeps are what the sweep needs beyond the settler itself. It does
// nothing until StartReconcile is called with all of them.
type ReconcileDeps struct {
	// Executor is shared with plugin jobs; the reconcile group keeps its own cap.
	Executor *background.Executor
	// Registry resolves the plugin named on an entry to its PlatformService.
	Registry core.PluginRegistry
	// Accounts loads the account that served the original request, with its
	// credentials - the same account the plugin already saw when it built the
	// upstream request - and gives its way out (Egress): a reconcile leaves
	// the deployment through the account's proxy as it stands now.
	Accounts core.AccountDirectory
	Slots    core.Slots
	Limiter  core.AccountLimiter
	// AllowPrivateUpstream mirrors the gateway's setting for the SSRF guard.
	AllowPrivateUpstream bool
	NodeID               string
	Logger               *slog.Logger
}

type reconciler struct {
	ReconcileDeps
	log   *slog.Logger
	group *background.Group
}

// StartReconcile prepares the reconcile sweep (core.usage.reconcile,
// ReconcileTaskSpec) until Stop. It is a no-op when a dependency is missing:
// a deployment without a plugin registry has no pre-charged rows to close
// either.
func (s *Service) StartReconcile(ctx context.Context, d ReconcileDeps) {
	if d.Registry == nil || d.Accounts == nil {
		return
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Executor == nil {
		d.Executor = background.New(8)
	}
	s.mu.Lock()
	if s.rec != nil || s.stopping {
		s.mu.Unlock()
		return
	}
	s.rec = &reconciler{ReconcileDeps: d, log: d.Logger.With("component", "reconcile", "node", d.NodeID)}
	ctx, s.recStop = context.WithCancel(context.WithoutCancel(ctx))
	s.rec.group = d.Executor.Group(ctx, reconcileWorkers, reconcileBatch*2)
	s.mu.Unlock()
}

// ReconcileTask is the cluster task that runs the reconcile sweep.
const ReconcileTask = "core.usage.reconcile"

// ReconcileTaskSpec is core.usage.reconcile: one sweep every
// reconcileInterval on one node. A run returns once every entry it claimed
// was checked, so its count and duration are the sweep's.
func (s *Service) ReconcileTaskSpec() core.TaskSpec {
	return core.TaskSpec{
		Name:  ReconcileTask,
		Title: core.LocalizedText{"en": "Reconcile pre-charged usage", "zh": "对账预扣用量"},
		Description: core.LocalizedText{
			"en": "Asks the plugins for the final usage of pre-charged requests that are due and settles them; an entry nobody can confirm by its deadline keeps its estimate.",
			"zh": "向插件查询到期的预扣请求的最终用量并结算；到截止时间仍无法确认的保留预估金额。",
		},
		Scope:   core.TaskCluster,
		Every:   reconcileInterval,
		Timeout: reconcileSweepBudget + reconcileEntryBudget,
		Handler: func(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
			return core.TaskResult{Count: s.ReconcileDue(ctx)}, ctx.Err()
		},
	}
}

// ---------------------------------------------------------------- the sweep

// settleEntry is one claimed row of pending_settlements.
type settleEntry struct {
	id           int64
	pluginKey    string
	refID        string
	usageLogID   int64
	accountID    *int64
	attempts     int
	pollFailures int
	pollObserved bool
	pollFailed   bool
	createdAt    time.Time
	deadlineAt   time.Time
	task         *taskClaim
	account      core.AccountRef // the fixed account used by this observation
	monitor      *monitorOutcome // the claim of the Monitor result being applied
}

// ReconcileDue runs one sweep: claim the due entries and work through them,
// returning once every claimed entry was checked. It returns how many
// entries it claimed. The task framework runs it on one node at a time; a
// sweep that runs anyway beside another (a node of the previous release
// during an upgrade) only takes entries the other did not lease.
func (s *Service) ReconcileDue(ctx context.Context) int {
	r := s.rec
	if r == nil || !r.Executor.Allowed() {
		return 0
	}
	var claimed int
	_ = background.Scope(ctx, background.Work{Timeout: reconcileSweepBudget}, func(ctx context.Context) error {
		claimed = s.reconcileSweep(ctx)
		return nil
	})
	return claimed
}

func (s *Service) reconcileSweep(ctx context.Context) int {
	r := s.rec
	if !r.Executor.Allowed() {
		return 0
	}
	entries, err := s.claimDue(ctx, reconcileBatch)
	if err != nil {
		r.log.Error("reconcile: claim due entries", "err", err)
		return 0
	}
	s.cleanTaskReceipts(ctx)
	tasks, err := s.claimTasks(ctx, reconcileBatch)
	if err != nil {
		r.log.Error("reconcile: claim tasks", "err", err)
	} else {
		entries = append(entries, tasks...)
	}
	if len(entries) == 0 {
		return 0
	}
	var abandoned atomic.Int64
	ordered := interleaveByPlugin(entries)
	_ = r.group.Each(ctx, background.Work{}, len(ordered), func(ctx context.Context, index int) {
		if s.reconcileOne(ctx, ordered[index]) {
			abandoned.Add(1)
		}
	})
	// Abandoning is the outcome an operator has to know about: money was
	// kept on an estimate nobody could confirm.
	if n := abandoned.Load(); n > 0 {
		r.log.Error("reconcile: gave up on pre-charged entries; the estimate stands as the final charge",
			"entries", n, "of", len(entries))
	}
	return len(entries)
}

// claimDue takes the next due entries AND leases them in one statement: their
// next_check_at moves forward before any plugin is called, so a node that
// dies halfway through a sweep loses a lease rather than wedging an entry,
// and a second node cannot pick up what this one is already holding.
func (s *Service) claimDue(ctx context.Context, limit int) ([]*settleEntry, error) {
	g := store.G(ctx, s.db.Gorm)
	due := g.Model(&model.PendingSettlement{}).Select("id").
		Where("state = 'pending' AND next_check_at <= now() AND task_public_id IS NULL").
		Order("next_check_at").Limit(limit).Clauses(store.ForUpdate().SkipLocked())
	var rows []model.PendingSettlement
	err := g.Model(&rows).Clauses(claimedEntry).Where("id IN (?)", due).
		Updates(map[string]any{"next_check_at": gorm.Expr("now() + make_interval(secs => ?)", reconcileLease.Seconds())}).Error
	if err != nil {
		return nil, err
	}
	out := make([]*settleEntry, len(rows))
	for i, r := range rows {
		out[i] = &settleEntry{id: r.ID, pluginKey: r.PluginKey, refID: r.RefID, usageLogID: r.UsageLogID, accountID: r.AccountID,
			attempts: r.Attempts, pollFailures: r.PollFailures, createdAt: r.CreatedAt, deadlineAt: r.DeadlineAt}
	}
	return out, nil
}

// interleaveByPlugin round-robins the entries across their plugins. With
// four workers and a hundred entries of one slow plugin followed by one entry
// of a fast one, a plain ordering would leave the fast one waiting behind the
// whole slow batch; interleaved, it is reached in the first few rounds. The
// slow plugin still pays for being slow - in its own entries' latency - which
// is the only place that cost belongs.
func interleaveByPlugin(entries []*settleEntry) []*settleEntry {
	byPlugin := map[string][]*settleEntry{}
	var order []string
	for _, e := range entries {
		if _, ok := byPlugin[e.pluginKey]; !ok {
			order = append(order, e.pluginKey)
		}
		byPlugin[e.pluginKey] = append(byPlugin[e.pluginKey], e)
	}
	if len(order) < 2 {
		return entries
	}
	out := make([]*settleEntry, 0, len(entries))
	for len(out) < len(entries) {
		for _, k := range order {
			if q := byPlugin[k]; len(q) > 0 {
				out = append(out, q[0])
				byPlugin[k] = q[1:]
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- one entry

// reservedRowState is the usage row an entry belongs to.
type reservedRowState struct {
	p         *pending
	status    string
	totalCost decimal.Decimal
}

// reconcileOne checks one entry and applies the outcome. It reports whether
// the entry was abandoned.
func (s *Service) reconcileOne(ctx context.Context, e *settleEntry) bool {
	var abandoned bool
	_ = background.Scope(ctx, background.Work{Timeout: reconcileEntryBudget}, func(ctx context.Context) error {
		abandoned = s.reconcileOneAdmitted(ctx, e)
		return nil
	})
	return abandoned
}

func (s *Service) reconcileOneAdmitted(ctx context.Context, e *settleEntry) bool {
	if e.task != nil {
		return s.reconcileTask(ctx, e)
	}
	r := s.rec

	row, err := s.loadReserved(ctx, e.usageLogID)
	if err != nil {
		r.log.Error("reconcile: load usage row", "entry", e.id, "usage_log_id", e.usageLogID, "err", err)
		return false
	}
	if row.status != StatusReserved {
		// A human already refunded or re-billed this row, or another node
		// finished it. Close the entry rather than fight over it.
		s.closeEntry(ctx, e, SettleStateSettled, "usage row left the reserved state")
		return false
	}

	cfg := s.reconcileSettings(ctx)
	now := time.Now()
	switch {
	case !now.Before(e.deadlineAt):
		s.abandon(ctx, e, row, fmt.Sprintf("deadline %s passed", e.deadlineAt.UTC().Format(time.RFC3339)))
		return true
	case e.attempts >= cfg.maxAttempts:
		s.abandon(ctx, e, row, fmt.Sprintf("gave up after %d checks", e.attempts))
		return true
	}

	s.observe(ctx, e, row)
	return false
}

// askPlugin asks the plugin's Monitor about e on its account, held for the
// call.
func (s *Service) askPlugin(ctx context.Context, e *settleEntry, row *reservedRowState) (*pluginv1.ReconcileResult, error) {
	gen := s.rec.Registry.Current()
	if gen == nil {
		return nil, errors.New("no plugin generation loaded")
	}
	client := platformClientOf(gen, e.pluginKey)
	if client == nil {
		return nil, fmt.Errorf("plugin %q has no platform service", e.pluginKey)
	}
	if e.task != nil {
		var err error
		if e.task.paths, err = taskPaths(gen, e); err != nil {
			return nil, err
		}
	}
	ctx, acct, release, err := s.acquireTaskAccount(ctx, e)
	if err != nil {
		return nil, err
	}
	defer release()
	entry := &pluginv1.ReconcileEntry{
		RefId: e.refID, RequestId: row.p.RequestID, Attempts: int32(e.attempts),
		CreatedAtUnix: e.createdAt.Unix(), DeadlineAtUnix: e.deadlineAt.Unix(),
		Model: row.p.Model, Protocol: row.p.Protocol,
	}
	if e.task != nil {
		entry.TaskKind = e.task.kind
	}
	return s.askMonitor(ctx, e, observationCall{client: client, entry: entry, acct: acct})
}

// observationCall is one observation sent to a plugin: the platform client,
// the entry describing the request, and the account the plugin may act for.
type observationCall struct {
	client core.PlatformPlugin
	entry  *pluginv1.ReconcileEntry
	acct   *pluginv1.Account
}

// platformClientOf finds the PlatformService of the plugin that declared the
// platform whose reported usage made the reservation.
func platformClientOf(gen core.Generation, pluginKey string) core.PlatformPlugin {
	for _, pb := range gen.Platforms() {
		if pb.Plugin.Key == pluginKey && pb.Client != nil {
			return pb.Client
		}
	}
	return nil
}

// ---------------------------------------------------------------- outcomes

// bookkeeping returns the context the outcome writes run under: a fresh one
// with its own short deadline, NOT the entry budget.
//
// The entry budget bounds the plugin calls and the upstream request, and a
// plugin that used all of it would otherwise take the bookkeeping down with
// it - losing an abandon (the entry sits leased and is reconsidered later,
// which is harmless) or, worse, losing the record of a settle whose ledger
// row the same transaction would have rolled back anyway. Writing the outcome
// is not the part that is allowed to be slow.
func bookkeeping(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

// reschedule counts the attempt and puts the entry back in the queue. want is
// the plugin's own suggestion, 0 when it made none.
func (s *Service) reschedule(ctx context.Context, e *settleEntry, cfg resolved, want time.Duration, lastErr string) {
	s.rescheduleAttempt(ctx, e, cfg, want, lastErr, true)
}

func (s *Service) rescheduleAttempt(ctx context.Context, e *settleEntry, cfg resolved, want time.Duration, lastErr string, count bool) {
	if e.task != nil {
		s.rescheduleTask(ctx, e, cfg, want, lastErr, count)
		return
	}
	ctx, cancel := bookkeeping(ctx)
	defer cancel()
	increment := 0
	if count {
		increment = 1
	}
	next := time.Now().Add(cfg.clampDelay(want, e.attempts+increment))
	if next.After(e.deadlineAt) {
		// One last attempt exactly at the deadline rather than one fewer.
		next = e.deadlineAt
	}
	err := s.outcomeTx(ctx, e, func(tx *gorm.DB) error {
		status, err := lockUsageRow(ctx, tx, e.usageLogID)
		if err != nil {
			return err
		}
		if status != StatusReserved {
			return errNotPending
		}
		res := entryRow(store.G(ctx, tx), e.id).Where("state = 'pending' AND task_public_id IS NULL").Updates(map[string]any{
			"attempts": gorm.Expr("attempts + ?", increment), "next_check_at": next, "last_error": trunc(lastErr, 2000),
			"poll_failures": pollFailures(e),
		})
		if res.Error == nil && res.RowsAffected != 1 {
			return errNotPending
		}
		return res.Error
	})
	if err != nil {
		s.rec.log.Error("reconcile: reschedule", "entry", e.id, "err", err)
		return
	}
	if lastErr != "" {
		s.rec.log.Warn("reconcile: check failed, will retry", "entry", e.id, "plugin", e.pluginKey,
			"ref_id", e.refID, "attempts", e.attempts+increment, "next_check_at", next, "err", lastErr)
	}
}

// closeEntry marks an entry done without touching money.
func (s *Service) closeEntry(ctx context.Context, e *settleEntry, state, note string) {
	ctx, cancel := bookkeeping(ctx)
	defer cancel()
	// An entry a task owns is the task's to close.
	if err := store.G(ctx, s.db.Gorm).Model(&model.PendingSettlement{}).Where("id = ? AND task_public_id IS NULL", e.id).
		Updates(map[string]any{"state": state, "last_error": trunc(note, 2000)}).Error; err != nil {
		s.rec.log.Error("reconcile: close entry", "entry", e.id, "err", err)
	}
}

// settleReconciled replaces the estimate with the real usage: the row is
// repriced, the difference is charged or refunded, and the row becomes an
// ordinary billed one.
func (s *Service) settleReconciled(ctx context.Context, e *settleEntry, row *reservedRowState, res *pluginv1.ReconcileResult) {
	p := row.p
	t := res.GetTokens()
	p.Tokens = usagerules.Tokens(t.GetInputTokens(), t.GetOutputTokens(), t.GetCacheReadTokens(),
		t.GetCacheCreationTokens(), t.GetCacheCreation_1HTokens())
	if m := s.reconciledFacts(ctx, e, p, res.GetFacts()); m != nil {
		p.Metrics = m
	}
	cost, detail, exprHash, err := s.priceOf(ctx, p)
	if err != nil {
		s.rec.log.Error("reconcile: price the real usage", "entry", e.id, "err", err)
		s.reschedule(ctx, e, s.reconcileSettings(ctx), 0, "pricing failed: "+err.Error())
		return
	}
	total := cost.total
	diff := total.Sub(row.totalCost)
	var ledgerRes *core.LedgerResult
	ctx, cancel := bookkeeping(ctx)
	defer cancel()
	err = s.outcomeTx(ctx, e, func(tx *gorm.DB) error {
		if err := s.lockTaskOutcome(ctx, tx, e, "closed"); err != nil {
			return err
		}
		status, err := lockUsageRow(ctx, tx, e.usageLogID)
		if err != nil {
			return err
		}
		if status != StatusReserved {
			return errNotPending
		}
		switch {
		case diff.Sign() > 0:
			// The estimate was short. The base amount already went through
			// "usage:{request_id}"; this is its own key so the two can never
			// cancel each other out.
			ledgerRes, err = s.ledger.ApplyTx(ctx, tx, core.LedgerChange{
				UserID: p.UserID, Amount: diff, Credit: false, Kind: "usage",
				RefType: "usage", RefID: p.RequestID, IdempotencyKey: "usage:" + p.RequestID + ":reconcile",
				Note: "reconciled: real usage exceeded the reservation",
			})
		case diff.Sign() < 0:
			ledgerRes, err = s.ledger.ApplyTx(ctx, tx, core.LedgerChange{
				UserID: p.UserID, Amount: diff.Neg(), Credit: true, Kind: "refund",
				RefType: "usage", RefID: p.RequestID, IdempotencyKey: "refund:" + p.RequestID + ":reconcile",
				Note: "reconciled: real usage was below the reservation",
			})
		}
		if err != nil {
			return fmt.Errorf("ledger: %w", err)
		}
		if ledgerRes != nil {
			detail.LedgerID = &ledgerRes.LedgerID
		}
		// The real usage and its price make the row an ordinary billed one.
		g, tk := store.G(ctx, tx), p.Tokens
		if err := g.Model(&model.UsageLog{}).Where("id = ?", e.usageLogID).Updates(map[string]any{
			"input_tokens": tk.Input, "output_tokens": tk.Output, "cache_read_tokens": tk.CacheRead,
			"cache_creation_tokens": tk.CacheCreation, "cache_creation_1h_tokens": tk.CacheCreation1h,
			"metrics": store.Bytes(jsonOr(p.Metrics, "{}")), "total_cost": cost.total, "billing_status": StatusBilled,
			"billing_detail": store.Bytes(jsonOr(detail, "{}")), "matched_tier": trunc(detail.Tier, 100), "expr_hash": exprHash,
			"base_cost": cost.base,
		}).Error; err != nil {
			return err
		}
		if err := entryRow(g, e.id).Updates(map[string]any{
			"state": SettleStateSettled, "attempts": gorm.Expr("attempts + 1"), "last_error": "",
		}).Error; err != nil {
			return err
		}
		if s.events != nil {
			return s.events.Emit(ctx, tx, recordedEvent(p, total, StatusBilled))
		}
		return nil
	})
	if errors.Is(err, errNotPending) {
		return
	}
	if err != nil {
		s.rec.log.Error("reconcile: settle", "entry", e.id, "request_id", p.RequestID, "err", err)
		return
	}
	s.cacheBalance(ctx, p.UserID, ledgerRes)
	s.rec.log.Info("reconcile: settled", "entry", e.id, "request_id", p.RequestID, "plugin", e.pluginKey,
		"ref_id", e.refID, "reserved", row.totalCost.String(), "final", total.String())
}

// refundFailed gives the whole reservation back: the upstream says the work
// produced nothing, so there is nothing to bill for.
func (s *Service) refundFailed(ctx context.Context, e *settleEntry, row *reservedRowState, reason string) {
	p := row.p
	var ledgerRes *core.LedgerResult
	ctx, cancel := bookkeeping(ctx)
	defer cancel()
	err := s.outcomeTx(ctx, e, func(tx *gorm.DB) error {
		if err := s.lockTaskOutcome(ctx, tx, e, "closed"); err != nil {
			return err
		}
		status, err := lockUsageRow(ctx, tx, e.usageLogID)
		if err != nil {
			return err
		}
		if status != StatusReserved {
			return errNotPending
		}
		if row.totalCost.Sign() > 0 {
			var err error
			ledgerRes, err = s.ledger.ApplyTx(ctx, tx, core.LedgerChange{
				UserID: p.UserID, Amount: row.totalCost, Credit: true, Kind: "refund",
				RefType: "usage", RefID: p.RequestID, IdempotencyKey: "refund:" + p.RequestID + ":reconcile",
				Note: "reconciled: the upstream work failed",
			})
			if err != nil {
				return fmt.Errorf("ledger: %w", err)
			}
		}
		// 'free' is the honest status: nothing was delivered, nothing is
		// owed. It is also not in usage_logs_billing_pending_idx, so the
		// settlement retry loop will not pick the row up and bill it again.
		g := store.G(ctx, tx)
		if err := g.Model(&model.UsageLog{}).Where("id = ?", e.usageLogID).Updates(map[string]any{
			"total_cost": 0, "base_cost": 0, "billing_status": StatusFree, "success": false,
			"error_type": trunc(reason, 50), "error_message": trunc(reason, 1000),
		}).Error; err != nil {
			return err
		}
		if err := entryRow(g, e.id).Updates(map[string]any{
			"state": SettleStateFailed, "attempts": gorm.Expr("attempts + 1"), "last_error": trunc(reason, 2000),
			"poll_failures": pollFailures(e),
		}).Error; err != nil {
			return err
		}
		p.Success, p.ErrorType = false, trunc(reason, 50)
		if s.events != nil {
			return s.events.Emit(ctx, tx, recordedEvent(p, decimal.Zero, StatusFree))
		}
		return nil
	})
	if errors.Is(err, errNotPending) {
		return
	}
	if err != nil {
		s.rec.log.Error("reconcile: refund a failed entry", "entry", e.id, "request_id", p.RequestID, "err", err)
		return
	}
	s.cacheBalance(ctx, p.UserID, ledgerRes)
	s.rec.log.Info("reconcile: upstream work failed, reservation refunded", "entry", e.id,
		"request_id", p.RequestID, "refunded", row.totalCost.String(), "reason", reason)
}

// abandon stops asking. The reservation becomes the final charge: the call
// really was made, the upstream really started the work, and making the
// platform eat a cost that was genuinely incurred because the upstream never
// answered a status query is the wrong default. A human can still reverse it
// (POST /usage/:id/refund). The write itself, and why it must say 'billed',
// is keepEstimate.
func (s *Service) abandon(ctx context.Context, e *settleEntry, row *reservedRowState, reason string) {
	marker := map[string]string{
		core.AnomalyReconcile:         core.ReconcileAbandoned,
		core.AnomalyReconcileAttempts: x.Itoa64(int64(e.attempts)),
		core.AnomalyReconcileError:    trunc(reason, 500),
	}
	if !s.keepEstimate(ctx, e, row, SettleStateAbandoned, marker, reason, false) {
		return
	}
	s.rec.log.Error("reconcile: gave up on a pre-charged entry; the estimate is now the final charge",
		"entry", e.id, "request_id", row.p.RequestID, "plugin", e.pluginKey, "ref_id", e.refID,
		"attempts", e.attempts, "charged", row.totalCost.String(), "reason", reason)
}

// settleEstimate closes an entry the plugin answered SETTLED_ESTIMATE to: the
// work finished, the upstream reported no usage for it, the estimate is the
// final charge (CONTRACTS §25.5 gap 2).
//
// It is the same ledger outcome as abandon - nothing moves, the row becomes
// 'billed' at the reservation - reached for the opposite reason: not "nobody
// could confirm the work" but "the work is confirmed and there is no figure
// to replace the estimate with". The row says which. usage_logs.anomalies
// carries reconcile=estimated rather than abandoned, and the entry closes as
// 'estimated' rather than 'abandoned', so a revenue summary counts both as
// estimates while an operator reading either row is not told the wrong story.
//
// Before this state existed a plugin in this position had two answers, both
// wrong: SETTLED with zero tokens refunded the whole reservation for a job
// that was delivered, and keeping its own table of estimates to answer
// SETTLED from meant every reserving plugin rebuilding what the core already
// holds on the row.
func (s *Service) settleEstimate(ctx context.Context, e *settleEntry, row *reservedRowState, res *pluginv1.ReconcileResult) {
	if t := res.GetTokens(); t.GetInputTokens() != 0 || t.GetOutputTokens() != 0 || t.GetCacheReadTokens() != 0 ||
		t.GetCacheCreationTokens() != 0 || t.GetCacheCreation_1HTokens() != 0 || len(res.GetFacts()) > 0 {
		// Not an error, but a plugin that has real figures should answer
		// SETTLED with them; ignoring what it sent is the contract, saying so
		// is what keeps the contract from being a surprise.
		s.rec.log.Warn("reconcile: SETTLED_ESTIMATE carried tokens or facts; they are ignored, the estimate stands",
			"entry", e.id, "plugin", e.pluginKey, "ref_id", e.refID)
	}
	reason := res.GetReason()
	marker := map[string]string{
		core.AnomalyReconcile:         core.ReconcileEstimated,
		core.AnomalyReconcileAttempts: x.Itoa64(int64(e.attempts + 1)),
	}
	if reason != "" {
		marker[core.AnomalyReconcileError] = trunc(reason, 500)
	}
	if !s.keepEstimate(ctx, e, row, SettleStateEstimated, marker, reason, true) {
		return
	}
	s.rec.log.Info("reconcile: upstream confirmed the work without a usage figure; the estimate is the final charge",
		"entry", e.id, "request_id", row.p.RequestID, "plugin", e.pluginKey, "ref_id", e.refID,
		"charged", row.totalCost.String(), "note", reason)
}

// keepEstimate is the one write that turns a reserved row into a final billed
// one WITHOUT moving money: the reservation is the charge. abandon and
// settleEstimate both end here; marker is what they disagree about. It
// reports whether the row was closed by this call (false when another actor
// got there first, or on a database error, both already logged as needed).
//
// THE STATUS MUST BE 'billed'. Not 'failed', which reads more naturally and
// is a duplicate charge:
//
//	CREATE INDEX usage_logs_billing_pending_idx ON usage_logs (id)
//	  WHERE billing_status IN ('pending', 'failed');
//
// That index is how the settlement retry loop finds work. The money for this
// row already left the balance when the reservation was charged; putting the
// row back into that index would have the retry loop price it again and take
// the amount a second time - silently, minutes later, on a row nobody is
// looking at. 'billed' is not a euphemism here, it is the truth: an amount
// was computed, charged, and is final.
func (s *Service) keepEstimate(ctx context.Context, e *settleEntry, row *reservedRowState, entryState string,
	marker map[string]string, note string, countAttempt bool) bool {
	p := row.p
	ctx, cancel := bookkeeping(ctx)
	defer cancel()
	err := s.outcomeTx(ctx, e, func(tx *gorm.DB) error {
		observation := "closed"
		if entryState == SettleStateAbandoned {
			observation = "abandoned"
		}
		if err := s.lockTaskOutcome(ctx, tx, e, observation); err != nil {
			return err
		}
		status, err := lockUsageRow(ctx, tx, e.usageLogID)
		if err != nil {
			return err
		}
		if status != StatusReserved {
			return errNotPending
		}
		g := store.G(ctx, tx)
		if err := markUsageRow(g, e.usageLogID, StatusBilled, nil, marker).Error; err != nil {
			return err
		}
		attempts := 0
		if countAttempt {
			attempts = 1
		}
		if err := entryRow(g, e.id).Updates(map[string]any{
			"state": entryState, "last_error": trunc(note, 2000), "attempts": gorm.Expr("attempts + ?", attempts),
		}).Error; err != nil {
			return err
		}
		if s.events != nil {
			return s.events.Emit(ctx, tx, recordedEvent(p, row.totalCost, StatusBilled))
		}
		return nil
	})
	if errors.Is(err, errNotPending) {
		return false
	}
	if err != nil {
		s.rec.log.Error("reconcile: close at the estimate", "entry", e.id, "request_id", p.RequestID,
			"state", entryState, "err", err)
		return false
	}
	return true
}

// ---------------------------------------------------------------- helpers

// loadReserved reads everything needed to reprice and close one usage row.
func (s *Service) loadReserved(ctx context.Context, usageLogID int64) (*reservedRowState, error) {
	p := &pending{}
	st := &reservedRowState{p: p}
	row := frozenPrice(store.G(ctx, s.db.Gorm), ", u.billing_status, u.total_cost").Where("u.id = ?", usageLogID).Row()
	if err := scanPending(row, p, &st.status, &st.totalCost); err != nil {
		return nil, err
	}
	return st, nil
}

// reconciledFacts types the facts the plugin reported for the real usage
// against the same manifest declarations the gateway checks a live report
// against (CONTRACTS §25.3): only declared keys survive, and only values that
// fit the declared type. Returns nil when nothing could be resolved, in which
// case the estimate's metrics stand.
func (s *Service) reconciledFacts(ctx context.Context, e *settleEntry, p *pending, facts map[string]string) map[string]any {
	if len(facts) == 0 {
		return nil
	}
	rules, ok := s.usageRulesFor(p)
	if !ok {
		s.rec.log.Warn("reconcile: cannot resolve the usage facts of this protocol, keeping the estimate's metrics",
			"entry", e.id, "protocol", p.UpstreamProtocol)
		return nil
	}
	// A poll often reports only changed facts. Keep request-time facts (such
	// as video input) when upstream does not echo them in its final response.
	out := make(map[string]any, len(p.Metrics)+len(facts))
	for key, value := range p.Metrics {
		out[key] = value
	}
	for key, raw := range facts {
		f, ok := rules.Facts[key]
		if !ok {
			s.rec.log.Warn("reconcile: plugin reported an undeclared usage fact, dropped",
				"entry", e.id, "plugin", e.pluginKey, "fact", key)
			continue
		}
		v, ok := usagerules.Fact(f, raw)
		if !ok {
			s.rec.log.Warn("reconcile: plugin reported a usage fact that does not fit its declared type, dropped",
				"entry", e.id, "plugin", e.pluginKey, "fact", key, "type", f.Type)
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// usageRulesFor resolves the usage rules of the row's upstream protocol, the
// same way the gateway and the console account test do (usagerules.For): the
// account type's per-protocol override, else the endpoint, else the platform.
func (s *Service) usageRulesFor(p *pending) (manifest.UsageRules, bool) {
	gen := s.rec.Registry.Current()
	if gen == nil || p.UpstreamProtocol == "" {
		return manifest.UsageRules{}, false
	}
	pb, ok := gen.PlatformForProtocol(p.UpstreamProtocol)
	if !ok {
		return manifest.UsageRules{}, false
	}
	var ap manifest.AccountPlatform
	if bt, ok := gen.AccountType(p.PluginKey, p.AccountType); ok {
		ap, _ = bt.Supports(pb.Platform.ID)
	}
	var ep *manifest.Endpoint
	for i := range pb.Platform.Endpoints {
		if pb.Platform.Endpoints[i].Protocol == p.UpstreamProtocol {
			ep = &pb.Platform.Endpoints[i]
			break
		}
	}
	return usagerules.For(ap, ep, &pb.Platform, p.UpstreamProtocol), true
}

// claimedEntry returns the columns of a settleEntry from a claim.
var claimedEntry = clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "plugin_key"}, {Name: "ref_id"},
	{Name: "usage_log_id"}, {Name: "account_id"}, {Name: "attempts"}, {Name: "poll_failures"}, {Name: "created_at"}, {Name: "deadline_at"}}}

// entryRow is the pending_settlements row id.
func entryRow(db *gorm.DB, id int64) *gorm.DB {
	return db.Model(&model.PendingSettlement{}).Where("id = ?", id)
}

// pollFailures counts e's poll: one more failure in a row, or none after
// an observation.
func pollFailures(e *settleEntry) clause.Expr {
	return gorm.Expr("CASE WHEN ? THEN poll_failures+1 WHEN ? THEN 0 ELSE poll_failures END", e.pollFailed, e.pollObserved)
}
