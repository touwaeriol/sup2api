package segment

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// segmentRatesField is the list of POST/PATCH /groups that sets what the
// segments have in the group: each item a segment, its grant and its rate.
const segmentRatesField = "segment_rates"

// PrepareGroupWrite implements core.GroupWritePart for segment_rates.
func (s *Service) PrepareGroupWrite(ctx context.Context, _ int64, body map[string]json.RawMessage) (core.GroupWrite, []core.FieldError, error) {
	raw, ok := body[segmentRatesField]
	if !ok {
		return core.GroupWrite{}, nil, nil
	}
	var items []pairInput
	if err := json.Unmarshal(raw, &items); err != nil {
		return core.GroupWrite{}, []core.FieldError{fieldError(ctx, segmentRatesField, "invalid",
			"must be a list of user group rates", "必须为用户分组倍率的列表")}, nil
	}
	known, err := knownRows(ctx, s.db.Gorm, "segments", ends(items, keySegment), false)
	if err != nil {
		return core.GroupWrite{}, nil, err
	}
	if fe := pairErrors(ctx, segmentRatesField, keySegment, items, known); len(fe) > 0 || len(items) == 0 {
		return core.GroupWrite{}, fe, nil
	}
	apply := func(ctx context.Context, tx *gorm.DB, groupID int64) error {
		return s.putGroupPairs(ctx, tx, groupID, items)
	}
	return core.GroupWrite{Apply: apply}, nil, nil
}

// putGroupPairs writes the checked items as pairs of the group, which tx
// has written: the segments are locked and must still exist, and none of
// them may be the caller's own (guardOwnSegments).
func (s *Service) putGroupPairs(ctx context.Context, tx *gorm.DB, groupID int64, items []pairInput) error {
	for i := range items {
		items[i].GroupID = groupID
	}
	segments := ends(items, keySegment)
	known, err := knownRows(ctx, tx, "segments", segments, true)
	if err != nil {
		return err
	}
	if fe := pairErrors(ctx, segmentRatesField, keySegment, items, known); len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	if err := guardOwnSegments(ctx, tx, s.users, segments); err != nil {
		return err
	}
	return putPairs(ctx, tx, items)
}
