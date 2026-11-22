package iam

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// CreateUserInput is the body of POST /users.
type CreateUserInput struct {
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Password    string   `json:"password"`
	RoleKeys    []string `json:"role_keys"`
	// SegmentID is the named segment the user starts in; nil = the default one.
	SegmentID *int64 `json:"segment_id"`
	// OwnerID is the user's owner, 0 for none; nil for none too, but for a
	// creator of users for its team only, which is the owner (CONTRACTS
	// §103.3, core.OwnerGuard).
	OwnerID *int64 `json:"owner_id"`
}

// IsDefaultRoles reports whether keys is empty or exactly the default role.
func IsDefaultRoles(keys []string) bool {
	return len(keys) == 0 || (len(keys) == 1 && keys[0] == authz.RoleUser)
}

// rolesOrDefault is keys, or the default "user" role when empty.
func rolesOrDefault(keys []string) []string {
	if len(keys) == 0 {
		return []string{authz.RoleUser}
	}
	return keys
}

// CreateUser creates a user; empty RoleKeys assigns the default "user" role.
// actorID is used for the superuser grant rules (0 = system).
func (s *Service) CreateUser(ctx context.Context, actorID int64, in CreateUserInput) (*User, error) {
	var u *User
	var v int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		u, v, err = s.createUserTx(ctx, tx, actorID, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.authz.Committed(ctx, v)
	return u, nil
}

func (s *Service) createUserTx(ctx context.Context, tx *gorm.DB, actorID int64, in CreateUserInput) (*User, int64, error) {
	email, fields := validateCreate(&in)
	if len(fields) > 0 {
		return nil, 0, core.InvalidFields(fields...)
	}
	if err := lockNamedSegment(ctx, tx, in.SegmentID); err != nil {
		return nil, 0, err
	}
	id, err := s.insertNewUser(ctx, tx, email, in)
	if err != nil {
		return nil, 0, err
	}
	// The one place the owner is decided and written (CONTRACTS §103.3);
	// whether the actor may name it comes before anything about it is read.
	if _, err := s.owners.SetOwner(ctx, tx, actorID, id, true, in.OwnerID); err != nil {
		return nil, 0, err
	}
	roles := rolesOrDefault(in.RoleKeys)
	// SetUserRoles checks the actor may grant the roles (SEC-H1) and act on
	// the user, under its owner, and that the owner manages the user by
	// them (owner_cannot_manage).
	v, err := s.authz.SetUserRoles(ctx, tx, actorID, id, roles)
	if err != nil {
		return nil, 0, err
	}
	if err := s.recordUserCreated(ctx, tx, actorID, id, email, roles); err != nil {
		return nil, 0, err
	}
	u, err := s.getUser(ctx, tx, id)
	return u, v, err
}

// validateCreate normalizes the new user in place (display-name trimming)
// and returns its normalized email with the field errors in response order.
func validateCreate(in *CreateUserInput) (email string, fields []core.FieldError) {
	email, fields = normalizeEmail(in.Email)
	if f := validatePassword("password", in.Password); f != nil {
		fields = append(fields, *f)
	}
	var f *core.FieldError
	if in.DisplayName, f = normalizeDisplayName(in.DisplayName); f != nil {
		fields = append(fields, *f)
	}
	if f := validateOwner(in.OwnerID); f != nil {
		fields = append(fields, *f)
	}
	return email, fields
}

// insertNewUser hashes the password and inserts the user (insertUser),
// returning its id; a taken email is a conflict.
func (s *Service) insertNewUser(ctx context.Context, tx *gorm.DB, email string, in CreateUserInput) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cost)
	if err != nil {
		return 0, err
	}
	var id int64
	err = insertUser(store.G(ctx, tx), email, in.DisplayName, string(hash), in.SegmentID).Row().Scan(&id)
	if store.IsUniqueViolation(err, "") {
		return 0, emailTaken()
	}
	return id, err
}

// recordUserCreated emits user.created and audits the creation (SEC-M5).
func (s *Service) recordUserCreated(ctx context.Context, tx *gorm.DB, actorID, id int64, email string, roles []string) error {
	if err := s.emit(ctx, tx, "user.created", id, email, StatusActive); err != nil {
		return err
	}
	detail := map[string]any{
		"email": email,
		"roles": roles,
	}
	return auditUser(ctx, tx, actorID, "user.create", id, detail)
}

// insertUser creates a user in segmentID, or the default segment when nil,
// without an owner (core.OwnerGuard gives it one), and returns its id.
func insertUser(db *gorm.DB, email, displayName, passwordHash string, segmentID *int64) *gorm.DB {
	row := store.OneRow(db).Select("?, ?, ?, COALESCE(?, default_segment_id())", email, displayName, passwordHash, segmentID)
	return gormpg.InsertSelectReturning(db, model.User{}.TableName(), []string{"email", "display_name", "password_hash", "segment_id"}, row,
		clause.Returning{Columns: []clause.Column{{Name: "id"}}})
}

// validateOwner refuses an owner_id that is no user id, 0 (none) aside.
func validateOwner(owner *int64) *core.FieldError {
	if owner != nil && *owner < 0 {
		return &core.FieldError{Field: "owner_id", Code: "invalid", Message: "must be a user id, or 0 for none"}
	}
	return nil
}

// lockNamedSegment checks that id, when given, is a named segment and keeps
// the segment from being deleted until the transaction ends, so the user
// about to join it cannot be left pointing at nothing.
func lockNamedSegment(ctx context.Context, tx *gorm.DB, id *int64) error {
	if id == nil {
		return nil
	}
	_, err := segment.LockNamed(ctx, tx, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.InvalidFields(core.FieldError{Field: "segment_id", Code: "invalid", Message: segment.UnknownMessage(ctx)})
	}
	return err
}
