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
	ctx := core.WithUserID(e.t.Context(), uid)
	raw, err := json.Marshal(items)
	require.NoError(e.t, err)
	w, fe, err := e.svc.PrepareGroupWrite(ctx, groupID, map[string]json.RawMessage{"segment_rates": raw})
	if err != nil || len(fe) > 0 || w.Apply == nil {
		return fe, err
	}
	return nil, e.db.Tx(ctx, func(tx *gorm.DB) error { return w.Apply(ctx, tx, groupID) })
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
