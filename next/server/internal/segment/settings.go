package segment

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

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
	// follows the group. Rate is what its members pay there for a model
	// with no rate of its own: the override, or the group's base rate.
	RateOverride *decimal.Decimal `json:"rate_override"`
	Rate         EffectiveRate    `json:"rate"`
	// ModelRates are the segment's rates for models in the group, by model:
	// those models are paid at these instead.
	ModelRates  []ModelRate `json:"model_rates"`
	MemberCount int64       `json:"member_count"`
}

// ModelRate is a model paid at a rate of its own.
type ModelRate struct {
	Model string          `json:"model"`
	Rate  decimal.Decimal `json:"rate"`
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

func (s *Service) segmentGroups(c *gin.Context) { s.serveSettings(c, s.ofSegment) }
func (s *Service) groupSegments(c *gin.Context) { s.serveSettings(c, s.ofGroup) }

// settingsOf returns the settings of the row the path names (a segment or
// a group).
type settingsOf func(c *gin.Context, id int64) ([]*Setting, error)

// serveSettings answers with the settings of the row the path names.
func (s *Service) serveSettings(c *gin.Context, of settingsOf) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	rows, err := of(c, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, rows)
}

// ofSegment returns what the segment has in every group.
func (s *Service) ofSegment(c *gin.Context, id int64) ([]*Setting, error) {
	ctx := c.Request.Context()
	if _, _, err := segmentKind(ctx, s.db.Gorm, id, ""); err != nil {
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

// ofGroup returns what the segments have in the group: every named one,
// then the personal ones that have a grant or a rate in the group or are
// named by ?include=.
func (s *Service) ofGroup(c *gin.Context, id int64) ([]*Setting, error) {
	ctx := c.Request.Context()
	if err := requireGroup(ctx, s.db.Gorm, id); err != nil {
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

// complete fills in what the rows do not carry themselves: the terms and
// the model rates of each pair, the platforms of the groups, and the names
// as the caller may see them.
func (s *Service) complete(ctx context.Context, rows []*Setting, terms func(*Setting) Terms) ([]*Setting, error) {
	show, err := ShowUsers(ctx, s.authz)
	if err != nil {
		return nil, err
	}
	platforms, err := s.platforms(ctx, idsOf(rows, groupID))
	if err != nil {
		return nil, err
	}
	rates, err := modelRates(ctx, s.db.Gorm, idsOf(rows, segmentID), idsOf(rows, groupID))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		tm := terms(r)
		r.Segment = Masked(r.Segment, show)
		r.Group.RateMultiplier, r.Group.Platforms = tm.GroupRate, platforms[r.Group.ID]
		r.Accessible, r.Granted, r.RateOverride, r.Rate = tm.Open, tm.Granted, tm.Override, tm.EffectiveRate()
		if r.ModelRates = rates[pairKey{r.Segment.ID, r.Group.ID}]; r.ModelRates == nil {
			r.ModelRates = []ModelRate{}
		}
	}
	return rows, nil
}

// pairKey is a segment in a group.
type pairKey struct{ segment, group int64 }

// modelRates reads the model rates the segments have in the groups, by
// segment and group, each by model.
func modelRates(ctx context.Context, q *gorm.DB, segments, groups []int64) (map[pairKey][]ModelRate, error) {
	var rows []model.SegmentModelRate
	if err := store.G(ctx, q).Select("segment_id", "group_id", "model", "rate").
		Where("segment_id = ANY(?) AND group_id = ANY(?)", store.Int64s(segments), store.Int64s(groups)).
		Order("model").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[pairKey][]ModelRate)
	for _, r := range rows {
		k := pairKey{r.SegmentID, r.GroupID}
		out[k] = append(out[k], ModelRate{Model: r.Model, Rate: r.Rate})
	}
	return out, nil
}
