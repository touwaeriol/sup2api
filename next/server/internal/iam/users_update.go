package iam

import (
	"context"
	"slices"
	"strconv"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// UpdateUserInput is the body of PATCH /users/:id (all fields optional).
type UpdateUserInput struct {
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	Status      *string `json:"status"`
	Password    *string `json:"password"`
	// OwnerID moves the user to this owner, 0 for none; nil keeps it.
	OwnerID *int64 `json:"owner_id"`
}

// consumeSelfChange spends the step-up proof a change of one's own sign-in
// needs: the password scope when it changes the password, the email one
// when only the email.
func (s *Service) consumeSelfChange(ctx context.Context, in UpdateUserInput) error {
	switch {
	case in.Password != nil:
		return s.ConsumeStepUp(ctx, core.StepUpSelfPassword)
	case in.Email != nil:
		return s.ConsumeStepUp(ctx, core.StepUpSelfEmail)
	}
	return nil
}

var errSelfDisable = core.ErrConflict.WithMessage("you cannot disable or delete yourself")

// validateUpdate normalizes the partial update in place (email casing,
// display-name trimming) and collects field errors in response order.
func validateUpdate(in UpdateUserInput) (email string, fields []core.FieldError) {
	if in.Email != nil {
		email, fields = normalizeEmail(*in.Email)
	}
	if in.DisplayName != nil {
		var f *core.FieldError
		if *in.DisplayName, f = normalizeDisplayName(*in.DisplayName); f != nil {
			fields = append(fields, *f)
		}
	}
	if in.Status != nil && *in.Status != StatusActive && *in.Status != StatusDisabled {
		fields = append(fields, core.FieldError{Field: "status", Code: "invalid", Message: "must be active or disabled"})
	}
	if in.Password != nil {
		if f := validatePassword("password", *in.Password); f != nil {
			fields = append(fields, *f)
		}
	}
	if f := validateOwner(in.OwnerID); f != nil {
		fields = append(fields, *f)
	}
	return email, fields
}

// UpdateUser applies a partial update to a user in scope (the actor's of
// user:update, core.UserRows); one out of it is not found.
func (s *Service) UpdateUser(ctx context.Context, actorID, id int64, scope core.DataScope, in UpdateUserInput) (*User, error) {
	email, fields := validateUpdate(in)
	if len(fields) > 0 {
		return nil, core.InvalidFields(fields...)
	}
	if err := s.authorizeUpdate(ctx, actorID, id, scope, in); err != nil {
		return nil, err
	}
	hash, err := s.hashNewPassword(in.Password)
	if err != nil {
		return nil, err
	}
	var v int64
	var u *User
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		v, u, err = s.applyUpdateTx(ctx, tx, actorID, id, scope, in, email, hash)
		return err
	})
	if err != nil {
		return nil, err
	}
	if hash != nil || (in.Status != nil && *in.Status == StatusDisabled) {
		s.sessionsEnded(ctx, id) // applyUpdateTx ended every session
	} else {
		s.sessionsChanged(ctx, id)
	}
	if v != 0 {
		s.authz.Committed(ctx, v)
	}
	return u, nil
}

// authorizeUpdate makes the checks of UpdateUser before its transaction, in
// this order: the target in scope, the step-up of a change of one's own
// sign-in, the target managed by the actor, and a password reset of another.
func (s *Service) authorizeUpdate(ctx context.Context, actorID, id int64, scope core.DataScope, in UpdateUserInput) error {
	// Before any other check, so that they tell nothing of a user out of
	// scope; checked again under the lock.
	if _, err := s.getUserIn(ctx, s.db.Gorm, id, scope); err != nil {
		return err
	}
	// SEC-H3: a user's own email or password changes only with a step-up
	// proof (the password one: the password and a second factor), as on
	// /me/password; this route must not be the way around it for an access
	// key or a stolen token. Spent before the guard, which then lets the
	// super admin's own access key through on it.
	if actorID == id {
		if err := s.consumeSelfChange(ctx, in); err != nil {
			return err
		}
	}
	// SEC-H1, FU6: the target, oneself included, must be a user the actor
	// manages (its roles within the actor's and assignable by it).
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
		return err
	}
	// SEC-H1: password reset for others requires user:password:reset and a
	// step-up proof; the system (actor 0, the server CLI) holds no roles and
	// is not checked.
	if in.Password != nil && actorID != id && actorID != 0 {
		return s.authorizePasswordReset(ctx, actorID)
	}
	return nil
}

// authorizePasswordReset checks that actorID holds user:password:reset, then
// spends its step-up proof.
func (s *Service) authorizePasswordReset(ctx context.Context, actorID int64) error {
	reset, err := core.Can(core.WithUserID(ctx, actorID), s.authz, "user:password:reset")
	if err != nil {
		return err
	}
	if !reset {
		return core.ErrPermissionDenied.WithMessage("missing permission: user:password:reset").
			WithDetails(map[string]any{"permission": "user:password:reset"})
	}
	return s.ConsumeStepUp(ctx, core.StepUpUserPasswordReset)
}

// hashNewPassword is the bcrypt hash of password, nil when it is not set.
func (s *Service) hashNewPassword(password *string) ([]byte, error) {
	if password == nil {
		return nil, nil
	}
	return bcrypt.GenerateFromPassword([]byte(*password), s.cost)
}

