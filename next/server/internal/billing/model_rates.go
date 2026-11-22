package billing

import (
	"context"
	"maps"
	"slices"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// modelRates are the rates set on models (CONTRACTS §97): a group's, by
// group id and then model, and a segment's in a group.
type modelRates struct {
	groups   map[int64]map[string]decimal.Decimal
	segments map[segmentModel]decimal.Decimal
}

// segmentModel is a model in a group for one segment.
type segmentModel struct {
	segmentID int64
	on        core.PriceQuery
}

// levels returns l with the rates set on the model of q added: its group's
// and segmentID's in the group.
func (m modelRates) levels(q core.PriceQuery, segmentID int64, l core.RateLevels) core.RateLevels {
	l.GroupModel = rateOf(m.groups[q.GroupID], q.Model)
	l.SegmentModel = rateOf(m.segments, segmentModel{segmentID: segmentID, on: q})
	return l
}

// rateOf is the rate of key in rates; nil without one.
func rateOf[K comparable](rates map[K]decimal.Decimal, key K) *decimal.Decimal {
	r, ok := rates[key]
	if !ok {
		return nil
	}
	return &r
}

// loadModelRates reads every model rate.
func loadModelRates(ctx context.Context, q *gorm.DB) (modelRates, error) {
	g := store.G(ctx, q)
	var groupRows []model.GroupModelRate
	if err := g.Select("group_id", "model", "rate").Find(&groupRows).Error; err != nil {
		return modelRates{}, err
	}
	var segmentRows []model.SegmentModelRate
	if err := g.Select("segment_id", "group_id", "model", "rate").Find(&segmentRows).Error; err != nil {
		return modelRates{}, err
	}
	out := modelRates{groups: map[int64]map[string]decimal.Decimal{}, segments: make(map[segmentModel]decimal.Decimal, len(segmentRows))}
	for _, r := range groupRows {
		if out.groups[r.GroupID] == nil {
			out.groups[r.GroupID] = map[string]decimal.Decimal{}
		}
		out.groups[r.GroupID][r.Model] = r.Rate
	}
	for _, r := range segmentRows {
		out.segments[segmentModel{segmentID: r.SegmentID, on: core.PriceQuery{Model: r.Model, GroupID: r.GroupID}}] = r.Rate
	}
	return out, nil
}

// ModelRates implements core.Pricer: levels with the rates set on the model
// of q, from the snapshot Resolve reads.
func (s *Service) ModelRates(ctx context.Context, q core.PriceQuery, segmentID int64, levels core.RateLevels) (core.RateLevels, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return levels, err
	}
	return snap.rates.levels(q, segmentID, levels), nil
}

// RatesChanged drops the price snapshot here and on the other nodes, once a
// write of model rates outside billing (those of segments) committed.
func (s *Service) RatesChanged(ctx context.Context) { s.changed(ctx, "model_rates") }

// rateChanges are checked model rates of a group, by model: the last one
// given for a model, nil to remove it.
type rateChanges map[string]*decimal.Decimal

// write writes the changes as the model rates of the group in tx, by
// updatedBy: removed ones deleted, the others upserted in the order of
// their models.
func (rc rateChanges) write(ctx context.Context, tx *gorm.DB, groupID, updatedBy int64) error {
	var removed []string
	var saved []model.GroupModelRate
	for _, m := range slices.Sorted(maps.Keys(rc)) {
		if rate := rc[m]; rate == nil {
			removed = append(removed, m)
		} else {
			saved = append(saved, model.GroupModelRate{GroupID: groupID, Model: m, Rate: *rate, UpdatedBy: nullID(updatedBy)})
		}
	}
	g := store.G(ctx, tx)
	if len(removed) > 0 {
		if err := g.Where("group_id = ? AND model = ANY(?)", groupID, store.Strings(removed)).Delete(&model.GroupModelRate{}).Error; err != nil {
			return err
		}
	}
	if len(saved) == 0 {
		return nil
	}
	return g.Clauses(store.UpsertOn(groupModelKeys, "rate", "updated_by")).Create(&saved).Error
}
