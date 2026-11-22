package segment

import (
	"context"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// makePersonal gives a user a segment of their own, with {"copy": true}
// starting from what the segment they leave has now.
func (s *Service) makePersonal(c *gin.Context) {
	ctx := c.Request.Context()
	uid, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Copy bool `json:"copy"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := s.actOnUsers(ctx, uid); err != nil {
		httpapi.Fail(c, err)
		return
	}
	var id int64
	err := s.tx(ctx, func(tx *gorm.DB) (err error) {
		id, err = createPersonal(ctx, tx, uid, in.Copy)
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.ratesWritten(ctx, in.Copy)
	s.render(c, id, httpapi.Created)
}

// userSegmentKind returns the kind of the segment the user is in; a deleted
// user is not found.
func userSegmentKind(ctx context.Context, q *gorm.DB, userID int64) (string, error) {
	var kind string
	err := store.G(ctx, q).Table("users u").Select("s.kind").Joins("JOIN segments s ON s.id = u.segment_id").
		Where("u.id = ? AND u.deleted_at IS NULL", userID).Take(&kind).Error
	if store.IsNoRows(err) {
		return "", UserNotFound(ctx)
	}
	return kind, err
}

// errAlreadyPersonal is the refusal to make a user personal who is already.
func errAlreadyPersonal(ctx context.Context) error {
	return core.ErrConflict.WithMessage(t(ctx, "the user has a personal user group already", "该用户已是个人专属"))
}

// createPersonal makes the personal segment of a user in a named segment and
// moves the user into it. Two requests for one user are settled by
// segments_user_uq.
func createPersonal(ctx context.Context, tx *gorm.DB, userID int64, copyFrom bool) (int64, error) {
	kind, err := userSegmentKind(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if kind == core.SegmentPersonal {
		return 0, errAlreadyPersonal(ctx)
	}
	id, from, err := insertPersonal(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	res := setUserSegment(store.G(ctx, tx), id, "id = ? AND segment_id = ? AND deleted_at IS NULL", userID, from)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, core.ErrConflict.WithMessage(t(ctx, "the user changed user group meanwhile, try again", "该用户的用户组刚被修改，请重试"))
	}
	if !copyFrom {
		return id, nil
	}
	return id, copySettings(ctx, tx, from, id)
}

// insertPersonal inserts the personal segment of a user in a named segment
// and returns its id and the segment the user is in (its origin). A user
// who is in none, or has one already, is errAlreadyPersonal.
func insertPersonal(ctx context.Context, tx *gorm.DB, userID int64) (id, from int64, err error) {
	// The user is not locked yet: referring to the origin waits for a
	// delete of it, and that delete waits for its members.
	var created struct {
		ID       int64 `gorm:"column:id"`
		OriginID int64 `gorm:"column:origin_id"`
	}
	g := store.G(ctx, tx)
	origin := g.Table("users u").Select("'personal', u.id, s.id").
		Joins("JOIN segments s ON s.id = u.segment_id").Where("u.id = ? AND u.deleted_at IS NULL AND s.kind = 'named'", userID)
	err = gormpg.InsertSelectReturning(g, "segments", []string{"kind", "user_id", "origin_id"}, origin,
		clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "origin_id"}}}).Take(&created).Error
	if store.IsNoRows(err) || store.IsUniqueViolation(err, "segments_user_uq") {
		return 0, 0, errAlreadyPersonal(ctx)
	}
	return created.ID, created.OriginID, err
}

// copySettings gives segment to what segment from has in every group: its
// grants, rate overrides and model rates. The two are unrelated afterwards.
func copySettings(ctx context.Context, tx *gorm.DB, from, to int64) error {
	g := store.G(ctx, tx)
	pairs := g.Model(&model.SegmentGroup{}).Select("?::bigint, group_id, granted, rate_override", to).Where("segment_id = ?", from)
	if err := gormpg.InsertSelect(g, "segment_groups", []string{"segment_id", "group_id", "granted", "rate_override"}, pairs).Error; err != nil {
		return err
	}
	rates := g.Model(&model.SegmentModelRate{}).Select("?::bigint, group_id, model, rate", to).Where("segment_id = ?", from)
	return gormpg.InsertSelect(g, "segment_model_rates", []string{"segment_id", "group_id", "model", "rate"}, rates).Error
}

// removePersonal moves a personal user back to a named segment (?to=, the
// default one without it); the personal segment and its settings are deleted.
func (s *Service) removePersonal(c *gin.Context) {
	ctx := c.Request.Context()
	uid, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	if err := s.actOnUsers(ctx, uid); err != nil {
		httpapi.Fail(c, err)
		return
	}
	err := s.tx(ctx, func(tx *gorm.DB) error { return leavePersonal(ctx, tx, uid, c.Query("to")) })
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

// leavePersonal moves a personal user to the named segment to names
// (moveTarget), deleting the personal segment and its settings; a user who
// is not personal is not found.
func leavePersonal(ctx context.Context, tx *gorm.DB, uid int64, to string) error {
	notPersonal := core.ErrNotFound.WithMessage(t(ctx, "the user has no personal user group", "该用户不是个人专属"))
	kind, err := userSegmentKind(ctx, tx, uid)
	if err != nil {
		return err
	}
	if kind != core.SegmentPersonal {
		return notPersonal
	}
	target, err := moveTarget(ctx, tx, to)
	if err != nil {
		return err
	}
	// The user was not locked when checked: only one still personal moves.
	moved, err := moveUsers(ctx, tx, target, []int64{uid}, core.SegmentPersonal)
	if err == nil && moved == 0 {
		return notPersonal
	}
	return err
}

// moveTarget returns the named segment to names, the default one when to is
// empty, locked against deletion until the transaction ends.
func moveTarget(ctx context.Context, tx *gorm.DB, to string) (int64, error) {
	unknown := core.InvalidFields(core.NewFieldError(ctx, "to", "invalid", "pick a user group", "请选择一个用户组"))
	var want *int64
	if to != "" {
		n, err := strconv.ParseInt(to, 10, 64)
		if err != nil {
			return 0, unknown
		}
		want = &n
	}
	id, err := LockNamed(ctx, tx, want)
	if errors.Is(err, core.ErrNotFound) {
		return 0, unknown
	}
	return id, err
}

// LockNamed returns the named segment id names, the default one when id is
// nil, locked against deletion until the transaction ends, so a user about
// to join it cannot be left pointing at nothing. It is core.ErrNotFound when
// id names no named segment.
func LockNamed(ctx context.Context, tx *gorm.DB, id *int64) (int64, error) {
	if err := store.RequireTx(tx); err != nil {
		return 0, err
	}
	var found int64
	err := lockNamedQuery(store.G(ctx, tx), id).Take(&found).Error
	if store.IsNoRows(err) {
		return 0, core.ErrNotFound
	}
	return found, err
}

// lockNamedQuery selects the id of the named segment id names, the default
// one when id is nil, locked against deletion (LockNamed).
func lockNamedQuery(db *gorm.DB, id *int64) *gorm.DB {
	db = db.Model(&model.Segment{}).Select("id").Where("kind = 'named'")
	if id == nil {
		db = db.Where("is_default")
	} else {
		db = db.Where("id = ?", *id)
	}
	return db.Clauses(store.Lock(store.LockingStrengthKeyShare))
}