// applyUpdateTx performs the locked read, authorization checks and the write
// inside the caller's transaction.
func (s *Service) applyUpdateTx(ctx context.Context, tx *gorm.DB, actorID, id int64, scope core.DataScope, in UpdateUserInput, email string, hash []byte) (int64, *User, error) {
	v, err := s.lockUpdateTarget(ctx, tx, actorID, id, in)
	if err != nil {
		return 0, nil, err
	}
	cur, err := s.getUserIn(ctx, tx, id, scope)
	if err != nil {
		return 0, nil, err
	}
	statusChange := in.Status != nil && *in.Status != cur.Status
	if statusChange {
		if err := s.checkStatusChange(ctx, tx, actorID, id, *in.Status); err != nil {
			return 0, nil, err
		}
	}
	if err := writeUserUpdate(ctx, tx, id, userChanges(in, email, hash)); err != nil {
		return 0, nil, err
	}
	// SEC-M1: a new password or disabling ends every session
	if hash != nil || (statusChange && *in.Status == StatusDisabled) {
		if err := s.endAllSessions(ctx, tx, id); err != nil {
			return 0, nil, err
		}
	}
	u, err := s.recordUserUpdated(ctx, tx, actorID, id, in)
	if err != nil {
		return 0, nil, err
	}
	return v, u, nil
}

// lockUpdateTarget takes the locks of an update in the established
// authz -> user order: the authz bump of a status change, the owner's move
// (SetOwner, under the authz lock), then the user's row. It returns the
// authz version of the bump, 0 for none.
func (s *Service) lockUpdateTarget(ctx context.Context, tx *gorm.DB, actorID, id int64, in UpdateUserInput) (int64, error) {
	var v int64
	// Even an apparently unchanged status must invalidate authorization
	// after concurrent edits.
	if in.Status != nil {
		var err error
		if v, err = s.authz.Bump(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	// A new owner moves the user between teams: SetOwner checks and writes
	// it under the authz lock, before the user's row is locked.
	if in.OwnerID != nil {
		if _, err := s.owners.SetOwner(ctx, tx, actorID, id, false, in.OwnerID); err != nil {
			return 0, err
		}
	}
	if err := lockUserRow(ctx, tx, id); err != nil {
		if err == errNoUser {
			return 0, core.ErrNotFound.WithMessage("user not found")
		}
		return 0, err
	}
	return v, nil
}

// checkStatusChange checks a change of the user id's status to status: not
// one's own, of a user the actor manages, and not disabling the last super
// admin.
func (s *Service) checkStatusChange(ctx context.Context, tx *gorm.DB, actorID, id int64, status string) error {
	if actorID == id {
		return errSelfDisable
	}
	// SEC-H2: target permissions must not exceed actor's - in both
	// directions. Re-enabling a disabled account hands its holder every
	// permission its roles grant, so it is as sensitive as disabling.
	// Checked again after Bump, which holds role changes off until commit.
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
		return err
	}
	if status == StatusDisabled {
		return s.authz.EnsureNotLastSuperAdmin(ctx, tx, id)
	}
	return nil
}

// writeUserUpdate writes set to the user id; a taken email is a conflict.
func writeUserUpdate(ctx context.Context, tx *gorm.DB, id int64, set map[string]any) error {
	err := updateUser(store.G(ctx, tx), id, set).Error
	if store.IsUniqueViolation(err, "") {
		return emailTaken()
	}
	return err
}

// recordUserUpdated reads the updated user, emits user.updated and audits
// what the update changed (SEC-M5).
func (s *Service) recordUserUpdated(ctx context.Context, tx *gorm.DB, actorID, id int64, in UpdateUserInput) (*User, error) {
	u, err := s.getUser(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := s.emit(ctx, tx, "user.updated", id, u.Email, u.Status); err != nil {
		return nil, err
	}
	if err := audit.Audit(ctx, tx, actorID, "user.update", "user", strconv.FormatInt(id, 10), updateAuditDetail(in)); err != nil {
		return nil, err
	}
	return u, nil
}

// updateAuditDetail is what the audit of an update records: which fields it
// changed, and the status it set.
func updateAuditDetail(in UpdateUserInput) map[string]any {
	detail := map[string]any{}
	if in.Email != nil {
		detail["email_changed"] = true
	}
	if in.DisplayName != nil {
		detail["display_name_changed"] = true
	}
	if in.Status != nil {
		detail["status"] = *in.Status
	}
	if in.Password != nil {
		detail["password_changed"] = true
	}
	return detail
}

// userChanges are the columns applyUpdateTx writes: those the update sets
// (the owner aside, which SetOwner writes), and updated_at.
func userChanges(in UpdateUserInput, email string, hash []byte) map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("now()")}
	store.SetIf(set, "email", nilStr(email))
	store.SetIf(set, "display_name", in.DisplayName)
	store.SetIf(set, "status", in.Status)
	store.SetIf(set, "password_hash", nilBytes(hash))
	return set
}

// updateUser writes set to the user id, deleted or not.
func updateUser(db *gorm.DB, id int64, set map[string]any) *gorm.DB {
	return db.Model(&model.User{}).Where("id = ?", id).Updates(set)
}

// SetUserRoles replaces a user's roles (PUT /users/:id/roles). An access
// key assigns no roles (it has no assignable list, §43.6), so the super
// admin's own roles never reach the step-up of the guard through one.
func (s *Service) SetUserRoles(ctx context.Context, actorID, id int64, roleKeys []string) (*User, error) {
	var v int64
	var u *User
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		cur, err := s.getUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if v, err = s.authz.SetUserRoles(ctx, tx, actorID, id, roleKeys); err != nil {
			return err
		}
		if u, err = s.getUser(ctx, tx, id); err != nil {
			return err
		}
		if !slices.Equal(cur.Roles, u.Roles) {
			return s.emit(ctx, tx, "user.updated", id, u.Email, u.Status)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.authz.Committed(ctx, v)
	return u, nil
}
