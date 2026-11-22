package iam

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// DeleteUser soft-deletes a user and ends their sessions.
func (s *Service) DeleteUser(ctx context.Context, actorID, id int64) error {
	if actorID == id {
		return errSelfDisable
	}

	// SEC-H1: check authorization to delete target user (fails fast; checked
	// again below)
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
		return err
	}

	var v int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		v, err = s.deleteUserTx(ctx, tx, actorID, id)
		return err
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(ctx, id)
	s.authz.Committed(ctx, v)
	return nil
}

// deleteUserTx bumps authz, checks the deletion again under it and the
// user's row lock, and soft-deletes the user (softDeleteUser). It returns
// the authz version of the bump.
func (s *Service) deleteUserTx(ctx context.Context, tx *gorm.DB, actorID, id int64) (int64, error) {
	if err := store.RequireTx(tx); err != nil {
		return 0, err
	}
	v, err := s.authz.Bump(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	// Checked again after Bump, which holds role changes off until
	// commit: the target may have been given a role the actor does not
	// manage since the check above (as applyUpdateTx does).
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
		return 0, err
	}
	var email string
	err = lockLiveUser(store.G(ctx, tx), id, "email").Row().Scan(&email)
	if store.IsNoRows(err) {
		return 0, core.ErrNotFound.WithMessage("user not found")
	}
	if err != nil {
		return 0, err
	}
	if err := s.authz.EnsureNotLastSuperAdmin(ctx, tx, id); err != nil {
		return 0, err
	}
	if err := s.softDeleteUser(ctx, tx, actorID, id, email); err != nil {
		return 0, err
	}
	return v, nil
}

// softDeleteUser marks the user deleted, with everything that goes with
// it: its personal segment, sessions and API keys; then emits and audits
// the deletion (SEC-M5).
func (s *Service) softDeleteUser(ctx context.Context, tx *gorm.DB, actorID, id int64, email string) error {
	g := store.G(ctx, tx)
	// A soft delete does not fire the cascade of segments.user_id: the
	// user goes back to the default segment and a personal one is removed
	// here, with its grants and rates.
	if err := updateUser(g, id, userDeletion()).Error; err != nil {
		return err
	}
	if err := segment.DropPersonal(ctx, tx, id); err != nil {
		return err
	}
	if err := s.endAllSessions(ctx, tx, id); err != nil {
		return err
	}
	// The user's API keys go with the user: authentication already
	// rejects them, and left behind they would block deleting their group.
	if err := deleteUserKeys(g, id).Error; err != nil {
		return err
	}
	if err := s.emit(ctx, tx, "user.updated", id, email, "deleted"); err != nil {
		return err
	}
	detail := map[string]any{"email": email}
	return audit.Audit(ctx, tx, actorID, "user.delete", "user", strconv.FormatInt(id, 10), detail)
}

// userDeletion soft-deletes a user and moves it back to the default segment.
func userDeletion() map[string]any {
	return map[string]any{"deleted_at": gorm.Expr("now()"), "updated_at": gorm.Expr("now()"), "segment_id": gorm.Expr("default_segment_id()")}
}

// deleteUserKeys soft-deletes the live API keys of userID.
func deleteUserKeys(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.APIKey{}).Where("user_id = ? AND deleted_at IS NULL", userID).Update("deleted_at", gorm.Expr("now()"))
}
