package segment

import (
	"context"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Deps is what the segment service needs of the rest of the server.
type Deps struct {
	DB *store.DB
	// Authz tells whether the caller may read users (see Masked).
	Authz core.Authorizer
	// Users decides which users the caller may move between segments; nil
	// refuses all.
	Users core.UserGuard
	// Platforms returns the platforms each group serves (group.Service.Platforms).
	Platforms func(ctx context.Context, groupIDs []int64) (map[int64][]string, error)
}

// Service serves the segment endpoints.
type Service struct {
	db        *store.DB
	authz     core.Authorizer
	users     core.UserGuard
	platforms func(ctx context.Context, groupIDs []int64) (map[int64][]string, error)
}

// New builds the segment service.
func New(d Deps) *Service {
	return &Service{db: d.DB, authz: d.Authz, users: core.GuardOrDeny(d.Users), platforms: d.Platforms}
}

// RegisterRoutes mounts the segment endpoints. The members of a segment are
// listed by iam (GET /segments/:id/members): they are user rows.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Perm("GET", "/segments", "segment:read", s.list)
	r.Perm("POST", "/segments", "segment:manage", s.create)
	r.Perm("GET", "/segments/:id", "segment:read", s.get)
	r.Perm("PATCH", "/segments/:id", "segment:manage", s.update)
	r.Perm("DELETE", "/segments/:id", "segment:manage", s.delete)
	r.Perm("POST", "/segments/:id/members", "segment:manage", s.addMembers)
	r.Perm("GET", "/segments/:id/groups", "segment:read", s.segmentGroups)
	r.Perm("PUT", "/segments/:id/groups", "segment:manage", s.setSegmentGroups)
	r.Perm("GET", "/groups/:id/segments", "group:read", s.groupSegments)
	r.Perm("PUT", "/groups/:id/segments", "group:manage", s.setGroupSegments)
	r.Perm("POST", "/users/:id/personal-segment", "segment:manage", s.makePersonal)
	r.Perm("DELETE", "/users/:id/personal-segment", "segment:manage", s.removePersonal)
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }

func fieldError(ctx context.Context, field, code, en, zh string) core.FieldError {
	return core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)}
}

// actOnUsers fails unless the caller may choose the user group of the users:
// it manages every one of them, itself only as a superuser (C-L7, FU6).
func (s *Service) actOnUsers(ctx context.Context, userIDs ...int64) error {
	actor, _ := core.UserID(ctx)
	return s.users.CanActOnUsers(ctx, actor, core.ActOnData, userIDs...)
}

// errOwnSegment is the refusal of a change to the terms of a user group the
// caller is in (guardOwnSegments).
func errOwnSegment(ctx context.Context) *core.Error {
	return core.ErrConflict.WithMessage(t(ctx, "you cannot change the terms of a user group you are in", "你不能修改自己所在用户组的定价或授权"))
}

// guardOwnSegments fails unless the caller may change what the segments
// charge or grant (discount, grants, rates): the caller is a
// member of none of them, or it manages itself (UserGuard on itself: only a
// superuser with the built-in assignable lists, audited there; FU2, FU6).
// It is the one check of every such write; refusals are 409. Other members
// are not targets: these writes are on the segment. The default segment is
// exempt: everyone is in it, so changing it is changing the platform's
// prices, as price:manage and group:manage do, not one's own; dropping the
// is_default condition tightens it. q is the writer's transaction or the
// pool.
func guardOwnSegments(ctx context.Context, q *gorm.DB, users core.UserGuard, segmentIDs []int64) error {
	actor, _ := core.UserID(ctx)
	if actor == 0 || len(segmentIDs) == 0 {
		return nil
	}
	g := store.G(ctx, q)
	member, err := gormpg.Exists(g, g.Table("users u").Joins("JOIN segments s ON s.id = u.segment_id").
		Where("u.id = ? AND s.id = ANY(?) AND NOT s.is_default", actor, store.Int64s(segmentIDs)))
	if err != nil || !member {
		return err
	}
	err = users.CanActOnUsers(ctx, actor, core.ActOnData, actor)
	if err != nil && core.AsError(err).Code == core.ErrPermissionDenied.Code {
		return errOwnSegment(ctx).WithCause(err)
	}
	return err
}

// NotFound is the not_found of a user group that does not exist. It is the
// one wording of it: iam and billing answer with it too. It is
// core.ErrNotFound to errors.Is.
func NotFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "user group not found", "用户组不存在")).WithCause(core.ErrNotFound)
}

// UnknownMessage is the message of a field that names a user group that does not
// exist, or not one it may name.
func UnknownMessage(ctx context.Context) string {
	return t(ctx, "unknown user group", "用户组不存在")
}

// UserNotFound is the not_found of a user that does not exist or was
// deleted. It is core.ErrNotFound to errors.Is.
func UserNotFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "user not found", "用户不存在")).WithCause(core.ErrNotFound)
}

// tx runs fn in a transaction. A row the writes refer to that was deleted
// meanwhile (a foreign key violation) is a conflict, not a failure, and so is
// a deadlock with another writer, should the lock order leave one possible.
func (s *Service) tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	err := s.db.Tx(ctx, fn)
	if store.IsForeignKeyViolation(err) || store.IsDeadlock(err) {
		return core.ErrConflict.WithMessage(t(ctx, "the data changed meanwhile, try again", "数据已被其他操作修改，请重试")).WithCause(err)
	}
	return err
}

// segmentKind returns the kind of a segment and whether it is the default
// one, locking the row with the lock strength ("" = not at all).
func segmentKind(ctx context.Context, q *gorm.DB, id int64, lock string) (kind string, isDefault bool, err error) {
	if lock != "" {
		if err := store.RequireTx(q); err != nil {
			return "", false, err
		}
	}
	db := store.G(ctx, q).Model(&model.Segment{}).Select("kind", "is_default").Where("id = ?", id)
	if lock != "" {
		db = db.Clauses(store.Lock(lock))
	}
	var row model.Segment
	err = db.Take(&row).Error
	if store.IsNoRows(err) {
		return "", false, NotFound(ctx)
	}
	return core.ValueOr(row.Kind, ""), row.IsDefault, err
}
