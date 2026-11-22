// Package segment owns user groups ("segments", CONTRACTS §97): which private
// groups their members may use and at what rate. Segments set no model
// prices: those are a group's (CONTRACTS §97.10).
package segment

import (
	"context"
	"strconv"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// openTo is the SQL condition for "the user may use the group g": the
// group is public, or the user's segment is granted it, the user's id
// being the expression that closes it (openTo, then the id, then "))").
// It is the one place the rule is written; the group's status is the
// caller's to check.
const openTo = `(g.visibility = 'public' OR EXISTS (SELECT 1 FROM segment_groups sg
		JOIN users su ON su.segment_id = sg.segment_id
		WHERE sg.group_id = g.id AND sg.granted AND su.id = `

const (
	// OpenToUser is openTo of the user given as the one ? parameter, the
	// groups row being aliased g.
	OpenToUser = openTo + `?))`
	// OpenToKeyOwner is openTo of the owner of the api_keys row k.
	OpenToKeyOwner = openTo + `k.user_id))`
	// UsableGroup is the SQL condition for the groups g the user (the one ?
	// parameter) can use (GET /me/groups): active, and OpenToUser. It is
	// also the range of groups an "own" account holder may bind.
	UsableGroup = `g.status = 'active' AND ` + OpenToUser
)

// Terms is what a segment gets in a group, the model aside.
type Terms struct {
	SegmentID int64
	// Granted is the explicit grant; Open reports whether members may use
	// the group: it is public or granted.
	Granted, Open bool
	// GroupRate is the group's base rate and Override the segment's own
	// rate in the group (nil = none).
	GroupRate decimal.Decimal
	Override  *decimal.Decimal
}

// Levels are the rate levels of the terms; the model levels are the
// caller's to add. RateOf(Levels()) is what members pay there for a model
// with no rate of its own.
func (t Terms) Levels() core.RateLevels {
	return core.RateLevels{Segment: t.Override, Group: t.GroupRate}
}

// EffectiveRate is a rate with where it comes from, as the console shows it.
type EffectiveRate struct {
	Value        decimal.Decimal `json:"value"`
	Source       string          `json:"source"`
	GroupDefault decimal.Decimal `json:"group_default"`
}

// RateOf returns the rate levels pick (core.RateLevels.Rate) for the
// console. It is the one way to it.
func RateOf(l core.RateLevels) EffectiveRate {
	value, source := l.Rate()
	return EffectiveRate{Value: value, Source: source, GroupDefault: l.Group}
}

// termsColumns are the columns of a termsRow.
const termsColumns = `g.id AS group_id, s.id AS segment_id, COALESCE(sg.granted, false) AS granted, g.visibility = 'public' AS public,
	g.rate_multiplier, sg.rate_override`

// termsRow is what a segment gets in a group, as stored.
type termsRow struct {
	GroupID      int64            `gorm:"column:group_id"`
	SegmentID    int64            `gorm:"column:segment_id"`
	Granted      bool             `gorm:"column:granted"`
	Public       bool             `gorm:"column:public"`
	GroupRate    decimal.Decimal  `gorm:"column:rate_multiplier"`
	RateOverride *decimal.Decimal `gorm:"column:rate_override"`
}

// terms returns the row as Terms.
func (r termsRow) terms() Terms {
	return Terms{SegmentID: r.SegmentID, Granted: r.Granted, Open: r.Public || r.Granted,
		GroupRate: r.GroupRate, Override: r.RateOverride}
}

// UserTerms returns the terms of the user's segment in each of the groups
// that exist, by group id. A deleted user has none.
func UserTerms(ctx context.Context, q *gorm.DB, userID int64, groupIDs []int64) (map[int64]Terms, error) {
	return queryTerms(ctx, q, byGroup, groupIDs, userSegment, userID)
}

// userSegment is the segment of a user (the parameter) who is not deleted.
const userSegment = `(SELECT u.segment_id FROM users u WHERE u.id = ? AND u.deleted_at IS NULL)`

// SegmentTerms returns the terms of the segment in each of the groups that
// exist, by group id.
func SegmentTerms(ctx context.Context, q *gorm.DB, segmentID int64, groupIDs []int64) (map[int64]Terms, error) {
	return queryTerms(ctx, q, byGroup, groupIDs, `?`, segmentID)
}

// GroupTerms returns the terms each of the segments that exist gets in the
// group, by segment id.
func GroupTerms(ctx context.Context, q *gorm.DB, groupID int64, segmentIDs []int64) (map[int64]Terms, error) {
	return queryTerms(ctx, q, bySegment, []int64{groupID}, `ANY(?)`, store.Int64s(segmentIDs))
}

func byGroup(groupID int64, _ Terms) int64 { return groupID }

func bySegment(_ int64, t Terms) int64 { return t.SegmentID }

// termsQuery reads the termsRows of the segments s.id = segment picks (an
// expression of arg) in the groups of groupIDs.
func termsQuery(db *gorm.DB, groupIDs []int64, segment string, arg any) *gorm.DB {
	return db.Table("groups g").Select(termsColumns).
		Joins("JOIN segments s ON s.id = "+segment, arg).
		Joins("LEFT JOIN segment_groups sg ON sg.segment_id = s.id AND sg.group_id = g.id").
		Where("g.id = ANY(?)", store.Int64s(groupIDs))
}

// queryTerms runs a termsQuery and files each row under what key picks.
func queryTerms(ctx context.Context, q *gorm.DB, key func(groupID int64, t Terms) int64, groupIDs []int64, segment string, arg any) (map[int64]Terms, error) {
	var rows []termsRow
	if err := termsQuery(store.G(ctx, q), groupIDs, segment, arg).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]Terms, len(rows))
	for _, r := range rows {
		t := r.terms()
		out[key(r.GroupID, t)] = t
	}
	return out, nil
}

// ShowUsers reports whether the caller may read users: only then does a
// personal segment go by its user's email (Masked). Without an authorizer
// nobody may.
func ShowUsers(ctx context.Context, authz core.Authorizer) (bool, error) {
	if authz == nil {
		return false, nil
	}
	uid, _ := core.UserID(ctx)
	return authz.Can(ctx, uid, "user:read")
}

// RefColumns are the columns of a core.SegmentRef (id, kind, name, user id)
// of the segments row s; u is the users row of the user of a personal
// segment, its only member. They are named as the fields they scan into. It
// is the one place a personal segment is named by its user's email.
const RefColumns = `s.id, s.kind, CASE WHEN s.user_id IS NULL THEN s.name ELSE u.email END AS name, s.user_id`

// Ref names a segment. A personal segment goes by its user's email; callers
// that may not read users (showUsers false) get "#<user id>" instead, so a
// role that only reads groups or prices cannot list users through it. A
// segment that does not exist is NotFound.
func Ref(ctx context.Context, q *gorm.DB, id int64, showUsers bool) (core.SegmentRef, error) {
	var ref core.SegmentRef
	err := fromSegments(store.G(ctx, q)).Select(RefColumns).Where("s.id = ?", id).Take(&ref).Error
	if store.IsNoRows(err) {
		return core.SegmentRef{ID: id}, NotFound(ctx)
	}
	if err != nil {
		return core.SegmentRef{ID: id}, err
	}
	return Masked(ref, showUsers), nil
}

// Masked returns ref as a caller may see it: the name of a personal segment
// is its user's email only for callers that may read users.
func Masked(ref core.SegmentRef, showUsers bool) core.SegmentRef {
	if showUsers || ref.UserID == nil {
		return ref
	}
	ref.Name = "#" + strconv.FormatInt(*ref.UserID, 10)
	return ref
}
