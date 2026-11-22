package segment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// settingsFixture is segment "vip" (one member) and three
// groups: a public one at rate 1.5, a private one at rate 2 granted to vip at
// its own rate 0.5, and a private one vip has nothing in.
type settingsFixture struct {
	*env
	vip                    int64
	public, granted, other int64
}

func newSettingsFixture(t *testing.T) settingsFixture {
	t.Helper()
	f := settingsFixture{env: setup(t)}
	f.vip = f.segment("vip")
	f.public, f.granted, f.other = f.group("public", "public", "1.5"), f.group("granted", "restricted", "2"), f.group("other", "restricted", "1")
	f.pair(f.vip, f.granted, true, "0.5")
	f.exec(`UPDATE users SET segment_id = $1 WHERE id = $2`, f.vip, f.plain)
	return f
}

func rate(value, source, groupDefault string) map[string]any {
	return map[string]any{"value": value, "source": source, "group_default": groupDefault}
}

// pairOf returns the stored grant and rate override of a pair, "none" when
// there is no row.
func (f settingsFixture) pairOf(segment, group int64) string {
	f.t.Helper()
	var out string
	err := f.db.SQL.QueryRowContext(f.t.Context(), `SELECT COALESCE((SELECT granted::text || ' ' || COALESCE(rate_override::text, 'null')
		FROM segment_groups WHERE segment_id = $1 AND group_id = $2), 'none')`, segment, group).Scan(&out)
	require.NoError(f.t, err)
	return out
}

func TestSegmentGroups(t *testing.T) {
	f := newSettingsFixture(t)
	rows := f.do(f.admin, "GET", path("/segments/%d/groups", f.vip), nil).ok(200).rows()
	require.Len(t, rows, 3, "one row per group, by id")

	require.Equal(t, map[string]any{
		"segment": map[string]any{"id": float64(f.vip), "kind": "named", "name": "vip"},
		"group": map[string]any{"id": float64(f.granted), "name": "granted", "description": "", "status": "active",
			"visibility": "restricted", "rate_multiplier": "2", "platforms": []any{"anthropic"}},
		"accessible": true, "granted": true, "rate_override": "0.5", "rate": rate("0.5", "segment", "2"),
		"model_rates": []any{}, "member_count": float64(1),
	}, rows[1])
	for _, tc := range []struct {
		row        map[string]any
		group      int64
		accessible bool
		rate       map[string]any
	}{
		{rows[0], f.public, true, rate("1.5", "group", "1.5")},
		{rows[2], f.other, false, rate("1", "group", "1")},
	} {
		require.Equal(t, float64(tc.group), tc.row["group"].(map[string]any)["id"])
		require.Equal(t, tc.accessible, tc.row["accessible"])
		require.Equal(t, false, tc.row["granted"])
		require.Nil(t, tc.row["rate_override"])
		require.Equal(t, tc.rate, tc.row["rate"])
	}
}

func TestSettingsRefusals(t *testing.T) {
	f := newSettingsFixture(t)
	f.do(f.admin, "GET", "/segments/999999/groups", nil).failed(404, "not_found")
	f.do(f.plain, "GET", path("/segments/%d/groups", f.vip), nil).failed(403, "permission_denied")
	f.do(f.admin, "GET", "/groups/999999/segments", nil).failed(404, "not_found")
	f.do(f.plain, "GET", path("/groups/%d/segments", f.granted), nil).failed(403, "permission_denied")
}

