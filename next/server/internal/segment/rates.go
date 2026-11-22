package segment

import (
	"cmp"
	"context"
	"slices"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// The rates of a segment are written with the group (segment_rates of
// POST/PATCH /groups) or with the segment (group_rates and model_rates of
// POST/PATCH /segments), each list checked as a whole: any item that is
// wrong writes nothing, and its field error names it by its position.

// The keys of the two ends of a pair, as items name them.
const (
	keySegment = "segment_id"
	keyGroup   = "group_id"
)

func unknownItem(ctx context.Context, path string) core.FieldError {
	return core.NewFieldError(ctx, path, "not_found", "not found", "不存在")
}

// pairInput is one segment in one group: granted left out stays as it is,
// a null rate follows the group. Remove drops the pair instead, with the
// segment's model rates in the group; it takes no rate.
type pairInput struct {
	SegmentID int64          `json:"segment_id"`
	GroupID   int64          `json:"group_id"`
	Granted   *bool          `json:"granted"`
	Rate      core.RateInput `json:"rate"`
	Remove    bool           `json:"remove"`
}

// end is the id of the end of the pair key names.
func (p pairInput) end(key string) int64 {
	if key == keySegment {
		return p.SegmentID
	}
	return p.GroupID
}

// ends returns the distinct ids of the ends key names of the pairs.
func ends(items []pairInput, key string) []int64 {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.end(key)
	}
	return x.Unique(ids)
}

// removes reports whether an item removes its pair: the model rates of the
// segment in the group go with it.
func removes(items []pairInput) bool {
	return slices.ContainsFunc(items, func(it pairInput) bool { return it.Remove })
}

// pairErrors are the field errors of the pairs of the list: the end key
// names is not among known, the rate of a pair kept is missing or wrong.
func pairErrors(ctx context.Context, list, key string, items []pairInput, known map[int64]bool) []core.FieldError {
	var fe []core.FieldError
	for i, it := range items {
		if !known[it.end(key)] {
			fe = append(fe, unknownItem(ctx, core.ItemField(list, i, key)))
		}
		if !it.Remove {
			fe = append(fe, it.Rate.Errors(ctx, core.ItemField(list, i, "rate"))...)
		}
	}
	return fe
}

// modelRateInput is a segment's rate for a model in a group; null removes it.
type modelRateInput struct {
	GroupID int64          `json:"group_id"`
	Model   string         `json:"model"`
	Rate    core.RateInput `json:"rate"`
}

// modelRatesField is the list of model rates of POST/PATCH /segments.
const modelRatesField = "model_rates"

// rateRefs are what the rates of a segment write refer to: the groups that
// exist and the models with an enabled global price.
type rateRefs struct {
	groups map[int64]bool
	priced map[string]bool
}

// modelRateErrors are the field errors of the model rates, their models
// trimmed: a group that does not exist, no model, no rate, or a rate set on
// a model with no global price (removing one always can).
func modelRateErrors(ctx context.Context, items []modelRateInput, refs rateRefs) []core.FieldError {
	var fe []core.FieldError
	for i, it := range items {
		if !refs.groups[it.GroupID] {
			fe = append(fe, unknownItem(ctx, core.ItemField(modelRatesField, i, keyGroup)))
		}
		rateErrs := it.Rate.Errors(ctx, core.ItemField(modelRatesField, i, "rate"))
		model := core.ItemField(modelRatesField, i, "model")
		switch {
		case it.Model == "":
			fe = append(fe, core.NewFieldError(ctx, model, "required", "model is required", "模型必填"))
		case len(rateErrs) == 0 && it.Rate.Value != nil && !refs.priced[it.Model]:
			fe = append(fe, core.NewFieldError(ctx, model, "invalid", "the model has no global price", "该模型没有全局价格"))
		}
		fe = append(fe, rateErrs...)
	}
	return fe
}

// pricedModels reports which of models have an enabled global price.
func pricedModels(ctx context.Context, q *gorm.DB, models []string) (map[string]bool, error) {
	var found []string
	if len(models) > 0 {
		if err := store.G(ctx, q).Model(&model.ModelPrice{}).Where("enabled AND model = ANY(?)", store.Strings(models)).
			Pluck("model", &found).Error; err != nil {
			return nil, err
		}
	}
	return setOf(found), nil
}

