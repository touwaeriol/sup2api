package segment

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Setting is what one segment has in one group.
type Setting struct {
	Segment core.SegmentRef `json:"segment"`
	Group   SettingGroup    `json:"group"`
	// Accessible: the group is public or granted. Granted is the explicit
	// grant; it only matters for a private group.
	Accessible bool `json:"accessible"`
	Granted    bool `json:"granted"`
	// RateOverride is the segment's own rate in the group; null = it
	// follows the group. Rate is what its members pay there.
	RateOverride *decimal.Decimal `json:"rate_override"`
	Rate         EffectiveRate    `json:"rate"`
	MemberCount  int64            `json:"member_count"`
}

// SettingGroup is the group of a Setting.
type SettingGroup struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	Visibility     string          `json:"visibility"`
	RateMultiplier decimal.Decimal `json:"rate_multiplier"`
	Platforms      []string        `json:"platforms"`
}

func (s *Service) segmentGroups(c *gin.Context)    { s.serveSettings(c, s.ofSegment, false) }
func (s *Service) setSegmentGroups(c *gin.Context) { s.serveSettings(c, s.ofSegment, true) }
func (s *Service) groupSegments(c *gin.Context)    { s.serveSettings(c, s.ofGroup, false) }
func (s *Service) setGroupSegments(c *gin.Context) { s.serveSettings(c, s.ofGroup, true) }

// settingsOf saves the items as settings of the row the path names (a
// segment or a group) and returns the settings of that row.
type settingsOf func(c *gin.Context, id int64, items []pairInput) ([]*Setting, error)

// serveSettings answers with the settings of the row the path names, after
// saving the items of the body when write is set.
func (s *Service) serveSettings(c *gin.Context, of settingsOf, write bool) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Items []pairInput `json:"items"`
	}
	if write && !httpapi.BindJSON(c, &in) {
		return
	}
	rows, err := of(c, id, in.Items)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, rows)
}

// ofSegment saves the items as settings of the segment and returns what it
// has in every group.
func (s *Service) ofSegment(c *gin.Context, id int64, items []pairInput) ([]*Setting, error) {
	ctx := c.Request.Context()
	if _, _, err := segmentKind(ctx, s.db.Gorm, id, ""); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].SegmentID = id
	}
	if err := s.savePairs(ctx, items); err != nil {
		return nil, err
	}
	rows, err := s.settingRows(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("s.id = ?", id).Order("g.id") })
	if err != nil {
		return nil, err
	}
	terms, err := SegmentTerms(ctx, s.db.Gorm, id, idsOf(rows, groupID))
	if err != nil {
		return nil, err
	}
	return s.complete(ctx, rows, func(r *Setting) Terms { return terms[r.Group.ID] })
}

// ofGroup saves the items as settings in the group and returns what the
// segments have there: every named one, then the personal ones that have a
// grant or a rate in the group or are named by ?include=.
func (s *Service) ofGroup(c *gin.Context, id int64, items []pairInput) ([]*Setting, error) {
	ctx := c.Request.Context()
	if err := requireGroup(ctx, s.db.Gorm, id); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].GroupID = id
	}
	if err := s.savePairs(ctx, items); err != nil {
		return nil, err
	}
	rows, err := s.settingRows(ctx, func(db *gorm.DB) *gorm.DB {
		return db.Where(`g.id = ? AND (s.kind = 'named' OR s.id = ANY(?)
			OR EXISTS (SELECT 1 FROM segment_groups sg WHERE sg.segment_id = s.id AND sg.group_id = g.id))`, id, store.Int64s(includedIDs(c))).
			Order("s.kind = 'personal', s.id")
	})
	if err != nil {
		return nil, err
	}
	terms, err := GroupTerms(ctx, s.db.Gorm, id, idsOf(rows, segmentID))
	if err != nil {
		return nil, err
	}
	return s.complete(ctx, rows, func(r *Setting) Terms { return terms[r.Segment.ID] })
}

func requireGroup(ctx context.Context, q *gorm.DB, id int64) error {
	var found int64
	err := store.G(ctx, q).Model(&model.Group{}).Select("id").Where("id = ?", id).Take(&found).Error
	if store.IsNoRows(err) {
		return core.ErrNotFound.WithMessage(t(ctx, "group not found", "分组不存在"))
	}
	return err
}