func TestGroupSegments(t *testing.T) {
	f := newSettingsFixture(t)
	def := f.defaultSegment()
	withGrant, withRate, bare := f.personal(f.admin), f.personal(f.masked), f.personal(f.user("bare@x.com"))
	f.pair(withGrant, f.granted, true, "")
	f.pair(withRate, f.granted, false, "0.7")
	segments := func(query string) []any {
		rows := f.do(f.admin, "GET", path("/groups/%d/segments%s", f.granted, query), nil).ok(200).rows()
		return names(rows, func(r map[string]any) any { return idOf(r["segment"].(map[string]any)) })
	}

	require.Equal(t, []any{def, f.vip, withGrant, withRate}, segments(""),
		"every named segment, then the personal ones with something in the group")
	require.Equal(t, []any{def, f.vip, withGrant, withRate, bare}, segments(path("?include=%d,x,%d", bare, f.vip)),
		"?include= adds personal segments that have nothing there yet")

	rows := f.do(f.admin, "GET", path("/groups/%d/segments", f.granted), nil).ok(200).rows()
	require.Equal(t, false, rows[0]["accessible"], "the default segment was not granted the private group")
	require.Equal(t, rate("2", "group", "2"), rows[0]["rate"])
	require.Equal(t, float64(0), rows[0]["member_count"])
	require.Equal(t, rate("0.5", "segment", "2"), rows[1]["rate"])
	require.NotContains(t, rows[1], "price_rule_count")
	require.Equal(t, float64(1), rows[1]["member_count"])
	require.Equal(t, map[string]any{"id": float64(withGrant), "kind": "personal", "name": "admin@x.com", "user_id": float64(f.admin)}, rows[2]["segment"])
	require.Equal(t, true, rows[2]["accessible"])
	require.Equal(t, "0.7", rows[3]["rate_override"])
	require.Equal(t, float64(f.granted), rows[3]["group"].(map[string]any)["id"])
}

// A personal segment with only model rates in the group is listed too.
func TestGroupSegmentsWithModelRates(t *testing.T) {
	f := newSettingsFixture(t)
	personal := f.personal(f.admin)
	f.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'm', 0.5)`, personal, f.granted)
	rows := f.do(f.admin, "GET", path("/groups/%d/segments", f.granted), nil).ok(200).rows()
	last := rows[len(rows)-1]
	require.Equal(t, float64(personal), last["segment"].(map[string]any)["id"])
	require.Equal(t, []any{map[string]any{"model": "m", "rate": "0.5"}}, last["model_rates"])
	require.Equal(t, false, last["granted"])
}

// A caller that may not read users sees a personal segment as "#<user id>":
// neither the rows nor the search give emails away.
func TestPersonalNamesAreMasked(t *testing.T) {
	f := newSettingsFixture(t)
	personal := f.personal(f.plain)
	f.pair(personal, f.granted, true, "")
	for _, tc := range []struct {
		caller int64
		name   string
		user   bool
		found  float64
	}{
		{f.admin, "plain@x.com", true, 1},
		{f.masked, path("#%d", f.plain), false, 0},
	} {
		list := f.do(tc.caller, "GET", "/segments?kind=personal", nil).ok(200).rows()
		require.Len(t, list, 1)
		require.Equal(t, tc.name, list[0]["name"])
		require.Equal(t, tc.user, list[0]["user"] != nil)
		require.Equal(t, tc.name, f.do(tc.caller, "GET", path("/segments/%d", personal), nil).ok(200).data()["name"])
		for _, q := range []string{"plain@", "name+of+plain"} {
			require.Equal(t, tc.found, f.do(tc.caller, "GET", "/segments?kind=personal&q="+q, nil).ok(200).total(), q)
			require.Equal(t, tc.found, f.do(tc.caller, "GET", "/segments?kind=all&q="+q, nil).ok(200).total(), q)
		}
		ofSegment := f.do(tc.caller, "GET", path("/segments/%d/groups", personal), nil).ok(200).rows()
		require.Equal(t, tc.name, ofSegment[0]["segment"].(map[string]any)["name"])
		ofGroup := f.do(tc.caller, "GET", path("/groups/%d/segments", f.granted), nil).ok(200).rows()
		require.Equal(t, tc.name, ofGroup[len(ofGroup)-1]["segment"].(map[string]any)["name"])
	}
}

// The detail and a new personal segment are masked as the lists are.
func TestPersonalNamesMaskedOutsideLists(t *testing.T) {
	f := newSettingsFixture(t)
	personal := f.personal(f.plain)
	masked := map[string]any{"id": float64(personal), "kind": "personal", "name": path("#%d", f.plain), "user_id": float64(f.plain)}
	got := f.do(f.masked, "GET", path("/segments/%d", personal), nil).ok(200).data()
	require.Equal(t, masked, map[string]any{"id": got["id"], "kind": got["kind"], "name": got["name"], "user_id": got["user_id"]})
	require.Equal(t, map[string]any{"id": float64(f.vip), "kind": "named", "name": "vip"}, got["origin"], "the origin is a named segment")
	made := f.do(f.masked, "POST", path("/users/%d/personal-segment", f.admin), map[string]any{"copy": false}).ok(201).data()
	require.Equal(t, path("#%d", f.admin), made["name"])
	require.NotContains(t, made, "user")
}
