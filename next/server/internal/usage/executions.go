package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

var _ core.ExecutionRecords = (*Service)(nil)

// errExecutionDone ends the transaction of a repeated commit or close
// without writing anything; the caller reports success.
var errExecutionDone = errors.New("execution already ended")

func (s *Service) BeginExecution(ctx context.Context, in core.ExecutionIntent) error {
	if in.ID == "" || in.RequestID == "" || in.AccountID == 0 {
		return errors.New("invalid execution identity")
	}
	return store.G(ctx, s.db.Gorm).Create(&model.PluginExecution{ID: in.ID, RequestID: in.RequestID, PluginKey: in.PluginKey,
		AccountID: in.AccountID, Attempt: in.Attempt, TaskSubmit: in.TaskSubmit}).Error
}

func (s *Service) ObserveExecution(ctx context.Context, id string, rec *core.UsageRecord, observation []byte) error {
	if rec == nil {
		return errors.New("invalid execution observation")
	}
	// Any size is stored: record is jsonb and observation bytea, both
	// TOASTed (compressed, out of line) when large.
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	res := execution(store.G(ctx, s.db.Gorm), id).Where("state=? AND request_id=? AND account_id=?", core.ExecutionStateIntent, rec.RequestID, rec.AccountID).
		Updates(map[string]any{"state": core.ExecutionStateObserved, "record": store.Bytes(raw), "observation": store.Bytes(observation),
			"updated_at": gorm.Expr("clock_timestamp()")})
	if res.Error == nil && res.RowsAffected != 1 {
		return errors.New("execution identity or state mismatch")
	}
	return res.Error
}

func (s *Service) RenewExecution(ctx context.Context, id string) error {
	res := execution(store.G(ctx, s.db.Gorm), id).Where("state=?", core.ExecutionStateObserved).
		Update("updated_at", gorm.Expr("clock_timestamp()"))
	if res.Error == nil && res.RowsAffected == 0 {
		return core.ErrExecutionNotObserved
	}
	return res.Error
}

