package segment

import (
	"context"
	"strconv"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// accessFixture is a user in segment "vip" and three groups: a public one,
// a private one granted to vip at its own rate, and a private one vip was
// not granted.
type accessFixture struct {
	db                          *store.DB
	user, other, vip            int64
	public, granted, restricted int64
}

func newAccessFixture(t *testing.T) accessFixture {
	t.Helper()
	ctx := context.Background()
	f := accessFixture{db: testutil.DB(t)}
	one := func(dst *int64, sql string, args ...any) {
		t.Helper()
		require.NoError(t, f.db.SQL.QueryRowContext(ctx, sql, args...).Scan(dst))
	}
	one(&f.vip, `INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	one(&f.user, `INSERT INTO users (email, password_hash, segment_id) VALUES ('vip@test', 'x', $1) RETURNING id`, f.vip)
	one(&f.other, `INSERT INTO users (email, password_hash) VALUES ('default@test', 'x') RETURNING id`)
	one(&f.public, `INSERT INTO groups (name, rate_multiplier) VALUES ('public', 1.5) RETURNING id`)
	one(&f.granted, `INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('granted', 'restricted', 2) RETURNING id`)
	one(&f.restricted, `INSERT INTO groups (name, visibility) VALUES ('restricted', 'restricted') RETURNING id`)
	_, err := f.db.SQL.ExecContext(ctx, `INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, true, 0.5)`, f.vip, f.granted)
	require.NoError(t, err)
	return f
}

// groups returns the ids of the groups cond (with ? for user) holds for.
func (f accessFixture) groups(t *testing.T, cond string, user int64) []int64 {
	t.Helper()
	var ids []int64
	require.NoError(t, f.db.Gorm.Table("groups g").Where(cond, user).Order("g.id").Pluck("g.id", &ids).Error)
	return ids
}

func TestOpenToUser(t *testing.T) {
	f := newAccessFixture(t)
	require.Equal(t, []int64{f.public, f.granted}, f.groups(t, OpenToUser, f.user))
	require.Equal(t, []int64{f.public}, f.groups(t, OpenToUser, f.other))
}

// A disabled group is open to its users still, but not usable.
func TestUsableGroup(t *testing.T) {
	f := newAccessFixture(t)
	_, err := f.db.SQL.ExecContext(context.Background(), `UPDATE groups SET status = 'disabled' WHERE id = $1`, f.public)
	require.NoError(t, err)
	require.Equal(t, []int64{f.granted}, f.groups(t, UsableGroup, f.user))
	require.Empty(t, f.groups(t, UsableGroup, f.other))
	require.Equal(t, []int64{f.public, f.granted}, f.groups(t, OpenToUser, f.user))
}

// termsRate is the rate and its source the terms give a model with no rate
// of its own.
func termsRate(tm Terms) string {
	r := RateOf(tm.Levels())
	return r.Value.String() + " " + r.Source
}

func TestUserTerms(t *testing.T) {
	f := newAccessFixture(t)
	got, err := UserTerms(context.Background(), f.db.Gorm, f.user, []int64{f.public, f.granted, f.restricted, 0})
	require.NoError(t, err)
	require.Len(t, got, 3, "a group that does not exist has no terms")
	for _, tc := range []struct {
		name  string
		group int64
		open  bool
		rate  string
	}{
		{"public group at its base rate", f.public, true, "1.5 group"},
		{"granted group at the override", f.granted, true, "0.5 segment"},
		{"private group not granted", f.restricted, false, "1 group"},
	} {
		terms := got[tc.group]
		require.Equal(t, f.vip, terms.SegmentID, tc.name)
		require.Equal(t, tc.open, terms.Open, tc.name)
		require.Equal(t, tc.rate, termsRate(terms), tc.name)
	}
	require.Equal(t, "0.5", got[f.granted].Override.String())
	require.Nil(t, got[f.public].Override)

	other, err := UserTerms(context.Background(), f.db.Gorm, f.other, []int64{f.granted})
	require.NoError(t, err)
	require.Equal(t, "2 group", termsRate(other[f.granted]))
	require.False(t, other[f.granted].Open)
}

// SegmentTerms and GroupTerms are the same query as UserTerms.
func TestSegmentAndGroupTerms(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	groups := []int64{f.public, f.granted, f.restricted, 0}
	got, err := UserTerms(ctx, f.db.Gorm, f.user, groups)
	require.NoError(t, err)
	bySegment, err := SegmentTerms(ctx, f.db.Gorm, f.vip, groups)
	require.NoError(t, err)
	require.Equal(t, got, bySegment)

	var def int64
	require.NoError(t, f.db.SQL.QueryRowContext(ctx, `SELECT default_segment_id()`).Scan(&def))
	byGroup, err := GroupTerms(ctx, f.db.Gorm, f.granted, []int64{f.vip, def, 0})
	require.NoError(t, err)
	require.Len(t, byGroup, 2, "a segment that does not exist has no terms")
	require.Equal(t, got[f.granted], byGroup[f.vip])
	require.Equal(t, "2 group", termsRate(byGroup[def]))
	require.False(t, byGroup[def].Open)
}

// The console's rate of levels with model rates is the most specific one.
func TestRateOf(t *testing.T) {
	d := func(s string) *decimal.Decimal { v := decimal.RequireFromString(s); return &v }
	levels := core.RateLevels{Segment: d("0.5"), GroupModel: d("0.9"), Group: *d("2")}
	require.Equal(t, "0.5 segment 2", rateText(RateOf(levels)))
	levels.SegmentModel = d("0.25")
	require.Equal(t, "0.25 segment_model 2", rateText(RateOf(levels)))
	require.Equal(t, "0.9 group_model 2", rateText(RateOf(core.RateLevels{GroupModel: d("0.9"), Group: *d("2")})))
}

func rateText(r EffectiveRate) string {
	return r.Value.String() + " " + r.Source + " " + r.GroupDefault.String()
}

func TestRef(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	var personal int64
	require.NoError(t, f.db.SQL.QueryRowContext(ctx, `INSERT INTO segments (kind, user_id) VALUES ('personal', $1) RETURNING id`, f.other).Scan(&personal))

	named, err := Ref(ctx, f.db.Gorm, f.vip, false)
	require.NoError(t, err)
	require.Equal(t, core.SegmentRef{ID: f.vip, Kind: core.SegmentNamed, Name: "vip"}, named)

	shown, err := Ref(ctx, f.db.Gorm, personal, true)
	require.NoError(t, err)
	require.Equal(t, core.SegmentRef{ID: personal, Kind: core.SegmentPersonal, Name: "default@test", UserID: &f.other}, shown)

	masked, err := Ref(ctx, f.db.Gorm, personal, false)
	require.NoError(t, err)
	require.Equal(t, "#"+strconv.FormatInt(f.other, 10), masked.Name)
	require.Equal(t, &f.other, masked.UserID)

	_, err = Ref(ctx, f.db.Gorm, 0, true)
	require.ErrorIs(t, err, core.ErrNotFound)
}
