package segment

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

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

// rateInput is a rate request field: a rate, or null (Value nil) to follow
// the level below. Set marks it present, Invalid one that is no rate.
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

// errors returns the field error of the rate at path: it is required, and
// a rate or null.
func (in rateInput) errors(ctx context.Context, path string) []core.FieldError {
	switch {
	case !in.Set:
		return []core.FieldError{fieldError(ctx, path, "required", "a rate or null is required", "必须填写倍率或 null")}
	case in.Invalid:
		return []core.FieldError{fieldError(ctx, path, "invalid",
			"must be a non-negative number with at most 4 decimals", "必须为非负数，最多 4 位小数")}
	}
	return nil
}

// itemPath is the path of field of item i of the list.
func itemPath(list string, i int, field string) string {
	return list + "." + strconv.Itoa(i) + "." + field
}

func unknownItem(ctx context.Context, path string) core.FieldError {
	return fieldError(ctx, path, "not_found", "not found", "不存在")
}

// pairInput is one segment in one group: granted left out stays as it is,
// a null rate follows the group.
type pairInput struct {
	SegmentID int64     `json:"segment_id"`
	GroupID   int64     `json:"group_id"`
	Granted   *bool     `json:"granted"`
	Rate      rateInput `json:"rate"`
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

// pairErrors are the field errors of the pairs of the list: the end key
// names is not among known, the rate is missing or wrong.
func pairErrors(ctx context.Context, list, key string, items []pairInput, known map[int64]bool) []core.FieldError {
	var fe []core.FieldError
	for i, it := range items {
		if !known[it.end(key)] {
			fe = append(fe, unknownItem(ctx, itemPath(list, i, key)))
		}
		fe = append(fe, it.Rate.errors(ctx, itemPath(list, i, "rate"))...)
	}
	return fe
}

// modelRateInput is a segment's rate for a model in a group; null removes it.
type modelRateInput struct {
	GroupID int64     `json:"group_id"`
	Model   string    `json:"model"`
	Rate    rateInput `json:"rate"`
}

// modelRatesField is the list of model rates of POST/PATCH /segments.
const modelRatesField = "model_rates"

// modelRateErrors trims the models of the items and returns the field
// errors of the items: a group not among known, no model, no rate.
func modelRateErrors(ctx context.Context, items []modelRateInput, known map[int64]bool) []core.FieldError {
	var fe []core.FieldError
	for i := range items {
		it := &items[i]
		it.Model = strings.TrimSpace(it.Model)
		if !known[it.GroupID] {
			fe = append(fe, unknownItem(ctx, itemPath(modelRatesField, i, keyGroup)))
		}
		if it.Model == "" {
			fe = append(fe, fieldError(ctx, itemPath(modelRatesField, i, "model"), "required", "model is required", "模型必填"))
		}
		fe = append(fe, it.Rate.errors(ctx, itemPath(modelRatesField, i, "rate"))...)
	}
	return fe
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
	known := make(map[int64]bool, len(found))
	for _, id := range found {
		known[id] = true
	}
	return known, nil
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
		if err := savePair(g, it).Error; err != nil {
			return err
		}
	}
	return g.Where("NOT granted AND rate_override IS NULL AND segment_id = ANY(?) AND group_id = ANY(?)",
		store.Int64s(ends(items, keySegment)), store.Int64s(ends(items, keyGroup))).Delete(&model.SegmentGroup{}).Error
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
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "segment_id"}, {Name: "group_id"}, {Name: "model"}},
		DoUpdates: clause.Assignments(map[string]any{
			"rate":       gorm.Expr("EXCLUDED.rate"),
			"updated_at": gorm.Expr("now()"),
		}),
	}).Create(&newModelRate{SegmentID: segmentID, GroupID: it.GroupID, Model: it.Model, Rate: *it.Rate.Value})
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
