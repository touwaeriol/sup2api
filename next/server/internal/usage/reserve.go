package usage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// reservedRow is one inserted usage row whose cost was estimated by a plugin.
type reservedRow struct {
	rec *core.UsageRecord
	id  int64
}

// balanceUpdate is a cache refresh owed once the transaction commits.
type balanceUpdate struct {
	userID, ledgerID int64
	balance          decimal.Decimal
}

// reserveTx prices a plugin's estimate, charges it and registers the entry
// for the reconcile loop, inside the caller's transaction.
//
// Every step is the ordinary one. The price comes from the administrator's
// table exactly as for a finished request; the charge is kind "usage" with
// idempotency key "usage:{request_id}", the same key settlement would have
// used, so a reservation and a later settlement of the same request can never
// both take the base amount. What is new is only the row's status and the
// pending_settlements entry.
func (s *Service) reserveTx(ctx context.Context, tx *gorm.DB, r reservedRow) (*core.LedgerResult, error) {
	rec := r.rec
	p := fromRecord(rec)
	cost, detail, _, err := s.priceOf(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("price reservation: %w", err)
	}
	total := cost.total
	res, err := s.settleLedgerTx(ctx, tx, p.UserID, p.RequestID, total)
	if err != nil {
		return nil, err
	}
	if total.Sign() > 0 {
		detail.LedgerID = &res.LedgerID
	}
	if err := store.G(ctx, tx).Model(&model.UsageLog{}).Where("id = ?", r.id).Updates(map[string]any{
		"total_cost": cost.total, "base_cost": cost.base, "billing_detail": store.Bytes(jsonOr(detail, "{}")), "matched_tier": trunc(detail.Tier, 100),
	}).Error; err != nil {
		return nil, err
	}
	return res, s.registerReservation(ctx, tx, r)
}

// registerReservation adds the pending_settlements entry of the reserved
// row r for the reconcile loop, inside the caller's transaction.
func (s *Service) registerReservation(ctx context.Context, tx *gorm.DB, r reservedRow) error {
	rv := r.rec.Reservation
	nextAt, deadlineAt := s.reservationTimes(ctx, tx, rv)
	// The reconcile entry of the row; no row when the plugin's ref_id is
	// registered already.
	entry := model.PendingSettlement{PluginKey: trunc(rv.PluginKey, 30), RefID: trunc(rv.RefID, 200), UsageLogID: r.id,
		AccountID: r.rec.AccountID, NextCheckAt: nextAt, DeadlineAt: deadlineAt}
	ins := store.G(ctx, tx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "plugin_key"}, {Name: "ref_id"}}, DoNothing: true}).
		Create(&entry)
	err := ins.Error
	if err == nil && ins.RowsAffected == 0 {
		// The plugin reused a ref_id that is already being reconciled, so
		// this row would never be looked at again: it would sit "reserved"
		// forever, charged and invisible to both loops. Close it here, the
		// same way an abandoned entry is closed - billed at the estimate,
		// with the reason on the row - instead of leaking it.
		return s.closeUnreconcilable(ctx, tx, r.id,
			"duplicate ref_id "+trunc(rv.RefID, 100)+" for plugin "+rv.PluginKey)
	}
	if err != nil {
		return fmt.Errorf("register pending settlement: %w", err)
	}
	return nil
}

// reservationTimes are the first check and the deadline of the reservation
// rv, clamped by the reconcile settings; the check never comes after the
// deadline.
func (s *Service) reservationTimes(ctx context.Context, tx *gorm.DB, rv *core.UsageReservation) (nextAt, deadlineAt time.Time) {
	rs := s.reconcileSettingsIn(ctx, tx)
	now := time.Now()
	deadlineAt = now.Add(rs.clampDeadline(rv.Deadline))
	nextAt = now.Add(rs.clampDelay(rv.NextCheckAfter, 0))
	if nextAt.After(deadlineAt) {
		nextAt = deadlineAt
	}
	return nextAt, deadlineAt
}

// closeUnreconcilable turns a reserved row into a final billed one: the
// estimate stands, and usage_logs.anomalies says why nobody will ever
// correct it. Same shape as the reconcile loop's abandon path, and for the
// same reason it writes 'billed' and never 'failed' - the money has already
// moved, and usage_logs_billing_pending_idx covers ('pending','failed').
func (s *Service) closeUnreconcilable(ctx context.Context, tx *gorm.DB, usageLogID int64, reason string) error {
	slog.ErrorContext(ctx, "usage: a reserved row cannot be reconciled, billing the estimate",
		"usage_log_id", usageLogID, "reason", reason)
	return markUsageRow(store.G(ctx, tx), usageLogID, StatusBilled, nil, map[string]string{
		core.AnomalyReconcile:      core.ReconcileAbandoned,
		core.AnomalyReconcileError: trunc(reason, 500),
	}).Error
}