func (s *Service) CommitExecution(ctx context.Context, in core.ExecutionCommit) error {
	task := in.Operation == core.ExecutionOpWatch
	if in.Record == nil || in.Record.AccountID == nil || len(in.Digest) != 64 || (in.Task != nil) != task ||
		!task && in.Operation != core.ExecutionOpUsage {
		return refused("invalid execution commit")
	}
	before := func(tx *gorm.DB) error {
		x, err := lockExecution(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		if x.RequestID != in.Record.RequestID || x.PluginKey != in.Record.PluginKey || x.AccountID != *in.Record.AccountID {
			return refused("execution identity mismatch")
		}
		switch core.ValueOr(x.State, "") {
		case core.ExecutionStateObserved:
			return nil
		case core.ExecutionStateCommitted:
			if x.Operation == in.Operation && x.Digest == in.Digest {
				return errExecutionDone
			}
			return core.ErrConflict.WithMessage("execution facts already committed")
		case core.ExecutionStateUnreported:
			return core.ErrConflict.WithMessage("execution closed as unreported")
		}
		return refused("execution has no durable observation")
	}
	taskID := ""
	if task {
		taskID = in.Task.PublicID
	}
	// The commit drops the execution's facts.
	after := func(tx *gorm.DB) error {
		return execution(store.G(ctx, tx), in.ID).Updates(map[string]any{"state": core.ExecutionStateCommitted, "operation": in.Operation,
			"digest": in.Digest, "task_id": taskID, "record": gorm.Expr("NULL"), "observation": gorm.Expr("NULL"),
			"updated_at": gorm.Expr("clock_timestamp()")}).Error
	}
	var err error
	if task {
		reg := *in.Task
		reg.Record = in.Record
		err = s.registerTask(ctx, reg, before, after)
	} else {
		err = s.insertExecutionRecord(ctx, in.Record, before, after)
	}
	if errors.Is(err, errExecutionDone) {
		return nil
	}
	return err
}

func (s *Service) CloseExecutionUnreported(ctx context.Context, in core.ExecutionUnreported) error {
	// The stored record is immutable once observed, so it is read before
	// the transaction; the lock below rechecks the state it was read in.
	var x model.PluginExecution
	if err := execution(store.G(ctx, s.db.Gorm), in.ID).Select("state, request_id, plugin_key, account_id, record").Take(&x).Error; err != nil {
		return err
	}
	if err := closableExecution(x); err != nil {
		return ignoreDone(err)
	}
	var rec core.UsageRecord
	if err := json.Unmarshal(x.Record, &rec); err != nil {
		return err
	}
	before := func(tx *gorm.DB) error {
		x, err := lockExecution(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		return closableExecution(x)
	}
	after := func(tx *gorm.DB) error {
		return execution(store.G(ctx, tx), in.ID).Updates(map[string]any{"state": core.ExecutionStateUnreported,
			"updated_at": gorm.Expr("clock_timestamp()")}).Error
	}
	// A request that already has its usage record is closed without another:
	// the gateway submitted the free record itself when it could not confirm
	// the observation (stored, but its acknowledgement lost).
	rec.SetUnreported(in.Reason)
	free := &rec
	_, err := s.insertAtomic(ctx, []*core.UsageRecord{free}, before, func(tx *gorm.DB, _ map[string]int64) error { return after(tx) }, true)
	if err != nil {
		return ignoreDone(err)
	}
	slog.ErrorContext(ctx, "usage: plugin execution closed as unreported, not billed", "request_id", x.RequestID,
		"execution_id", in.ID, "plugin", x.PluginKey, "account_id", x.AccountID, "reason", in.Reason)
	return nil
}

// closableExecution says whether x may be closed as unreported: only an
// observed execution is; closing one already unreported is done.
func closableExecution(x model.PluginExecution) error {
	switch core.ValueOr(x.State, "") {
	case core.ExecutionStateObserved:
		return nil
	case core.ExecutionStateUnreported:
		return errExecutionDone
	case core.ExecutionStateCommitted:
		return core.ErrConflict.WithMessage("execution facts already committed")
	}
	return refused("execution has no durable observation")
}

// refused is a deterministic refusal of an execution write: retrying it
// changes nothing (core.ErrExecutionRefused).
func refused(msg string) error {
	return fmt.Errorf("%w: %s", core.ErrExecutionRefused, msg)
}

func ignoreDone(err error) error {
	if errors.Is(err, errExecutionDone) {
		return nil
	}
	return err
}

// insertExecutionRecord writes the usage record of an execution in one
// transaction between before and after; inserting it settles it or, free,
// releases the request's precharge.
func (s *Service) insertExecutionRecord(ctx context.Context, rec *core.UsageRecord, before func(*gorm.DB) error, after func(*gorm.DB) error) error {
	_, err := s.insertAtomic(ctx, []*core.UsageRecord{rec}, before, func(tx *gorm.DB, ids map[string]int64) error {
		if _, ok := ids[rec.RequestID]; !ok {
			return refused("usage identity already recorded")
		}
		return after(tx)
	}, true)
	return err
}

// lockExecution reads the execution row id and keeps it locked to the end
// of the transaction tx.
func lockExecution(ctx context.Context, tx *gorm.DB, id string) (model.PluginExecution, error) {
	var x model.PluginExecution
	if err := store.RequireTx(tx); err != nil {
		return x, err
	}
	err := execution(store.G(ctx, tx), id).Select("state, operation, digest, request_id, plugin_key, account_id").
		Clauses(store.ForUpdate()).Take(&x).Error
	return x, err
}

// recoverExecutions closes, as unreported, the observed synchronous
// executions whose gateway is gone: not renewed (RenewExecution, which sets
// updated_at like ObserveExecution) for core.ExecutionStaleAfter. The host's
// reading stays as evidence and is never billed. A task submission is left
// alone; its receipt keeps the upstream answer.
func (s *Service) recoverExecutions(ctx context.Context) error {
	var ids []string
	if err := store.G(ctx, s.db.Gorm).Model(&model.PluginExecution{}).
		Where("state=? AND NOT task_submit AND updated_at<clock_timestamp()-make_interval(secs=>?)", core.ExecutionStateObserved, core.ExecutionStaleAfter.Seconds()).
		Order("updated_at").Limit(100).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		// A conflict is an execution committed meanwhile: its facts stand.
		err := s.CloseExecutionUnreported(ctx, core.ExecutionUnreported{ID: id, Reason: "its gateway stopped renewing the observed execution"})
		if err != nil && core.AsError(err).Code != core.ErrConflict.Code {
			s.deferExecutionRecovery(ctx, id, err)
		}
	}
	return s.cleanupExecutionReceipts(ctx)
}

func (s *Service) deferExecutionRecovery(ctx context.Context, id string, cause error) {
	slog.ErrorContext(ctx, "usage: execution recovery deferred", "execution_id", id, "err", cause)
	// Rotate failures behind other observations. A record that cannot be
	// written must not monopolize the oldest LIMIT batch on every sweep.
	_ = execution(store.G(ctx, s.db.Gorm), id).Where("state=?", core.ExecutionStateObserved).
		Update("updated_at", gorm.Expr("clock_timestamp()")).Error
}

// Retention eligibility is seven days, whatever the state: an unreported
// execution's reading is evidence for that long. Bounded batches catch up
// without monopolizing the settlement retry worker or holding a long
// transaction.
func (s *Service) cleanupExecutionReceipts(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, table := range []string{"plugin_executions", "plugin_monitor_receipts"} {
		for range 20 {
			if ctx.Err() != nil {
				return nil
			}
			// Up to 1000 rows older than seven days, the oldest first.
			g := store.G(ctx, s.db.Gorm)
			old := g.Table(table).Select("id").Where("created_at<clock_timestamp()-interval '7 days'").Order("created_at").Limit(1000)
			res := g.Table(table).Where("id IN (?)", old).Delete(nil)
			if err := res.Error; err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if res.RowsAffected < 1000 {
				break
			}
		}
	}
	return nil
}

// execution is the plugin_executions row id.
func execution(db *gorm.DB, id string) *gorm.DB {
	return db.Model(&model.PluginExecution{}).Where("id = ?", id)
}
