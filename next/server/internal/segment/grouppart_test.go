package segment

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// groupWrite writes segment_rates as POST/PATCH /groups do, as the user:
// the field errors the part found, or the error of the transaction.
func (e *env) groupWrite(uid, groupID int64, items ...map[string]any) ([]core.FieldError, error) {
	return e.groupWriteBody(uid, groupID, map[string]any{"segment_rates": items})
}

// groupWriteBody writes the lists of body as POST/PATCH /groups do, as the
// user.
func (e *env) groupWriteBody(uid, groupID int64, body map[string]any) ([]core.FieldError, error) {
	ctx := core.WithUserID(e.t.Context(), uid)
	raw := map[string]json.RawMessage{}
	for k, v := range body {
		b, err := json.Marshal(v)
		require.NoError(e.t, err)
		raw[k] = b
	}
	w, fe, err := e.svc.PrepareGroupWrite(ctx, groupID, raw)
	if err != nil || len(fe) > 0 || w.Apply == nil {
		return fe, err
	}
	if err := e.db.Tx(ctx, func(tx *gorm.DB) error { return w.Apply(ctx, tx, groupID) }); err != nil {
		return nil, err
	}
	if w.AfterCommit != nil {
		w.AfterCommit(ctx, groupID)
	}
	return nil, nil
}

// groupCall writes segment_rates of group g as the admin and answers the
// status POST/PATCH /groups would.
func (e *env) groupCall(g int64, items ...map[string]any) func() int {
	return func() int {
		fe, err := e.groupWrite(e.admin, g, items...)
		switch {
		case len(fe) > 0:
			return 400
		case err != nil:
			return core.AsError(err).Status
		}
		return 200
	}
}

// fields lists the fields the errors name.
func fields(fe []core.FieldError) []string {
	out := make([]string, len(fe))
	for i, e := range fe {
		out[i] = e.Field
	}
	return out
}

func TestGroupWritePart(t *testing.T) {
	f := newSettingsFixture(t)
	def := f.defaultSegment()
	write := func(items ...map[string]any) []string {
		fe, err := f.groupWrite(f.admin, f.other, items...)
		require.NoError(t, err)
		return fields(fe)
	}

	require.Empty(t, write(map[string]any{"segment_id": def, "granted": true, "rate": nil},
		map[string]any{"segment_id": f.vip, "rate": "0.7"}))
	require.Equal(t, "true null", f.pairOf(def, f.other))
	require.Equal(t, "false 0.7000", f.pairOf(f.vip, f.other))
	require.Equal(t, rate("0.7", "segment", "1"), f.do(f.admin, "GET", path("/groups/%d/segments", f.other), nil).ok(200).rows()[1]["rate"])

	require.Equal(t, []string{"segment_rates.1.segment_id"},
		write(map[string]any{"segment_id": def, "granted": false, "rate": nil}, map[string]any{"segment_id": 999999, "rate": nil}))
	require.Equal(t, []string{"segment_rates.0.rate", "segment_rates.1.rate"},
		write(map[string]any{"segment_id": def, "granted": false}, map[string]any{"segment_id": f.vip, "rate": "-1"}))
	require.Equal(t, "true null", f.pairOf(def, f.other), "all or none")
}

