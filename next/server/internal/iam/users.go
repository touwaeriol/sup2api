package iam

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz/casbinrule"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// User statuses.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// User is the API view of a user.
type User struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	Balance     *string    `json:"balance,omitempty"` // only for callers with balance:all:read
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	// OwnerID is the user's owner (CONTRACTS §103.3): the user is in the
	// team of it and of the users above it; nil for none. OwnerEmail is
	// kept when the owner is soft-deleted.
	OwnerID    *int64  `json:"owner_id"`
	OwnerEmail *string `json:"owner_email"`
	// Segment is the user group the user is priced by (CONTRACTS §97). A
	// personal one goes by the user's own email, so there is nothing to mask.
	// It is an administrator's label: the user's own token response omits it.
	Segment core.SegmentRef `json:"segment,omitzero"`
}

// userColumns are what scanUser reads, from usersFrom. They are read by
// position: the segment's id has the name of the user's.
const userColumns = `u.id, u.email, u.display_name, u.status, u.last_login_at, u.created_at, u.updated_at,
    u.owner_id, (SELECT o.email FROM users o WHERE o.id = u.owner_id),
    COALESCE((SELECT array_agg(k ORDER BY k) FROM (` + casbinrule.RolesOfUser + `) ur(k)), '{}'),
    ` + segment.RefColumns + `,
    COALESCE((SELECT b.balance FROM user_balances b WHERE b.user_id = u.id), 0)::numeric(20,8)::text`

const usersFrom = `users u JOIN segments s ON s.id = u.segment_id`

// userByID is the statement of getUser, scanned through Row (the fewest
// allocations): it loads the user of every login and GET /me.
func userByID(db *gorm.DB, id int64) *gorm.DB {
	return usersView(db).Where("u.id = ? AND u.deleted_at IS NULL", id)
}

// usersView selects userColumns, for ListUsers' conditions and page.
func usersView(db *gorm.DB) *gorm.DB {
	return db.Table(usersFrom).Select(userColumns)
}

func scanUser(row store.RowScanner) (*User, error) {
	var u User
	var balance string
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt, &u.OwnerID, &u.OwnerEmail,
		(*store.Strings)(&u.Roles), &u.Segment.ID, &u.Segment.Kind, &u.Segment.Name, &u.Segment.UserID, &balance)
	if err != nil {
		return nil, err
	}
	u.Balance = &balance
	return &u, nil
}

// GetUser returns a non-deleted user or ErrNotFound.
func (s *Service) GetUser(ctx context.Context, id int64) (*User, error) {
	return s.getUser(ctx, s.db.Gorm, id)
}

func (s *Service) getUser(ctx context.Context, q *gorm.DB, id int64) (*User, error) {
	return readUser(userByID(store.G(ctx, q), id))
}

// getUserIn is getUser within scope (of user:read or user:update,
// core.UserRows): a user out of it is not found either.
func (s *Service) getUserIn(ctx context.Context, q *gorm.DB, id int64, scope core.DataScope) (*User, error) {
	return readUser(userInScope(store.G(ctx, q), id, scope))
}

// userInScope is userByID within scope.
func userInScope(db *gorm.DB, id int64, scope core.DataScope) *gorm.DB {
	return userByID(db, id).Scopes(core.UserRows().Scope(scope))
}

// readUser scans the one user db selects (userByID), ErrNotFound for none.
func readUser(db *gorm.DB) (*User, error) {
	u, err := scanUser(db.Row())
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("user not found")
	}
	return u, err
}

