package usage

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// monitorOutcome is the claim a Monitor result is applied under: its
// receipt commits with the outcome, so one claim has one outcome.
type monitorOutcome struct {
	id, digest, taskID string
}

// outcomeTx adds the result receipt to the existing financial transaction.
// Its advisory lock serializes the claim's outcome before task→usage→pending.
func (s *Service) outcomeTx(ctx context.Context, e *settleEntry, fn func(*gorm.DB) error) error {
	m := e.monitor
	if m == nil {
		return s.db.Tx(ctx, fn)
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		if err := store.XactLock(ctx, tx, "plugin-monitor:"+m.id); err != nil {
			return err
		}
		var digest string
		err := g.Model(&model.PluginMonitorReceipt{}).Select("digest").Where("id = ?", m.id).Row().Scan(&digest)
		if err == nil {
			if digest != m.digest {
				return core.ErrConflict.WithMessage("monitor result already committed")
			}
			return nil
		}
		if !store.IsNoRows(err) {
			return err
		}
		if err = fn(tx); err != nil {
			return err
		}
		return g.Create(&model.PluginMonitorReceipt{ID: m.id, Digest: m.digest, TaskID: m.taskID}).Error
	})
}

// askMonitor runs the plugin's Monitor once for e on the account call
// holds. Any error is one failed query; a fetch the account's limiter
// refused is errTaskDeferred, whatever the plugin made of it. A result
// arms e.monitor for the outcome written from it.
func (s *Service) askMonitor(ctx context.Context, e *settleEntry, call observationCall) (*pluginv1.ReconcileResult, error) {
	var deferred atomic.Bool
	fetch := func(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return s.fetchExecutionHTTP(ctx, in, e.account.ID, func(ctx context.Context) error {
			if s.rec.Limiter == nil {
				return nil
			}
			if ok, err := s.rec.Limiter.TryHit(ctx, e.account, "", ""); err != nil || !ok {
				deferred.Store(true)
				return errTaskDeferred
			}
			return nil
		})
	}
	res, err := call.client.Monitor(ctx, &pluginv1.PollRequest{Entry: call.entry, Account: call.acct}, fetch)
	switch {
	case deferred.Load():
		return nil, errTaskDeferred
	case err != nil:
		return nil, err
	case res == nil:
		return nil, errors.New("Monitor returned no result")
	}
	e.monitor = &monitorOutcome{id: fmt.Sprintf("entry:%d:%s", e.id, uuid.NewString()), digest: core.FactDigest(res)}
	if e.task != nil {
		e.monitor.id, e.monitor.taskID = e.task.id+":"+e.task.token, e.task.id
	}
	return res, nil
}

// observe asks the plugin about e once and applies the answer. An outcome
// whose transaction fails is logged and left alone: the claim's lease
// lapses and the next round asks again.
func (s *Service) observe(ctx context.Context, e *settleEntry, row *reservedRowState) {
	cfg := s.reconcileSettings(ctx)
	res, err := s.askPlugin(ctx, e, row)
	if err != nil {
		// Query failures have a persisted consecutive-failure budget. Local
		// account deferrals and caller cancellation do not spend that budget.
		s.handlePollError(ctx, e, row, cfg, err)
		return
	}
	if s.handlePollResult(ctx, e, row, cfg, res) {
		return
	}
	if err := taskObservation(e, res); err != nil {
		s.handlePollError(ctx, e, row, cfg, err)
		return
	}
	state := res.GetState()
	if state == pluginv1.ReconcileResult_PENDING {
		// A plugin that cannot tell yet means "ask again" - never "settle
		// for nothing".
		s.reschedule(ctx, e, cfg, time.Duration(res.GetNextCheckAfterSec())*time.Second, "")
		return
	}
	if e.task != nil && row.status != StatusReserved {
		s.finishTaskOnly(ctx, e, "closed")
		return
	}
	switch state {
	case pluginv1.ReconcileResult_SETTLED:
		s.settleReconciled(ctx, e, row, res)
	case pluginv1.ReconcileResult_SETTLED_ESTIMATE:
		s.settleEstimate(ctx, e, row, res)
	case pluginv1.ReconcileResult_FAILED:
		s.refundFailed(ctx, e, row, res.GetReason())
	}
}

// taskObservation takes a task's snapshot and state from a result of one
// of the states handlePollResult passes on. A terminal one must carry the
// snapshot the client will be shown.
func taskObservation(e *settleEntry, res *pluginv1.ReconcileResult) error {
	t := e.task
	if t == nil {
		return nil
	}
	state := res.GetState()
	t.snapshot = nil
	if raw := []byte(res.GetTaskSnapshotJson()); len(raw) > 0 {
		// Paths are captured from the same generation as the Monitor call.
		if _, err := protocol.RewriteTaskID(raw, t.paths, e.refID, t.id); err != nil {
			return err
		}
		t.snapshot = raw
	} else if state != pluginv1.ReconcileResult_PENDING {
		return errors.New("terminal task observation is missing its snapshot")
	}
	switch state {
	case pluginv1.ReconcileResult_FAILED:
		t.state = "failed"
	case pluginv1.ReconcileResult_SETTLED, pluginv1.ReconcileResult_SETTLED_ESTIMATE:
		t.state = "succeeded"
	}
	return nil
}