// An item with remove drops the pair and the segment's model rates in the
// group, takes no rate, and has billing's snapshot dropped.
func TestGroupWritePartRemove(t *testing.T) {
	f := newSettingsFixture(t)
	f.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'm', 0.5), ($1, $3, 'm', 0.6)`,
		f.vip, f.granted, f.public)
	fe, err := f.groupWrite(f.admin, f.granted, map[string]any{"segment_id": f.vip, "remove": true})
	require.NoError(t, err)
	require.Empty(t, fe)
	require.Equal(t, "none", f.pairOf(f.vip, f.granted))
	require.Equal(t, "none", f.modelRateOf(f.vip, f.granted, "m"))
	require.Equal(t, "0.6000", f.modelRateOf(f.vip, f.public, "m"), "other groups keep theirs")
	require.Equal(t, int32(1), f.ratesChanged.Load())

	fe, err = f.groupWrite(f.admin, f.granted, map[string]any{"segment_id": 999999, "remove": true})
	require.NoError(t, err)
	require.Equal(t, []string{"segment_rates.0.segment_id"}, fields(fe))
	_, err = f.groupWrite(f.admin, f.granted, map[string]any{"segment_id": f.vip, "rate": "0.5"})
	require.NoError(t, err)
	require.Equal(t, int32(1), f.ratesChanged.Load(), "a write without removals leaves the model rates alone")
}

// A rate that is no string is the field error of its item.
func TestGroupWritePartRateType(t *testing.T) {
	f := newSettingsFixture(t)
	fe, err := f.groupWrite(f.admin, f.other, map[string]any{"segment_id": f.vip, "rate": "1"},
		map[string]any{"segment_id": f.vip, "rate": 1.5})
	require.NoError(t, err)
	require.Equal(t, []string{"segment_rates.1.rate"}, fields(fe))
}

// What PrepareGroupWrite makes of a body: nothing without segment_rates or
// with an empty list, a field error of a list that is none.
func TestGroupWritePartBody(t *testing.T) {
	f := newSettingsFixture(t)
	fe, err := f.groupWrite(f.admin, f.other)
	require.NoError(t, err)
	require.Empty(t, fe, "an empty list writes nothing")
	w, fe, err := f.svc.PrepareGroupWrite(t.Context(), f.other, map[string]json.RawMessage{"name": []byte(`"x"`)})
	require.NoError(t, err)
	require.Empty(t, fe)
	require.Nil(t, w.Apply, "a body without segment_rates has nothing for it")
	_, fe, err = f.svc.PrepareGroupWrite(t.Context(), f.other, map[string]json.RawMessage{"segment_rates": []byte(`{}`)})
	require.NoError(t, err)
	require.Equal(t, []string{"segment_rates"}, fields(fe))
}

// segment_model_rates sets the segments' rates for the group's models from
// the group, as model_rates of a segment does from the other end: checked as
// a whole, the item's group ignored, billing's snapshot dropped.
func TestGroupWriteSegmentModelRates(t *testing.T) {
	f := newSettingsFixture(t)
	write := func(items ...map[string]any) []string {
		fe, err := f.groupWriteBody(f.admin, f.public, map[string]any{"segment_model_rates": items})
		require.NoError(t, err)
		return fields(fe)
	}
	require.Empty(t, write(map[string]any{"segment_id": f.vip, "model": " m ", "rate": "0.85"},
		map[string]any{"segment_id": f.vip, "group_id": f.other, "model": "gpt-4o", "rate": "0.9"}))
	require.Equal(t, "0.8500", f.modelRateOf(f.vip, f.public, "m"), "the model is trimmed")
	require.Equal(t, "0.9000", f.modelRateOf(f.vip, f.public, "gpt-4o"), "the item's group is the group written")
	require.Equal(t, "none", f.modelRateOf(f.vip, f.other, "gpt-4o"))
	require.Equal(t, int32(1), f.ratesChanged.Load())
	row := f.do(f.admin, "GET", path("/groups/%d/segments", f.public), nil).ok(200).rows()
	require.NotNil(t, rowOfSegment(row, f.vip), "a segment with only model rates is listed")
	require.Equal(t, []any{map[string]any{"model": "gpt-4o", "rate": "0.9"}, map[string]any{"model": "m", "rate": "0.85"}},
		rowOfSegment(row, f.vip)["model_rates"])

	require.Empty(t, write(map[string]any{"segment_id": f.vip, "model": "m", "rate": nil}))
	require.Equal(t, "none", f.modelRateOf(f.vip, f.public, "m"), "null removes it")
	require.Equal(t, []string{"segment_model_rates.0.segment_id", "segment_model_rates.1.model", "segment_model_rates.2.rate", "segment_model_rates.3.model"},
		write(map[string]any{"segment_id": 999999, "model": "m", "rate": "1"},
			map[string]any{"segment_id": f.vip, "model": "unpriced", "rate": "1"},
			map[string]any{"segment_id": f.vip, "model": "m"},
			map[string]any{"segment_id": f.vip, "model": " ", "rate": "1"}))
	require.Equal(t, "0.9000", f.modelRateOf(f.vip, f.public, "gpt-4o"), "all or none")
}

// A pair removed and its model rates set in one write: the removal goes
// first, the model rates sent with it stay; both lists are checked together.
func TestGroupWriteRemoveThenModelRates(t *testing.T) {
	f := newSettingsFixture(t)
	f.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'gpt-4o', 0.5)`, f.vip, f.granted)
	fe, err := f.groupWriteBody(f.admin, f.granted, map[string]any{
		"segment_rates":       []any{map[string]any{"segment_id": f.vip, "remove": true}},
		"segment_model_rates": []any{map[string]any{"segment_id": f.vip, "model": "m", "rate": "0.7"}}})
	require.NoError(t, err)
	require.Empty(t, fe)
	require.Equal(t, "none", f.pairOf(f.vip, f.granted))
	require.Equal(t, "none", f.modelRateOf(f.vip, f.granted, "gpt-4o"))
	require.Equal(t, "0.7000", f.modelRateOf(f.vip, f.granted, "m"))

	fe, err = f.groupWriteBody(f.admin, f.granted, map[string]any{
		"segment_rates":       []any{map[string]any{"segment_id": f.vip, "rate": "-1"}},
		"segment_model_rates": map[string]any{}})
	require.NoError(t, err)
	require.Equal(t, []string{"segment_model_rates"}, fields(fe), "a list that is none")
	fe, err = f.groupWriteBody(f.admin, f.granted, map[string]any{
		"segment_rates":       []any{map[string]any{"segment_id": f.vip, "rate": "-1"}},
		"segment_model_rates": []any{map[string]any{"segment_id": 999999, "model": "m", "rate": "1"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"segment_rates.0.rate", "segment_model_rates.0.segment_id"}, fields(fe))
}

// rowOfSegment is the row of segment id among rows of GET /groups/:id/segments.
func rowOfSegment(rows []map[string]any, id int64) map[string]any {
	for _, r := range rows {
		if seg, _ := r["segment"].(map[string]any); seg != nil && int64(seg["id"].(float64)) == id {
			return r
		}
	}
	return nil
}

// Saves of the same pairs at once, from either side and in either order, all
// go through, and each pair ends as one of them left it.
func TestSettingsSaveRace(t *testing.T) {
	f := newSettingsFixture(t)
	// item grants the pair at the rate; nil says nothing, so the pair goes.
	item := func(key string, id int64, rate any) map[string]any {
		return map[string]any{key: id, "granted": rate != nil, "rate": rate}
	}
	ofSegment := func(items ...map[string]any) func() int {
		return func() int {
			return f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"group_rates": items}).code
		}
	}
	saves := []func() int{
		ofSegment(item("group_id", f.other, "0.1"), item("group_id", f.public, "0.1")),
		ofSegment(item("group_id", f.public, "0.2"), item("group_id", f.other, "0.2")),
		f.groupCall(f.other, item("segment_id", f.vip, "0.3")),
		f.groupCall(f.other, item("segment_id", f.vip, nil)),
	}
	for range 10 {
		codes := concurrently(len(saves), func(i int) int { return saves[i]() })
		require.Equal(t, len(saves), count(codes, 200), "%v", codes)
		require.Contains(t, []string{"true 0.1000", "true 0.2000", "true 0.3000", "none"}, f.pairOf(f.vip, f.other))
		require.Contains(t, []string{"true 0.1000", "true 0.2000"}, f.pairOf(f.vip, f.public))
	}
}