// LookupUsers implements core.UserDirectory (HostService.GetUsers).
func (s *Service) LookupUsers(ctx context.Context, ids []int64) ([]core.UserSummary, error) {
	var rows []core.UserSummary
	if err := lookupUsers(store.G(ctx, s.db.Gorm), ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := []core.UserSummary{}
	seen := map[int64]bool{}
	for _, u := range rows {
		if !seen[u.ID] {
			seen[u.ID] = true
			out = append(out, u)
		}
	}
	return out, nil
}

// lookupUsers reads the undeleted users of ids in their order, repeats
// included.
func lookupUsers(db *gorm.DB, ids []int64) *gorm.DB {
	return db.Table("unnest(?::bigint[]) WITH ORDINALITY AS q(id, n)", store.Int64s(ids)).Select("u.id, u.display_name, u.status").
		Joins("JOIN users u ON u.id = q.id AND u.deleted_at IS NULL").Order("q.n")
}

// ListUsersFilter filters GET /users.
type ListUsersFilter struct {
	Query   string // matches email or display name
	Status  string
	Role    string // role key
	Segment string // a segment id, or "personal" for the users with a personal segment
	// Owner keeps the users this user owns directly, those without an
	// owner when 0; nil keeps all.
	Owner *int64
	// Direct, when not 0, keeps the users it (the caller) owns directly.
	Direct int64
	// Scope is the caller's range of user:read (core.UserRows).
	Scope    core.DataScope
	Page     int
	PageSize int
}

// ListUsers returns a page of non-deleted users in f.Scope, ordered by id.
func (s *Service) ListUsers(ctx context.Context, f ListUsersFilter) ([]*User, int64, error) {
	filter := usersFilter(f)
	g := store.G(ctx, s.db.Gorm)
	var total int64
	if err := usersCount(g, filter).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	out, err := store.CollectRows(usersPage(g, filter, f.Page, f.PageSize), func(rows *sql.Rows) (*User, error) { return scanUser(rows) })
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// usersCount is what ListUsers counts: the users u that filter keeps.
func usersCount(db *gorm.DB, filter store.Conds) *gorm.DB {
	return db.Table("users u").Scopes(filter.Scope())
}

// usersFilter are the conditions of ListUsers on the users u.
func usersFilter(f ListUsersFilter) store.Conds {
	filter := store.Conds{core.UserRows().Scope(f.Scope)}
	filter.And("u.deleted_at IS NULL")
	if q := strings.TrimSpace(f.Query); q != "" {
		pattern := "%" + store.Like(q) + "%"
		filter.And(`(u.email ILIKE ? OR u.display_name ILIKE ?)`, pattern, pattern)
	}
	if f.Status != "" {
		filter.And("u.status = ?", f.Status)
	}
	if f.Role != "" {
		filter.And(`? IN (`+casbinrule.RolesOfUser+`)`, f.Role)
	}
	switch {
	case f.Segment == core.SegmentPersonal:
		filter.And(`u.segment_id IN (SELECT id FROM segments WHERE kind = ?)`, core.SegmentPersonal)
	case f.Segment != "":
		// A value that is not a segment id selects nobody.
		id, _ := strconv.ParseInt(f.Segment, 10, 64)
		filter.And("u.segment_id = ?", id)
	}
	switch {
	case f.Owner != nil && *f.Owner == 0:
		filter.And("u.owner_id IS NULL")
	case f.Owner != nil:
		filter.And("u.owner_id = ?", *f.Owner)
	}
	if f.Direct != 0 {
		filter.And("u.owner_id = ?", f.Direct)
	}
	return filter
}

// usersPage is the page of ListUsers, page and size kept within
// httpapi.Pagination's bounds: GORM would leave out a size of 0 or less
// and a negative offset, and read every user.
func usersPage(db *gorm.DB, filter store.Conds, page, size int) *gorm.DB {
	page, size = httpapi.PageBounds(page, size)
	return usersView(db).Scopes(filter.Scope()).Order("u.id").Limit(size).Offset((page - 1) * size)
}

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
	email, fields := normalizeEmail(in.Email)
	if f := validatePassword("password", in.Password); f != nil {
		fields = append(fields, *f)
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(in.DisplayName) > 100 {
		fields = append(fields, core.FieldError{Field: "display_name", Code: "too_long", Message: "at most 100 characters"})
	}
	if f := validateOwner(in.OwnerID); f != nil {
		fields = append(fields, *f)
	}
	if len(fields) > 0 {
		return nil, 0, core.InvalidFields(fields...)
	}
	if err := lockNamedSegment(ctx, tx, in.SegmentID); err != nil {
		return nil, 0, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cost)
	if err != nil {
		return nil, 0, err
	}
	var id int64
	err = insertUser(store.G(ctx, tx), email, in.DisplayName, string(hash), in.SegmentID).Row().Scan(&id)
	switch {
	case store.IsUniqueViolation(err, ""):
		return nil, 0, emailTaken()
	case err != nil:
		return nil, 0, err
	}
	// The one place the owner is decided and written (CONTRACTS §103.3);
	// whether the actor may name it comes before anything about it is read.
	if _, err := s.owners.SetOwner(ctx, tx, actorID, id, true, in.OwnerID); err != nil {
		return nil, 0, err
	}
	roles := in.RoleKeys
	if len(roles) == 0 {
		roles = []string{authz.RoleUser}
	}
	// SetUserRoles checks the actor may grant the roles (SEC-H1) and act on
	// the user, under its owner, and that the owner manages the user by
	// them (owner_cannot_manage).
	v, err := s.authz.SetUserRoles(ctx, tx, actorID, id, roles)
	if err != nil {
		return nil, 0, err
	}
	if err := s.emit(ctx, tx, "user.created", id, email, StatusActive); err != nil {
		return nil, 0, err
	}

	// SEC-M5: audit user creation
	detail := map[string]any{
		"email": email,
		"roles": roles,
	}
	if err := audit.Audit(ctx, tx, actorID, "user.create", "user", strconv.FormatInt(id, 10), detail); err != nil {
		return nil, 0, err
	}

	u, err := s.getUser(ctx, tx, id)
	return u, v, err
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
		*in.DisplayName = strings.TrimSpace(*in.DisplayName)
		if utf8.RuneCountInString(*in.DisplayName) > 100 {
			fields = append(fields, core.FieldError{Field: "display_name", Code: "too_long", Message: "at most 100 characters"})
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
	// Before any other check, so that they tell nothing of a user out of
	// scope; checked again under the lock.
	if _, err := s.getUserIn(ctx, s.db.Gorm, id, scope); err != nil {
		return nil, err
	}
	// SEC-H3: a user's own email or password changes only with a step-up
	// proof (the password one: the password and a second factor), as on
	// /me/password; this route must not be the way around it for an access
	// key or a stolen token. Spent before the guard, which then lets the
	// super admin's own access key through on it.
	if actorID == id {
		if err := s.consumeSelfChange(ctx, in); err != nil {
			return nil, err
		}
	}
	// SEC-H1, FU6: the target, oneself included, must be a user the actor
	// manages (its roles within the actor's and assignable by it).
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
		return nil, err
	}

	// SEC-H1: password reset for others requires user:password:reset and a
	// step-up proof; the system (actor 0, the server CLI) holds no roles and
	// is not checked.
	if in.Password != nil && actorID != id && actorID != 0 {
		reset, err := core.Can(core.WithUserID(ctx, actorID), s.authz, "user:password:reset")
		if err != nil {
			return nil, err
		}
		if !reset {
			return nil, core.ErrPermissionDenied.WithMessage("missing permission: user:password:reset").
				WithDetails(map[string]any{"permission": "user:password:reset"})
		}
		if err := s.ConsumeStepUp(ctx, core.StepUpUserPasswordReset); err != nil {
			return nil, err
		}
	}

	var hash []byte
	if in.Password != nil {
		var err error
		if hash, err = bcrypt.GenerateFromPassword([]byte(*in.Password), s.cost); err != nil {
			return nil, err
		}
	}
	var v int64
	var u *User
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
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

// applyUpdateTx performs the locked read, authorization checks and the write
// inside the caller's transaction.
func (s *Service) applyUpdateTx(ctx context.Context, tx *gorm.DB, actorID, id int64, scope core.DataScope, in UpdateUserInput, email string, hash []byte) (int64, *User, error) {
	var v int64
	// Keep the established authz -> user lock order. Even an apparently
	// unchanged status must invalidate authorization after concurrent edits.
	if in.Status != nil {
		var err error
		if v, err = s.authz.Bump(ctx, tx, id); err != nil {
			return 0, nil, err
		}
	}
	// A new owner moves the user between teams: SetOwner checks and writes
	// it under the authz lock, before the user's row is locked.
	if in.OwnerID != nil {
		if _, err := s.owners.SetOwner(ctx, tx, actorID, id, false, in.OwnerID); err != nil {
			return 0, nil, err
		}
	}
	if err := lockUserRow(ctx, tx, id); err != nil {
		if err == errNoUser {
			return 0, nil, core.ErrNotFound.WithMessage("user not found")
		}
		return 0, nil, err
	}
	cur, err := s.getUserIn(ctx, tx, id, scope)
	if err != nil {
		return 0, nil, err
	}
	statusChange := in.Status != nil && *in.Status != cur.Status
	if statusChange {
		if actorID == id {
			return 0, nil, errSelfDisable
		}
		// SEC-H2: target permissions must not exceed actor's - in both
		// directions. Re-enabling a disabled account hands its holder every
		// permission its roles grant, so it is as sensitive as disabling.
		// Checked again after Bump, which holds role changes off until commit.
		if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
			return 0, nil, err
		}
		if *in.Status == StatusDisabled {
			if err := s.authz.EnsureNotLastSuperAdmin(ctx, tx, id); err != nil {
				return 0, nil, err
			}
		}
	}

	err = updateUser(store.G(ctx, tx), id, userChanges(in, email, hash)).Error
	if store.IsUniqueViolation(err, "") {
		return 0, nil, emailTaken()
	}
	if err != nil {
		return 0, nil, err
	}
	// SEC-M1: a new password or disabling ends every session
	if hash != nil || (statusChange && *in.Status == StatusDisabled) {
		if err := s.endAllSessions(ctx, tx, id); err != nil {
			return 0, nil, err
		}
	}
	u, err := s.getUser(ctx, tx, id)
	if err != nil {
		return 0, nil, err
	}
	if err := s.emit(ctx, tx, "user.updated", id, u.Email, u.Status); err != nil {
		return 0, nil, err
	}

	// SEC-M5: audit user changes
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
	if err := audit.Audit(ctx, tx, actorID, "user.update", "user", strconv.FormatInt(id, 10), detail); err != nil {
		return 0, nil, err
	}
	return v, u, nil
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
		if v, err = s.authz.Bump(ctx, tx, id); err != nil {
			return err
		}
		// Checked again after Bump, which holds role changes off until
		// commit: the target may have been given a role the actor does not
		// manage since the check above (as applyUpdateTx does).
		if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, id); err != nil {
			return err
		}
		g := store.G(ctx, tx)
		var email string
		err = lockLiveUser(g, id, "email").Row().Scan(&email)
		if store.IsNoRows(err) {
			return core.ErrNotFound.WithMessage("user not found")
		}
		if err != nil {
			return err
		}
		if err := s.authz.EnsureNotLastSuperAdmin(ctx, tx, id); err != nil {
			return err
		}
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

		// SEC-M5: audit user deletion
		detail := map[string]any{"email": email}
		return audit.Audit(ctx, tx, actorID, "user.delete", "user", strconv.FormatInt(id, 10), detail)
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(ctx, id)
	s.authz.Committed(ctx, v)
	return nil
}

// userDeletion soft-deletes a user and moves it back to the default segment.
func userDeletion() map[string]any {
	return map[string]any{"deleted_at": gorm.Expr("now()"), "updated_at": gorm.Expr("now()"), "segment_id": gorm.Expr("default_segment_id()")}
}

// deleteUserKeys soft-deletes the live API keys of userID.
func deleteUserKeys(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.APIKey{}).Where("user_id = ? AND deleted_at IS NULL", userID).Update("deleted_at", gorm.Expr("now()"))
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

// ------------------------------------------------------------ helpers

func (s *Service) emit(ctx context.Context, tx *gorm.DB, typ string, userID int64, email, status string) error {
	if s.events == nil {
		return nil
	}
	return s.events.Emit(ctx, tx, core.Event{Type: typ, Payload: map[string]any{
		"user_id": userID, "email": email, "status": status,
	}})
}

func normalizeEmail(raw string) (string, []core.FieldError) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 255 {
		return email, []core.FieldError{{Field: "email", Code: "invalid", Message: "invalid email address"}}
	}
	return email, nil
}

