package usage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

var _ core.AsyncTasks = (*Service)(nil)

// The statements of the task endpoints are on the request path: BeginTask,
// ReceiveTask and registerTask (through CommitExecution) run for every task
// submission, FindTask for every poll of a client.

func (s *Service) BeginTask(ctx context.Context, in core.TaskIntent) (string, error) {
	// The dispatch identity of the submission; the database fills its
	// state, expiry and timestamps.
	id := "s2task_" + uuid.NewString()
	return id, store.G(ctx, s.db.Gorm).Create(&model.TaskSubmissionReceipt{RequestID: in.RequestID, PublicID: id,
		PluginKey: in.PluginKey, Kind: in.Kind, UserID: in.UserID, APIKeyID: in.APIKeyID, GroupID: in.GroupID,
		AccountID: in.AccountID, Model: in.Model, Protocol: in.Protocol}).Error
}

func (s *Service) ReceiveTask(ctx context.Context, requestID string, status int, body []byte, reason string) error {
	if len(body) > protocol.MaxTaskSnapshotBytes {
		body = nil
		reason = "response exceeds task receipt limit"
	}
	state := "received"
	if reason != "" {
		state = "uncertain"
	}
	// For a submission not yet registered. A nil body keeps the stored one;
	// an empty one is stored.
	return receipt(store.G(ctx, s.db.Gorm), requestID).Where("state <> 'registered'").Updates(map[string]any{
		"state": state, "response_status": status, "response_body": gorm.Expr("COALESCE(?,response_body)", store.Bytes(body)),
		"last_error": trunc(reason, 2000),
	}).Error
}

// registerTask writes a submitted task with its usage record in one
// transaction between before and after (CommitExecution's): the usage row,
// its reserve or settlement, the task and its monitoring. A registration
// that cannot be written as it is (malformed, its request already
// registered, its receipt not matching) is refused
// (core.ErrExecutionRefused): retrying the same commit changes nothing.
func (s *Service) registerTask(ctx context.Context, in core.TaskRegistration, before func(*gorm.DB) error, after func(*gorm.DB) error) error {
	r := in.Record
	if r == nil || r.AccountID == nil || in.PublicID == "" || in.UpstreamID == "" {
		return refused("invalid task registration")
	}
	if _, err := protocol.RewriteTaskID(in.Snapshot, in.IDPaths, in.UpstreamID, in.PublicID); err != nil {
		return refused("task snapshot: " + err.Error())
	}
	cfg := s.withEnvironment(ctx, s.reconcileSettings(ctx), r.GroupID, r.AccountID)
	now := time.Now()
	deadline := now.Add(cfg.clampDeadline(in.Deadline))
	next := now.Add(cfg.clampDelay(in.NextCheckAfter, 0))
	if next.After(deadline) {
		next = deadline
	}
	if r.Reservation != nil {
		r.Reservation.RefID = in.PublicID
	}
	_, err := s.insertAtomic(ctx, []*core.UsageRecord{r}, before, func(tx *gorm.DB, ids map[string]int64) error {
		uid, ok := ids[r.RequestID]
		if !ok {
			return refused("task request ID already registered")
		}
		if err := recordTask(ctx, tx, in, uid, next, deadline); err != nil {
			return err
		}
		return after(tx)
	}, true)
	return err
}

// recordTask writes a submitted task in the transaction of its usage row
// uid: it checks the receipt, inserts the task, links the row's
// settlement entry and closes the receipt.
func recordTask(ctx context.Context, tx *gorm.DB, in core.TaskRegistration, uid int64, next, deadline time.Time) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	r := in.Record
	// The receipt is the trusted dispatch identity; parsing cannot substitute it.
	g := store.G(ctx, tx)
	var valid bool
	if err := receipt(g, r.RequestID).Select(`public_id=? AND plugin_key=? AND kind=? AND user_id=?
		AND api_key_id=? AND group_id=? AND account_id=? AND model=? AND protocol=?`,
		in.PublicID, r.PluginKey, in.Kind, r.UserID, r.APIKeyID, r.GroupID, *r.AccountID, r.Model, r.Protocol).
		Where("state='received'").Clauses(store.ForUpdate()).Row().Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return refused("task receipt identity mismatch")
	}
	err := g.Create(&model.AsyncTask{PublicID: in.PublicID, PluginKey: r.PluginKey, Kind: in.Kind, UpstreamRefID: in.UpstreamID,
		UserID: r.UserID, APIKeyID: r.APIKeyID, GroupID: r.GroupID, AccountID: *r.AccountID, Model: r.Model, Protocol: r.Protocol,
		UsageLogID: uid, Snapshot: in.Snapshot, SnapshotIDPaths: jsonOr(in.IDPaths, "[]"), NextCheckAt: next, DeadlineAt: deadline}).Error
	if err != nil {
		return err
	} // Identity collisions must never overwrite ownership.
	if err = g.Model(&model.PendingSettlement{}).Where("usage_log_id=?", uid).Update("task_public_id", in.PublicID).Error; err != nil {
		return err
	}
	return receipt(g, r.RequestID).Updates(map[string]any{"state": "registered", "response_body": gorm.Expr("NULL"),
		"last_error": "", "expires_at": gorm.Expr("LEAST(expires_at,?)", deadline)}).Error
}

// receipt is the task_submission_receipts row of a request.
func receipt(db *gorm.DB, requestID string) *gorm.DB {
	return db.Model(&model.TaskSubmissionReceipt{}).Where("request_id=?", requestID)
}

func (s *Service) FindTask(ctx context.Context, q core.TaskQuery) (*core.TaskSnapshot, error) {
	out, err := readTask(ctx, s.db.Gorm, q)
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("task not found")
	}
	return out, err
}

// readTask reads the task q names by its public ID; none is sql.ErrNoRows.
func readTask(ctx context.Context, db *gorm.DB, q core.TaskQuery) (*core.TaskSnapshot, error) {
	out := &core.TaskSnapshot{}
	var paths []byte
	err := store.G(ctx, db).Model(&model.AsyncTask{}).Select(`public_id,upstream_ref_id,model,state,
		CASE WHEN state='pending' AND deadline_at<=clock_timestamp() THEN 'abandoned' ELSE observation_status END,
		snapshot,snapshot_id_paths,failure_code,last_error`).
		Where("plugin_key=? AND kind=? AND user_id=? AND group_id=? AND public_id=?", q.PluginKey, q.Kind, q.UserID, q.GroupID, q.ID).
		Row().Scan(&out.PublicID, &out.UpstreamID, &out.Model, &out.State, &out.ObservationStatus, &out.Body, &paths, &out.FailureCode, &out.FailureReason)
	if err == nil {
		err = json.Unmarshal(paths, &out.IDPaths)
	}
	return out, err
}

func (s *Service) cleanTaskReceipts(ctx context.Context) {
	g := store.G(ctx, s.db.Gorm)
	expired := g.Model(&model.TaskSubmissionReceipt{}).Select("request_id").Where("expires_at <= now()").Order("expires_at").Limit(200)
	err := g.Where("request_id IN (?)", expired).Delete(&model.TaskSubmissionReceipt{}).Error
	if err != nil {
		s.rec.log.Warn("task receipt cleanup failed", "err", err)
	}
}
