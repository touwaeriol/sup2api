package segment

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// rateWrite is what one write sets of the rates between segments and
// groups, seen from one end (a segment's write, or a group's): its pairs
// with the far ends and its model rates there. The two directions are the
// same rows, so one type checks and writes both.
type rateWrite struct {
	// far is the key of the ends the items name: keyGroup for a segment's
	// write, keySegment for a group's.
	far string
	// pairList and modelList name the lists in the request, for the field
	// errors.
	pairList, modelList string
	pairs               []pairInput
	models              []modelRateInput
}

// empty reports whether the write sets nothing.
func (w rateWrite) empty() bool { return len(w.pairs)+len(w.models) == 0 }

// farIDs are the distinct far ends the items name.
func (w rateWrite) farIDs() []int64 {
	return x.Unique(append(ends(w.pairs, w.far), ends(w.models, w.far)...))
}

// farTable is the table of the far ends.
func (w rateWrite) farTable() string {
	if w.far == keySegment {
		return "segments"
	}
	return "groups"
}

// trimModels trims the models of the model rates, as they are checked and
// stored.
func (w rateWrite) trimModels() {
	for i := range w.models {
		w.models[i].Model = strings.TrimSpace(w.models[i].Model)
	}
}

// refs reads what the items refer to. With lock, in a transaction, the far
// ends are locked against deletion until it ends.
func (w rateWrite) refs(ctx context.Context, q *gorm.DB, lock bool) (rateRefs, error) {
	known, err := knownRows(ctx, q, w.farTable(), w.farIDs(), lock)
	if err != nil {
		return rateRefs{}, err
	}
	models := make([]string, len(w.models))
	for i, it := range w.models {
		models[i] = it.Model
	}
	priced, err := pricedModels(ctx, q, x.Unique(models))
	return rateRefs{known: known, priced: priced}, err
}

// errors are the field errors of the items against what they refer to.
func (w rateWrite) errors(ctx context.Context, refs rateRefs) []core.FieldError {
	return append(pairErrors(ctx, w.pairList, w.far, w.pairs, refs.known), modelRateErrors(ctx, w.modelList, w.far, w.models, refs)...)
}

// check trims the models and returns the field errors of the write, read
// outside the transaction that writes it.
func (w rateWrite) check(ctx context.Context, q *gorm.DB) ([]core.FieldError, error) {
	w.trimModels()
	refs, err := w.refs(ctx, q, false)
	if err != nil {
		return nil, err
	}
	return w.errors(ctx, refs), nil
}

// segments are the segments the write changes the terms of, made from
// the end self.
func (w rateWrite) segments(self int64) []int64 {
	if w.far == keySegment {
		return w.farIDs()
	}
	return []int64{self}
}

// changesModelRates reports whether the write changes model rates: it sets
// some, or removes a pair and with it the segment's model rates there.
func (w rateWrite) changesModelRates() bool {
	return len(w.models) > 0 || removes(w.pairs)
}

// writeRates writes the checked items from the end self, which tx has written:
// the far ends are locked and must still exist, and none of the segments
// may be the caller's own (guardOwnSegments). Pairs go first, so a model
// rate sent with the removal of its pair is kept.
func (s *Service) writeRates(ctx context.Context, tx *gorm.DB, self int64, w rateWrite) error {
	if w.empty() {
		return nil
	}
	near := otherKey(w.far)
	setEnds(w.pairs, near, self)
	setEnds(w.models, near, self)
	refs, err := w.refs(ctx, tx, true)
	if err != nil {
		return err
	}
	if fe := w.errors(ctx, refs); len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	if err := guardOwnSegments(ctx, tx, s.users, w.segments(self)); err != nil {
		return err
	}
	if err := putPairs(ctx, tx, w.pairs); err != nil {
		return err
	}
	return putModelRates(ctx, tx, w.models)
}
