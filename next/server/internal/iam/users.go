package iam

import (
	"context"
	"database/sql"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

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

// normalizeDisplayName trims a display name; one over 100 characters is a
// field error.
func normalizeDisplayName(name string) (string, *core.FieldError) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 100 {
		return name, &core.FieldError{Field: "display_name", Code: "too_long", Message: "at most 100 characters"}
	}
	return name, nil
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
