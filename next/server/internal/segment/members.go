package segment

import (
	"context"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// addMembers moves users into a named segment and answers how many moved.
func (s *Service) addMembers(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := s.actOnUsers(ctx, in.UserIDs...); err != nil {
		httpapi.Fail(c, err)
		return
	}
	var moved int
	err := s.tx(ctx, func(tx *gorm.DB) (err error) {
		moved, err = joinNamed(ctx, tx, id, in.UserIDs)
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"moved": moved})
}

// joinNamed moves the users into the named segment id (moveUsers) and
// returns how many moved. A personal segment takes no one, and an id that is
// no user fails the whole move.
func joinNamed(ctx context.Context, tx *gorm.DB, id int64, userIDs []int64) (int, error) {
	// The lock keeps the segment from being deleted under the move.
	kind, _, err := segmentKind(ctx, tx, id, store.LockingStrengthKeyShare)
	if err != nil {
		return 0, err
	}
	if kind == core.SegmentPersonal {
		return 0, core.ErrConflict.WithMessage(t(ctx, "a personal user group has one member", "个人专属只有一个成员"))
	}
	if err := requireUsers(ctx, tx, userIDs); err != nil {
		return 0, err
	}
	return moveUsers(ctx, tx, id, userIDs, "")
}

// requireUsers fails with the field error of the first id that is not a
// user; a deleted user is none.
func requireUsers(ctx context.Context, q *gorm.DB, ids []int64) error {
	var live []int64
	if err := store.G(ctx, q).Model(&model.User{}).Where("id = ANY(?) AND deleted_at IS NULL", store.Int64s(ids)).
		Pluck("id", &live).Error; err != nil {
		return err
	}
	for _, unknown := range ids {
		if !slices.Contains(live, unknown) {
			id := strconv.FormatInt(unknown, 10)
			return core.InvalidFields(fieldError(ctx, "user_ids", "not_found", "unknown user "+id, "用户 "+id+" 不存在"))
		}
	}
	return nil
}

// moveUsers puts the users into the named segment to and deletes the
// personal segment of each one who had one, with its settings. With from a
// segment kind, only users in a segment of that kind move. It returns how
// many moved (a user already there does not).
func moveUsers(ctx context.Context, tx *gorm.DB, to int64, userIDs []int64, from string) (int, error) {
	ids, err := lockUsers(ctx, tx, movingUsers, store.Int64s(userIDs), to, from, from)
	if err != nil {
		return 0, err
	}
	if err := setUserSegment(store.G(ctx, tx), to, "id = ANY(?)", store.Int64s(ids)).Error; err != nil {
		return 0, err
	}
	return len(ids), DropPersonal(ctx, tx, ids...)
}

// movingUsers picks the users of ids (the first parameter) that are not
// deleted and not in the segment to (the second), only those in a segment of
// the kind from (the last two, "" = any).
const movingUsers = `id = ANY(?) AND deleted_at IS NULL AND segment_id <> ?
	AND (? = '' OR segment_id IN (SELECT id FROM segments WHERE kind = ?))`

// lockUsers locks the users cond picks, in id order, until the transaction
// ends, and returns their ids. Every writer of users.segment_id locks first
// and updates in a statement of its own: an UPDATE in the statement that
// locks starts from the row versions of that statement's snapshot and, where
// another writer replaced one meanwhile, queues on it behind the key-share
// holders (inserts that refer to the user), which can close a cycle.
func lockUsers(ctx context.Context, tx *gorm.DB, cond string, args ...any) ([]int64, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var ids []int64
	err := lockUsersQuery(store.G(ctx, tx), cond, args...).Pluck("id", &ids).Error
	return ids, err
}

// lockUsersQuery selects the users cond picks, locked in id order (lockUsers).
func lockUsersQuery(db *gorm.DB, cond string, args ...any) *gorm.DB {
	return db.Model(&model.User{}).Where(cond, args...).Order("id").Clauses(store.Lock(store.LockingStrengthNoKeyUpdate))
}

// setUserSegment puts the users cond picks into segment (an id, or an
// expression such as default_segment_id()). Only segment_id is written:
// updated_at stays, as in the raw SQL each caller replaced.
func setUserSegment(db *gorm.DB, segment any, cond string, args ...any) *gorm.DB {
	return db.Model(&model.User{}).Where(cond, args...).Update("segment_id", segment)
}

// DropPersonal deletes the personal segments of the users, who have left
// them, with their grants and rates.
func DropPersonal(ctx context.Context, tx *gorm.DB, userIDs ...int64) error {
	return store.G(ctx, tx).Where("kind = 'personal' AND user_id = ANY(?)", store.Int64s(userIDs)).Delete(&model.Segment{}).Error
}
