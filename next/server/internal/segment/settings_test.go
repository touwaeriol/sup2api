package segment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// settingsFixture is segment "vip" (discount 0.8, one member) and three
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
	f.vip = f.segment("vip", "0.8")
	f.public, f.granted, f.other = f.group("public", "public", "1.5"), f.group("granted", "restricted", "2"), f.group("other", "restricted", "1")
	f.pair(f.vip, f.granted, true, "0.5")
	f.exec(`UPDATE users SET segment_id = $1 WHERE id = $2`, f.vip, f.plain)
	return f
}

func rate(value, source, groupDefault, discount string) map[string]any {
	return map[string]any{"value": value, "source": source, "group_default": groupDefault, "discount": discount}
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
		"accessible": true, "granted": true, "rate_override": "0.5", "rate": rate("0.5", "segment", "2", "0.8"),
		"member_count": float64(1),
	}, rows[1])
	for _, tc := range []struct {
		row        map[string]any
		group      int64
		accessible bool
		rate       map[string]any
	}{
		{rows[0], f.public, true, rate("1.2", "discount", "1.5", "0.8")},
		{rows[2], f.other, false, rate("0.8", "discount", "1", "0.8")},
	} {
		require.Equal(t, float64(tc.group), tc.row["group"].(map[string]any)["id"])
		require.Equal(t, tc.accessible, tc.row["accessible"])
		require.Equal(t, false, tc.row["granted"])
		require.Nil(t, tc.row["rate_override"])
		require.Equal(t, tc.rate, tc.row["rate"])
	}

	f.do(f.admin, "GET", "/segments/999999/groups", nil).failed(404, "not_found")
	f.do(f.plain, "GET", path("/segments/%d/groups", f.vip), nil).failed(403, "permission_denied")
}

func TestSetSegmentGroups(t *testing.T) {
	f := newSettingsFixture(t)
	put := func(items ...map[string]any) response {
		return f.do(f.admin, "PUT", path("/segments/%d/groups", f.vip), map[string]any{"items": items})
	}

	// A field left out stays, null clears the override, a value sets it.
	rows := put(
		map[string]any{"group_id": f.granted, "granted": false},
		map[string]any{"group_id": f.other, "granted": true, "rate_override": "0.2500"},
		map[string]any{"group_id": f.public, "rate_override": "0"},
	).ok(200).rows()
	require.Equal(t, "false 0.5000", f.pairOf(f.vip, f.granted))
	require.Equal(t, "true 0.2500", f.pairOf(f.vip, f.other))
	require.Equal(t, "false 0.0000", f.pairOf(f.vip, f.public))
	require.Equal(t, rate("0", "segment", "1.5", "0.8"), rows[0]["rate"], "the answer is the list as saved")
	require.Equal(t, false, rows[1]["accessible"])
	require.Equal(t, "0.25", rows[2]["rate_override"])
	require.Equal(t, true, rows[2]["accessible"])

	put(map[string]any{"group_id": f.other, "rate_override": nil}).ok(200)
	require.Equal(t, "true null", f.pairOf(f.vip, f.other))
	// A pair that says nothing is not kept.
	put(map[string]any{"group_id": f.other, "granted": false}, map[string]any{"group_id": f.granted, "rate_override": nil}).ok(200)
	require.Equal(t, "none", f.pairOf(f.vip, f.other))
	require.Equal(t, "none", f.pairOf(f.vip, f.granted))
	// The items apply in order: the last word on a pair wins.
	put(map[string]any{"group_id": f.other, "granted": true}, map[string]any{"group_id": f.other, "rate_override": "3"}).ok(200)
	require.Equal(t, "true 3.0000", f.pairOf(f.vip, f.other))
	put().ok(200)

	// All or none.
	for _, bad := range []any{"-1", "0.12345", "1000000", "x", ""} {
		put(map[string]any{"group_id": f.public, "granted": true}, map[string]any{"group_id": f.other, "rate_override": bad}).
			invalid("items.1.rate_override")
	}
	put(map[string]any{"group_id": f.public, "granted": true}, map[string]any{"group_id": 999999, "granted": true}).invalid("items.1")
	// The error names the item where the request has it, whatever its id.
	put(map[string]any{"group_id": 999999, "granted": true}, map[string]any{"group_id": f.public, "granted": true}).invalid("items.0")
	require.Equal(t, "false 0.0000", f.pairOf(f.vip, f.public))

	f.do(f.admin, "PUT", "/segments/999999/groups", map[string]any{"items": []any{}}).failed(404, "not_found")
	f.do(f.plain, "PUT", path("/segments/%d/groups", f.vip), map[string]any{"items": []any{}}).failed(403, "permission_denied")
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
	require.Equal(t, rate("2", "group", "2", "1"), rows[0]["rate"])
	require.Equal(t, float64(0), rows[0]["member_count"])
	require.Equal(t, rate("0.5", "segment", "2", "0.8"), rows[1]["rate"])
	require.NotContains(t, rows[1], "price_rule_count")
	require.Equal(t, float64(1), rows[1]["member_count"])
	require.Equal(t, map[string]any{"id": float64(withGrant), "kind": "personal", "name": "admin@x.com", "user_id": float64(f.admin)}, rows[2]["segment"])
	require.Equal(t, true, rows[2]["accessible"])
	require.Equal(t, "0.7", rows[3]["rate_override"])
	require.Equal(t, float64(f.granted), rows[3]["group"].(map[string]any)["id"])

	f.do(f.admin, "GET", "/groups/999999/segments", nil).failed(404, "not_found")
	f.do(f.plain, "GET", path("/groups/%d/segments", f.granted), nil).failed(403, "permission_denied")
}