// includedIDs reads ?include=<id>,<id>: personal segments to list in a group
// although they have nothing there yet.
func includedIDs(c *gin.Context) []int64 {
	ids := []int64{}
	for _, v := range strings.Split(c.Query("include"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func segmentID(r *Setting) int64 { return r.Segment.ID }
func groupID(r *Setting) int64   { return r.Group.ID }

// idsOf returns the distinct ids id picks of the rows.
func idsOf(rows []*Setting, id func(*Setting) int64) []int64 {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = id(r)
	}
	return x.Unique(ids)
}

// settingRows reads the pairs of a segment and a group that narrow picks
// (and orders), with the member count of each; complete fills in the rest.
func (s *Service) settingRows(ctx context.Context, narrow func(*gorm.DB) *gorm.DB) ([]*Setting, error) {
	var rows []settingRow
	if err := settingQuery(store.G(ctx, s.db.Gorm)).Scopes(narrow).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*Setting, len(rows))
	for i, r := range rows {
		out[i] = &Setting{Segment: r.SegmentRef, MemberCount: r.MemberCount, Group: SettingGroup{ID: r.GroupID, Name: r.GroupName,
			Description: r.GroupDescription, Status: r.GroupStatus, Visibility: r.GroupVisibility}}
	}
	return out, nil
}

// settingColumns are the columns of a settingRow.
const settingColumns = RefColumns + `, mc.n AS member_count,
	g.id AS group_id, g.name AS group_name, g.description AS group_description, g.status AS group_status,
	g.visibility AS group_visibility`

// settingQuery reads the settingRows of every segment s and group g; the
// caller narrows them.
func settingQuery(db *gorm.DB) *gorm.DB {
	return fromSegments(db).Select(settingColumns).
		Joins("CROSS JOIN LATERAL (SELECT count(*) AS n FROM users m WHERE m.segment_id = s.id AND m.deleted_at IS NULL) mc").
		Joins("CROSS JOIN groups g")
}

// settingRow is a row of settingQuery.
type settingRow struct {
	core.SegmentRef
	MemberCount      int64  `gorm:"column:member_count"`
	GroupID          int64  `gorm:"column:group_id"`
	GroupName        string `gorm:"column:group_name"`
	GroupDescription string `gorm:"column:group_description"`
	GroupStatus      string `gorm:"column:group_status"`
	GroupVisibility  string `gorm:"column:group_visibility"`
}

// complete fills in what the rows do not carry themselves: the terms of each
// pair, the platforms of the groups, and the names as the caller may see
// them.
func (s *Service) complete(ctx context.Context, rows []*Setting, terms func(*Setting) Terms) ([]*Setting, error) {
	show, err := ShowUsers(ctx, s.authz)
	if err != nil {
		return nil, err
	}
	platforms, err := s.platforms(ctx, idsOf(rows, groupID))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		tm := terms(r)
		r.Segment = Masked(r.Segment, show)
		r.Group.RateMultiplier, r.Group.Platforms = tm.GroupRate, platforms[r.Group.ID]
		r.Accessible, r.Granted, r.RateOverride, r.Rate = tm.Open, tm.Granted, tm.Override, tm.EffectiveRate()
	}
	return rows, nil
}

// pairInput is one changed pair of PUT /segments/:id/groups and of
// PUT /groups/:id/segments; the path names one side of it.
type pairInput struct {
	SegmentID    int64     `json:"segment_id"`
	GroupID      int64     `json:"group_id"`
	Granted      *bool     `json:"granted"`
	RateOverride rateInput `json:"rate_override"`
}

// rateInput is a rate_override request field: absent (Set false), null
// (Value nil: follow the group) or a rate. Invalid marks one that is no rate.
type rateInput struct {
	Set     bool
	Value   *decimal.Decimal
	Invalid bool
}

func (in *rateInput) UnmarshalJSON(b []byte) error {
	var text *string
	if err := json.Unmarshal(b, &text); err != nil {
		return err
	}
	*in = rateInput{Set: true}
	if text == nil {
		return nil
	}
	d, ok := core.ParseRate(*text)
	in.Value, in.Invalid = &d, !ok
	return nil
}

