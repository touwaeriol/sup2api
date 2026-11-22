package segment

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The snapshots pin the SQL of the statements this package builds with
// GORM (see package gormtest); rerun with -update after an intended change.

// UserTerms: the user's segment joins by its own parameter, ahead of the
// group ids.
func TestSQLUserTerms(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []termsRow
		return termsQuery(db, []int64{1, 2}, userSegment, int64(7)).Scan(&rows)
	})
}

func TestSQLGroupTerms(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []termsRow
		return termsQuery(db, []int64{1}, `ANY(?)`, store.Int64s{3, 4}).Scan(&rows)
	})
}

// The upsert of a pair: an absent granted (nil) keeps the stored one, the
// rate is the item's.
func TestSQLSavePair(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		rate := decimal.RequireFromString("0.8")
		return savePair(db, pairInput{SegmentID: 3, GroupID: 4, Rate: rateInput{Set: true, Value: &rate}})
	})
}

// A model rate is set by an upsert and removed on null.
func TestSQLSaveModelRate(t *testing.T) {
	rate := decimal.RequireFromString("0.8")
	for name, it := range map[string]modelRateInput{
		"set":    {GroupID: 4, Model: "m", Rate: rateInput{Set: true, Value: &rate}},
		"remove": {GroupID: 4, Model: "m", Rate: rateInput{Set: true}},
	} {
		t.Run(name, func(t *testing.T) {
			gormtest.Assert(t, func(db *gorm.DB) *gorm.DB { return saveModelRate(db, 3, it) })
		})
	}
}

func TestSQLLockMovingUsers(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var ids []int64
		return lockUsersQuery(db, movingUsers, store.Int64s{5, 6}, int64(2), core.SegmentPersonal, core.SegmentPersonal).Pluck("id", &ids)
	})
}

// The moves of users between segments write segment_id alone, as the raw
// SQL did: users.updated_at stays.
func TestSQLSetUserSegment(t *testing.T) {
	for name, build := range map[string]func(*gorm.DB) *gorm.DB{
		// moveUsers
		"move": func(db *gorm.DB) *gorm.DB { return setUserSegment(db, int64(2), "id = ANY(?)", store.Int64s{5, 6}) },
		// createPersonal: only from the segment the user was in.
		"personal": func(db *gorm.DB) *gorm.DB {
			return setUserSegment(db, int64(9), "id = ? AND segment_id = ? AND deleted_at IS NULL", int64(5), int64(2))
		},
		// delete: the members of a deleted segment.
		"default": func(db *gorm.DB) *gorm.DB {
			return setUserSegment(db, gorm.Expr("default_segment_id()"), "id = ANY(?)", store.Int64s{5, 6})
		},
	} {
		t.Run(name, func(t *testing.T) { gormtest.Assert(t, build) })
	}
}

// A PATCH writes the fields present and updated_at = now(), as the raw SQL
// did.
func TestSQLSegmentUpdate(t *testing.T) {
	name := "VIP"
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return segmentUpdate(db, 3, &segmentInput{Name: &name})
	})
}

func TestSQLLockNamed(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var id int64
			return lockNamedQuery(db, nil).Take(&id)
		})
	})
	t.Run("by id", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var id int64
			want := int64(3)
			return lockNamedQuery(db, &want).Take(&id)
		})
	})
}

// GET /segments?kind=personal&q=: users are searched only for callers that
// may read them.
func TestSQLSegmentList(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []segmentRow
		return selectSegments(db).Scopes(listFilter(core.SegmentPersonal, "a_b", true)).
			Order(listOrder).Limit(20).Offset(0).Scan(&rows)
	})
}

// GET /segments?kind=all&q=: both kinds, with no condition on the kind.
func TestSQLSegmentListAll(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []segmentRow
		return selectSegments(db).Scopes(listFilter(kindAll, "a_b", false)).
			Order(listOrder).Limit(20).Offset(0).Scan(&rows)
	})
}

func TestSQLSettingRows(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []settingRow
		return settingQuery(db).Where("s.id = ?", 3).Order("g.id").Scan(&rows)
	})
}