func TestSetGroupSegments(t *testing.T) {
	f := newSettingsFixture(t)
	def, bare := f.defaultSegment(), f.personal(f.masked)
	put := func(query string, items ...map[string]any) response {
		return f.do(f.admin, "PUT", path("/groups/%d/segments%s", f.other, query), map[string]any{"items": items})
	}

	rows := put(path("?include=%d", bare),
		map[string]any{"segment_id": def, "granted": true},
		map[string]any{"segment_id": f.vip, "rate_override": "0.7"},
	).ok(200).rows()
	require.Equal(t, "true null", f.pairOf(def, f.other))
	require.Equal(t, "false 0.7000", f.pairOf(f.vip, f.other))
	require.Len(t, rows, 3, "the answer keeps the personal segments ?include= names")
	require.Equal(t, true, rows[0]["accessible"])
	require.Equal(t, rate("0.7", "segment", "1", "0.8"), rows[1]["rate"])
	require.Equal(t, float64(bare), rows[2]["segment"].(map[string]any)["id"])

	// A personal segment with a setting is listed without ?include=.
	rows = put("", map[string]any{"segment_id": bare, "granted": true}).ok(200).rows()
	require.Len(t, rows, 3)
	require.Equal(t, true, rows[2]["granted"])

	put("", map[string]any{"segment_id": def, "granted": false}, map[string]any{"segment_id": 999999, "granted": true}).invalid("items.1")
	put("", map[string]any{"segment_id": 999999, "granted": true}, map[string]any{"segment_id": def, "granted": false}).invalid("items.0")
	put("", map[string]any{"segment_id": def, "granted": false}, map[string]any{"segment_id": f.vip, "rate_override": "-1"}).
		invalid("items.1.rate_override")
	require.Equal(t, "true null", f.pairOf(def, f.other), "all or none")

	f.do(f.admin, "PUT", "/groups/999999/segments", map[string]any{"items": []any{}}).failed(404, "not_found")
	f.do(f.plain, "PUT", path("/groups/%d/segments", f.other), map[string]any{"items": []any{}}).failed(403, "permission_denied")
}

// Saves of the same pairs at once, from either side and in either order, all
// go through, and each pair ends as one of them left it.
func TestSettingsSaveRace(t *testing.T) {
	f := newSettingsFixture(t)
	// item grants the pair at the rate; nil says nothing, so the pair goes.
	item := func(key string, id int64, rate any) map[string]any {
		return map[string]any{key: id, "granted": rate != nil, "rate_override": rate}
	}
	ofSegment, ofGroup := path("/segments/%d/groups", f.vip), path("/groups/%d/segments", f.other)
	saves := []struct {
		path  string
		items []map[string]any
	}{
		{ofSegment, []map[string]any{item("group_id", f.other, "0.1"), item("group_id", f.public, "0.1")}},
		{ofSegment, []map[string]any{item("group_id", f.public, "0.2"), item("group_id", f.other, "0.2")}},
		{ofGroup, []map[string]any{item("segment_id", f.vip, "0.3")}},
		{ofGroup, []map[string]any{item("segment_id", f.vip, nil)}},
	}
	for range 10 {
		codes := concurrently(len(saves), func(i int) int {
			return f.do(f.admin, "PUT", saves[i].path, map[string]any{"items": saves[i].items}).code
		})
		require.Equal(t, len(saves), count(codes, 200), "%v", codes)
		require.Contains(t, []string{"true 0.1000", "true 0.2000", "true 0.3000", "none"}, f.pairOf(f.vip, f.other))
		require.Contains(t, []string{"true 0.1000", "true 0.2000"}, f.pairOf(f.vip, f.public))
	}
}

// A caller that may not read users sees a personal segment as "#<user id>":
// neither the rows nor the search give emails away.
func TestPersonalNamesAreMasked(t *testing.T) {
	f := newSettingsFixture(t)
	personal := f.personal(f.plain)
	f.pair(personal, f.granted, true, "")
	masked := map[string]any{"id": float64(personal), "kind": "personal", "name": path("#%d", f.plain), "user_id": float64(f.plain)}
	ref := func(row map[string]any) map[string]any {
		return map[string]any{"id": row["id"], "kind": row["kind"], "name": row["name"], "user_id": row["user_id"]}
	}

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

	got := f.do(f.masked, "GET", path("/segments/%d", personal), nil).ok(200).data()
	require.Equal(t, masked, ref(got))
	require.Equal(t, map[string]any{"id": float64(f.vip), "kind": "named", "name": "vip"}, got["origin"], "the origin is a named segment")
	saved := f.do(f.masked, "PUT", path("/groups/%d/segments", f.granted), map[string]any{"items": []any{
		map[string]any{"segment_id": personal, "rate_override": "0.3"}}}).ok(200).rows()
	require.Equal(t, masked, saved[len(saved)-1]["segment"])
	made := f.do(f.masked, "POST", path("/users/%d/personal-segment", f.admin), map[string]any{"copy": false}).ok(201).data()
	require.Equal(t, path("#%d", f.admin), made["name"])
	require.NotContains(t, made, "user")
}