// setOf is the set of the values.
func setOf[T comparable](values []T) map[T]bool {
	set := make(map[T]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// knownRows reports which of ids are rows of the table. With lock, in a
// transaction, it locks those in id order against deletion until it ends.
func knownRows(ctx context.Context, q *gorm.DB, table string, ids []int64, lock bool) (map[int64]bool, error) {
	if len(ids) == 0 {
		return map[int64]bool{}, nil
	}
	db := store.G(ctx, q).Table(table).Where("id = ANY(?)", store.Int64s(ids)).Order("id")
	if lock {
		if err := store.RequireTx(q); err != nil {
			return nil, err
		}
		db = db.Clauses(store.Lock(store.LockingStrengthKeyShare))
	}
	var found []int64
	if err := db.Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	return setOf(found), nil
}

// putPairs writes the pairs, in one order for every writer so that two
// writes of the same pairs cannot deadlock; items on one pair keep the
// order sent, the last one wins. A pair that says nothing is not kept. The
// caller has locked both ends.
func putPairs(ctx context.Context, tx *gorm.DB, items []pairInput) error {
	if len(items) == 0 {
		return nil
	}
	ordered := slices.SortedStableFunc(slices.Values(items), func(a, b pairInput) int {
		return cmp.Or(cmp.Compare(a.SegmentID, b.SegmentID), cmp.Compare(a.GroupID, b.GroupID))
	})
	g := store.G(ctx, tx)
	for _, it := range ordered {
		if err := putPair(g, it); err != nil {
			return err
		}
	}
	return g.Where("NOT granted AND rate_override IS NULL AND segment_id = ANY(?) AND group_id = ANY(?)",
		store.Int64s(ends(items, keySegment)), store.Int64s(ends(items, keyGroup))).Delete(&model.SegmentGroup{}).Error
}

// putPair writes the item: it saves the pair, or removes it and the
// segment's model rates in the group, in that order (putModelRates writes
// them after the pairs too).
func putPair(db *gorm.DB, it pairInput) error {
	if !it.Remove {
		return savePair(db, it).Error
	}
	const pair = "segment_id = ? AND group_id = ?"
	if err := db.Where(pair, it.SegmentID, it.GroupID).Delete(&model.SegmentGroup{}).Error; err != nil {
		return err
	}
	return db.Where(pair, it.SegmentID, it.GroupID).Delete(&model.SegmentModelRate{}).Error
}

// savePair writes the item: the pair is created, or takes the item's rate
// and, when the item sets it, its grant.
func savePair(db *gorm.DB, it pairInput) *gorm.DB {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "segment_id"}, {Name: "group_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"granted":       gorm.Expr("COALESCE(?, segment_groups.granted)", it.Granted),
			"rate_override": gorm.Expr("EXCLUDED.rate_override"),
			"updated_at":    gorm.Expr("now()"),
		}),
	}).Create(&newPair{SegmentID: it.SegmentID, GroupID: it.GroupID, Granted: it.Granted != nil && *it.Granted,
		RateOverride: it.Rate.Value})
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

// putModelRates writes the model rates of the segment, in one order for
// every writer; items on one model keep the order sent, the last one wins.
// The caller has locked the segment and the groups.
func putModelRates(ctx context.Context, tx *gorm.DB, segmentID int64, items []modelRateInput) error {
	ordered := slices.SortedStableFunc(slices.Values(items), func(a, b modelRateInput) int {
		return cmp.Or(cmp.Compare(a.GroupID, b.GroupID), cmp.Compare(a.Model, b.Model))
	})
	g := store.G(ctx, tx)
	for _, it := range ordered {
		if err := saveModelRate(g, segmentID, it).Error; err != nil {
			return err
		}
	}
	return nil
}

// saveModelRate sets the segment's rate for the item's model in its group,
// or removes it on a null rate.
func saveModelRate(db *gorm.DB, segmentID int64, it modelRateInput) *gorm.DB {
	if it.Rate.Value == nil {
		return db.Where("segment_id = ? AND group_id = ? AND model = ?", segmentID, it.GroupID, it.Model).
			Delete(&model.SegmentModelRate{})
	}
	return db.Clauses(store.UpsertOn([]string{"segment_id", "group_id", "model"}, "rate")).Create(&newModelRate{SegmentID: segmentID, GroupID: it.GroupID, Model: it.Model, Rate: *it.Rate.Value})
}

// newModelRate is a segment_model_rates row as written; updated_at is the
// default, and nothing is read back.
type newModelRate struct {
	SegmentID int64           `gorm:"column:segment_id"`
	GroupID   int64           `gorm:"column:group_id"`
	Model     string          `gorm:"column:model"`
	Rate      decimal.Decimal `gorm:"column:rate"`
}

func (newModelRate) TableName() string { return model.SegmentModelRate{}.TableName() }