func validatePassword(field, p string) *core.FieldError {
	switch {
	case utf8.RuneCountInString(p) < 8:
		return &core.FieldError{Field: field, Code: "too_short", Message: "at least 8 characters"}
	case len(p) > 72:
		return &core.FieldError{Field: field, Code: "too_long", Message: "at most 72 bytes"}
	}
	return nil
}

func emailTaken() error {
	return core.ErrConflict.WithMessage("email already in use").WithDetails(map[string]any{
		"fields": []core.FieldError{{Field: "email", Code: "taken", Message: "email already in use"}},
	})
}

// lockUserRow locks the row of userID, deleted or not, until tx ends: the
// parent lock of every session change and second-factor write of the user.
// errNoUser when there is no such row.
func lockUserRow(ctx context.Context, tx *gorm.DB, userID int64) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	var id int64
	err := lockUser(store.G(ctx, tx), userID).Row().Scan(&id)
	if store.IsNoRows(err) {
		return errNoUser
	}
	return err
}

// lockUser locks the row of userID, deleted or not, until the transaction
// ends.
func lockUser(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.User{}).Select("id").Where("id = ?", userID).Clauses(store.ForUpdate())
}

// liveUser selects cols of the undeleted user id.
func liveUser(db *gorm.DB, id int64, cols string) *gorm.DB {
	return liveUsers(db).Select(cols).Where("id = ?", id)
}

// lockLiveUser is liveUser, the row locked until the transaction ends.
func lockLiveUser(db *gorm.DB, id int64, cols string) *gorm.DB {
	return liveUser(db, id, cols).Clauses(store.ForUpdate())
}

// userByEmail selects cols of the undeleted user with email, in any case.
func userByEmail(db *gorm.DB, email, cols string) *gorm.DB {
	return liveUsers(db).Select(cols).Where("lower(email) = lower(?)", email)
}

// touchLastLogin records a login of userID.
func touchLastLogin(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.User{}).Where("id = ?", userID).Update("last_login_at", gorm.Expr("now()"))
}

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilBytes(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}