// checkRates fails with the field errors of the items whose rate_override
// is no rate.
func checkRates(ctx context.Context, items []pairInput) error {
	var fe []core.FieldError
	for i, it := range items {
		if it.RateOverride.Invalid {
			fe = append(fe, fieldError(ctx, "items."+strconv.Itoa(i)+".rate_override", "invalid",
				"must be a non-negative number with at most 4 decimals", "必须为非负数，最多 4 位小数"))
		}
	}
	if len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// savePairs applies the items, all or none. A field an item leaves out
// stays as it is.
func (s *Service) savePairs(ctx context.Context, items []pairInput) error {
	if err := checkRates(ctx, items); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	// One order for every writer: two saves of the same pairs cannot deadlock.
	// Items on one pair keep the order sent, and items itself is left as
	// sent: field errors name its positions.
	ordered := slices.SortedStableFunc(slices.Values(items), func(a, b pairInput) int {
		return cmp.Or(cmp.Compare(a.SegmentID, b.SegmentID), cmp.Compare(a.GroupID, b.GroupID))
	})
	return s.tx(ctx, func(tx *gorm.DB) error {
		segments, groups, err := lockPairs(ctx, tx, items)
		if err != nil {
			return err
		}
		if err := guardOwnSegments(ctx, tx, s.users, segments); err != nil {
			return err
		}
		g := store.G(ctx, tx)
		for _, it := range ordered {
			if err := savePair(g, it).Error; err != nil {
				return err
			}
		}
		// A pair that says nothing is not kept.
		return g.Where("NOT granted AND rate_override IS NULL AND segment_id = ANY(?) AND group_id = ANY(?)",
			store.Int64s(segments), store.Int64s(groups)).Delete(&model.SegmentGroup{}).Error
	})
}

// savePair writes the item: the pair is created, or what the item sets
// replaces what the pair has.
func savePair(db *gorm.DB, it pairInput) *gorm.DB {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "segment_id"}, {Name: "group_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"granted":       gorm.Expr("COALESCE(?, segment_groups.granted)", it.Granted),
			"rate_override": gorm.Expr("CASE WHEN ? THEN ?::numeric ELSE segment_groups.rate_override END", it.RateOverride.Set, it.RateOverride.Value),
			"updated_at":    gorm.Expr("now()"),
		}),
	}).Create(&newPair{SegmentID: it.SegmentID, GroupID: it.GroupID, Granted: it.Granted != nil && *it.Granted,
		RateOverride: it.RateOverride.Value})
}

// newPair is a segment_groups row, every column written as given (an
// unset grant is false); updated_at is the default, and nothing is read
// back.
type newPair struct {
	SegmentID    int64            `gorm:"column:segment_id"`
	GroupID      int64            `gorm:"column:group_id"`
	Granted      bool             `gorm:"column:granted"`
	RateOverride *decimal.Decimal `gorm:"column:rate_override"`
}

func (newPair) TableName() string { return model.SegmentGroup{}.TableName() }

// lockPairs returns the segments and the groups the items are about, locked
// against deletion until the transaction ends. It fails with the field
// errors of the items about a segment or a group that does not exist.
func lockPairs(ctx context.Context, tx *gorm.DB, items []pairInput) (segments, groups []int64, err error) {
	for _, it := range items {
		segments, groups = append(segments, it.SegmentID), append(groups, it.GroupID)
	}
	knownSegments, err := lockRows(ctx, tx, "segments", segments)
	if err != nil {
		return nil, nil, err
	}
	knownGroups, err := lockRows(ctx, tx, "groups", groups)
	if err != nil {
		return nil, nil, err
	}
	var fe []core.FieldError
	for i, it := range items {
		if !knownSegments[it.SegmentID] || !knownGroups[it.GroupID] {
			fe = append(fe, fieldError(ctx, "items."+strconv.Itoa(i), "not_found", "unknown user group or group", "用户组或分组不存在"))
		}
	}
	if len(fe) > 0 {
		return nil, nil, core.InvalidFields(fe...)
	}
	return segments, groups, nil
}

// lockRows reports which of ids are rows of the table and locks those, in id
// order, against deletion.
func lockRows(ctx context.Context, tx *gorm.DB, table string, ids []int64) (map[int64]bool, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var found []int64
	if err := store.G(ctx, tx).Table(table).Where("id = ANY(?)", store.Int64s(ids)).Order("id").
		Clauses(store.Lock(store.LockingStrengthKeyShare)).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	known := make(map[int64]bool, len(found))
	for _, id := range found {
		known[id] = true
	}
	return known, nil
}
