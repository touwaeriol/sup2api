package group

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// delete removes a group. Its API keys are only loosely bound to it: with
// ?move_keys_to=<group id> they move to that group, otherwise they stay
// (soft-deleted ones included) with no group and authenticate as
// group_deleted until their owner picks another one.
func (s *Service) delete(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	moveTo, ok := moveKeysTo(c, id)
	if !ok {
		return
	}
	var hashes []string
	err := s.db.Tx(ctx, func(tx *gorm.DB) (err error) {
		hashes, err = s.deleteGroup(ctx, tx, id, moveTo)
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.delKeys(ctx, hashes)
	s.announceAccounts(ctx)
	httpapi.NoContent(c)
}

// moveKeysTo reads ?move_keys_to= of deleting group id (nil = none), or
// renders invalid_argument.
func moveKeysTo(c *gin.Context, id int64) (*int64, bool) {
	v := strings.TrimSpace(c.Query("move_keys_to"))
	if v == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || n == id {
		httpapi.Fail(c, core.InvalidFields(fieldError(c.Request.Context(), "move_keys_to", "invalid",
			"pick another existing group", "请选择另一个已存在的分组")))
		return nil, false
	}
	return &n, true
}

// deleteGroup deletes group id in tx, moving its keys to moveTo (nil = no
// group) and clearing the scoped grants naming it. It returns the hashes of
// the moved keys, whose cached principals are stale once tx commits.
func (s *Service) deleteGroup(ctx context.Context, tx *gorm.DB, id int64, moveTo *int64) ([]string, error) {
	if err := lockForDelete(ctx, tx, id, moveTo); err != nil {
		return nil, err
	}
	hashes, err := moveKeys(ctx, tx, id, moveTo)
	if err != nil {
		return nil, err
	}
	if err := store.G(ctx, tx).Delete(&model.Group{}, id).Error; err != nil {
		return nil, err
	}
	uid, _ := core.UserID(ctx)
	_, err = s.grants.ClearScopeGrants(ctx, tx, uid, core.ScopeRefGroup, []int64{id})
	return hashes, err
}

// lockForDelete locks group id and the move target until tx ends, in id
// order, so two deletes moving keys into each other's group cannot
// deadlock. It fails when either does not exist.
func lockForDelete(ctx context.Context, tx *gorm.DB, id int64, moveTo *int64) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	ids := []int64{id}
	if moveTo != nil {
		ids = append(ids, *moveTo)
	}
	var locked []int64
	if err := store.G(ctx, tx).Model(&model.Group{}).Where("id = ANY(?)", store.Int64s(ids)).Order("id").
		Clauses(store.ForUpdate()).Pluck("id", &locked).Error; err != nil {
		return err
	}
	if !slices.Contains(locked, id) {
		return groupNotFound(ctx)
	}
	if moveTo != nil && !slices.Contains(locked, *moveTo) {
		return core.InvalidFields(fieldError(ctx, "move_keys_to", "not_found", "the target group does not exist", "目标分组不存在"))
	}
	return nil
}

// moveKeys moves every key of group id to moveTo (nil = no group),
// soft-deleted ones included, and returns their hashes.
func moveKeys(ctx context.Context, tx *gorm.DB, id int64, moveTo *int64) ([]string, error) {
	var moved []model.APIKey
	if err := store.G(ctx, tx).Model(&moved).Clauses(clause.Returning{Columns: []clause.Column{{Name: "key_hash"}}}).
		Where("group_id = ?", id).Update("group_id", moveTo).Error; err != nil {
		return nil, err
	}
	hashes := make([]string, len(moved))
	for i, k := range moved {
		hashes[i] = k.KeyHash
	}
	return hashes, nil
}

func (s *Service) announceAccounts(ctx context.Context) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(ctx, core.ChannelAccountChanged, []byte(`{"reason":"group_deleted"}`)); err != nil {
		slog.WarnContext(ctx, "group: publish account:changed", "err", err)
	}
}
