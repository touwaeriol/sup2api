package segment

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The lists of POST/PATCH /groups this part owns: what the segments have in
// the group (each item a segment, its grant and its rate), and their rates
// for its models (each item a segment, a model and the rate).
const (
	segmentRatesField      = "segment_rates"
	segmentModelRatesField = "segment_model_rates"
)

// decodeGroupRates reads the lists of the body, as a write from the group;
// a list that is none is a field error.
func decodeGroupRates(ctx context.Context, body map[string]json.RawMessage) (rateWrite, []core.FieldError) {
	w := rateWrite{far: keySegment, pairList: segmentRatesField, modelList: segmentModelRatesField}
	var fe []core.FieldError
	if raw, ok := body[segmentRatesField]; ok {
		var errs []core.FieldError
		w.pairs, errs = core.DecodeList[pairInput](ctx, raw, segmentRatesField, "user group rates", "用户分组倍率")
		fe = append(fe, errs...)
	}
	if raw, ok := body[segmentModelRatesField]; ok {
		var errs []core.FieldError
		w.models, errs = core.DecodeList[modelRateInput](ctx, raw, segmentModelRatesField, "user group model rates", "用户分组模型倍率")
		fe = append(fe, errs...)
	}
	return w, fe
}

// PrepareGroupWrite implements core.GroupWritePart for segment_rates and
// segment_model_rates.
func (s *Service) PrepareGroupWrite(ctx context.Context, _ int64, body map[string]json.RawMessage) (core.GroupWrite, []core.FieldError, error) {
	w, fe := decodeGroupRates(ctx, body)
	if len(fe) > 0 || w.empty() {
		return core.GroupWrite{}, fe, nil
	}
	if fe, err := w.check(ctx, s.db.Gorm); err != nil || len(fe) > 0 {
		return core.GroupWrite{}, fe, err
	}
	write := core.GroupWrite{Apply: func(ctx context.Context, tx *gorm.DB, groupID int64) error {
		return s.writeRates(ctx, tx, groupID, w)
	}}
	if w.changesModelRates() {
		write.AfterCommit = func(ctx context.Context, _ int64) { s.ratesWritten(ctx, true) }
	}
	return write, nil, nil
}
