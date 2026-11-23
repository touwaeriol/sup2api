package group

import (
	"context"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// PricingSummary is what a group row tells of group pricing (CONTRACTS §97).
type PricingSummary struct {
	Access Access `json:"access"`
	// PriceRuleCount counts the models the group sets a price or a rate of
	// its own on, as its pricing table lists them: provided now or not.
	PriceRuleCount int64 `json:"price_rule_count"`
	// ModelCount is the number of models the group provides (Models.Offered).
	ModelCount int `json:"model_count"`
}

// Access is who may use a private group: the named segments granted it and
// how many personal ones. A public group is open to everyone and lists none.
type Access struct {
	Segments      []core.SegmentRef `json:"segments"`
	PersonalCount int64             `json:"personal_count"`
}

// fillSummary sets the pricing summary of the groups, with one query per part.
func (s *Service) fillSummary(ctx context.Context, gs []*Group) error {
	ids := make([]int64, len(gs))
	policies := make([]core.GroupInfo, len(gs))
	for i, g := range gs {
		ids[i] = g.ID
		policies[i] = core.GroupInfo{ID: g.ID, ModelAllowlist: g.ModelAllowlist, ModelFilterMode: g.ModelFilterMode}
	}
	access, err := groupAccess(ctx, s.db.Gorm, ids)
	if err != nil {
		return err
	}
	offered, err := s.offeredModels(ctx, policies)
	if err != nil {
		return err
	}
	rules, err := ruleCounts(ctx, s.db.Gorm, ids)
	if err != nil {
		return err
	}
	for _, g := range gs {
		g.Access, g.ModelCount, g.PriceRuleCount = access[g.ID], len(offered[g.ID]), rules[g.ID]
	}
	return nil
}

// ruleCounts counts, for each of the groups, the models it sets a price
// (price_rules) or a rate (group_model_rates) of its own on, by group id.
func ruleCounts(ctx context.Context, q *gorm.DB, ids []int64) (map[int64]int64, error) {
	var rows []struct {
		GroupID int64 `gorm:"column:group_id"`
		N       int64 `gorm:"column:n"`
	}
	g := store.G(ctx, q)
	priced := g.Model(&model.PriceRule{}).Select("group_id, model").Where("group_id = ANY(?)", store.Int64s(ids))
	rated := g.Model(&model.GroupModelRate{}).Select("group_id, model").Where("group_id = ANY(?)", store.Int64s(ids))
	if err := g.Table("((?) UNION (?)) AS m", priced, rated).Select("group_id, count(*) AS n").
		Group("group_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.GroupID] = r.N
	}
	return out, nil
}

// groupAccess returns the access of each of the groups, by group id.
func groupAccess(ctx context.Context, q *gorm.DB, ids []int64) (map[int64]Access, error) {
	out := make(map[int64]Access, len(ids))
	for _, id := range ids {
		out[id] = Access{Segments: []core.SegmentRef{}}
	}
	var rows []accessRow
	// For each private group of ids that grants a segment: the named
	// segments granted it (ids and names in id order) and the number of
	// personal ones.
	if err := store.G(ctx, q).Table("segment_groups sg").Select(`sg.group_id,
			COALESCE(array_agg(s.id ORDER BY s.id) FILTER (WHERE s.kind = 'named'), '{}') AS segment_ids,
			COALESCE(array_agg(s.name ORDER BY s.id) FILTER (WHERE s.kind = 'named'), '{}') AS names,
			count(*) FILTER (WHERE s.kind = 'personal') AS personal_count`).
		Joins("JOIN segments s ON s.id = sg.segment_id").Joins("JOIN groups g ON g.id = sg.group_id").
		Where("sg.group_id = ANY(?) AND sg.granted AND g.visibility = 'restricted'", store.Int64s(ids)).
		Group("sg.group_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GroupID] = r.access()
	}
	return out, nil
}

// accessRow is who a private group is granted to, as groupAccess reads it.
type accessRow struct {
	GroupID       int64         `gorm:"column:group_id"`
	SegmentIDs    store.Int64s  `gorm:"column:segment_ids"`
	Names         store.Strings `gorm:"column:names"`
	PersonalCount int64         `gorm:"column:personal_count"`
}

func (r accessRow) access() Access {
	a := Access{Segments: []core.SegmentRef{}, PersonalCount: r.PersonalCount}
	for i, id := range r.SegmentIDs {
		a.Segments = append(a.Segments, core.SegmentRef{ID: id, Kind: core.SegmentNamed, Name: r.Names[i]})
	}
	return a
}
