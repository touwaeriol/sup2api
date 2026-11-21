package segment

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nameOf(row map[string]any) any { return row["name"] }

func TestSegmentCreateAndRead(t *testing.T) {
	e := setup(t)

	created := e.do(e.admin, "POST", "/segments", map[string]any{"name": "  VIP "}).ok(201).data()
	require.Equal(t, "VIP", created["name"])
	require.Equal(t, "named", created["kind"])
	require.Equal(t, "1", created["discount"])
	require.Equal(t, "", created["description"])
	require.Equal(t, false, created["is_default"])
	require.Equal(t, float64(0), created["member_count"])
	require.Equal(t, []any{}, created["granted_groups"])
	require.NotContains(t, created, "user", "a named segment has no user")
	require.NotContains(t, created, "origin")

	team := e.do(e.admin, "POST", "/segments", map[string]any{"name": "team", "description": "contract", "discount": "0.90"}).ok(201).data()
	require.Equal(t, "0.9", team["discount"])
	require.Equal(t, team, e.do(e.admin, "GET", path("/segments/%d", idOf(team)), nil).ok(200).data())

	list := e.do(e.admin, "GET", "/segments", nil).ok(200)
	require.Equal(t, []any{"默认", "VIP", "team"}, names(list.rows(), nameOf), "the default segment first, then by id")
	require.Equal(t, true, list.rows()[0]["is_default"])
	require.Equal(t, float64(3), list.rows()[0]["member_count"])
	require.Equal(t, float64(3), list.total())

	require.Equal(t, []any{"team"}, names(e.do(e.admin, "GET", "/segments?q=CONTRACT", nil).ok(200).rows(), nameOf))
	paged := e.do(e.admin, "GET", "/segments?page=2&page_size=1", nil).ok(200)
	require.Equal(t, []any{"VIP"}, names(paged.rows(), nameOf))
	require.Equal(t, float64(3), paged.total())

	require.Equal(t, []any{}, e.do(e.admin, "GET", path("/segments/%d/groups", idOf(team)), nil).ok(200).body["data"], "no groups yet")
	e.do(e.admin, "GET", "/segments/999999", nil).failed(404, "not_found")
	e.do(e.plain, "GET", "/segments", nil).failed(403, "permission_denied")

	// ?kind=all: the named ones (the default first), then the personal ones,
	// on the same pages.
	solo := e.personal(e.plain)
	ids := func(query string) []any {
		return names(e.do(e.admin, "GET", "/segments"+query, nil).ok(200).rows(), func(r map[string]any) any { return idOf(r) })
	}
	require.Equal(t, []any{e.defaultSegment(), idOf(created), idOf(team), solo}, ids("?kind=all"))
	require.Equal(t, float64(4), e.do(e.admin, "GET", "/segments?kind=all", nil).ok(200).total())
	require.Equal(t, []any{solo}, ids("?kind=all&page=2&page_size=3"))
	require.Equal(t, []any{solo}, ids("?kind=all&q=plain@"))
	require.Equal(t, []any{e.defaultSegment(), idOf(created), idOf(team)}, ids("?kind=other"), "any other kind is named")
	e.do(e.plain, "POST", "/segments", map[string]any{"name": "x"}).failed(403, "permission_denied")
}

func TestSegmentValidation(t *testing.T) {
	e := setup(t)
	vip := e.segment("VIP", "1")
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"no name", map[string]any{}, "name"},
		{"blank name", map[string]any{"name": "  "}, "name"},
		{"long name", map[string]any{"name": strings.Repeat("长", 101)}, "name"},
		{"name in use, whatever the case", map[string]any{"name": "vip"}, "name"},
		{"zero discount", map[string]any{"name": "a", "discount": "0"}, "discount"},
		{"negative discount", map[string]any{"name": "a", "discount": "-0.5"}, "discount"},
		{"discount that is no number", map[string]any{"name": "a", "discount": "half"}, "discount"},
		{"five decimals", map[string]any{"name": "a", "discount": "0.12345"}, "discount"},
		{"too large", map[string]any{"name": "a", "discount": "1000000"}, "discount"},
	} {
		e.do(e.admin, "POST", "/segments", tc.body).invalid(tc.field)
		if _, hasDiscount := tc.body["discount"]; hasDiscount {
			delete(tc.body, "name")
			e.do(e.admin, "PATCH", path("/segments/%d", vip), tc.body).invalid(tc.field)
		}
	}
	require.Equal(t, "name is required", e.do(e.admin, "POST", "/segments", map[string]any{}).invalid("name")["message"])
	e.do(e.admin, "POST", "/segments", map[string]any{"name": strings.Repeat("长", 100), "discount": "999999.9999"}).ok(201)
	require.Equal(t, int64(3), e.id(`SELECT count(*) FROM segments`), "nothing invalid was stored")
}

func TestSegmentUpdate(t *testing.T) {
	e := setup(t)
	vip, team := e.segment("VIP", "1"), e.segment("team", "1")

	got := e.do(e.admin, "PATCH", path("/segments/%d", vip), map[string]any{"description": "old customers"}).ok(200).data()
	require.Equal(t, "VIP", got["name"], "a field left out stays")
	require.Equal(t, "old customers", got["description"])

	got = e.do(e.admin, "PATCH", path("/segments/%d", vip), map[string]any{"name": "gold", "discount": "0.8500"}).ok(200).data()
	require.Equal(t, "gold", got["name"])
	require.Equal(t, "0.85", got["discount"])
	require.Equal(t, "old customers", got["description"])

	e.do(e.admin, "PATCH", path("/segments/%d", vip), map[string]any{"name": "gold"}).ok(200)
	e.do(e.admin, "PATCH", path("/segments/%d", team), map[string]any{"name": "GOLD"}).invalid("name")
	e.do(e.admin, "PATCH", path("/segments/%d", team), map[string]any{"name": ""}).invalid("name")
	e.do(e.admin, "PATCH", path("/segments/%d", e.defaultSegment()), map[string]any{"name": "everyone"}).ok(200)
	e.do(e.admin, "PATCH", "/segments/999999", map[string]any{"name": "x"}).failed(404, "not_found")
	e.do(e.plain, "PATCH", path("/segments/%d", vip), map[string]any{"name": "x"}).failed(403, "permission_denied")

	personal := e.personal(e.plain)
	for _, body := range []map[string]any{{"name": "mine"}, {"description": "mine"}} {
		e.do(e.admin, "PATCH", path("/segments/%d", personal), body).invalid("name")
	}
	got = e.do(e.admin, "PATCH", path("/segments/%d", personal), map[string]any{"discount": "0.5"}).ok(200).data()
	require.Equal(t, "0.5", got["discount"])
	require.Equal(t, "plain@x.com", got["name"])
}

func TestSegmentCounts(t *testing.T) {
	e := setup(t)
	vip := e.segment("VIP", "1")
	public, private, other := e.group("public", "public", "1"), e.group("private", "restricted", "1"), e.group("other", "restricted", "1")
	e.pair(vip, public, true, "0.5")
	e.pair(vip, private, true, "")
	e.pair(vip, other, false, "0.7")
	e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, vip, []int64{e.plain, e.masked})
	e.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, e.masked)

	got := e.do(e.admin, "GET", path("/segments/%d", vip), nil).ok(200).data()
	require.Equal(t, float64(1), got["member_count"], "a deleted user is no member")
	require.Equal(t, []any{map[string]any{"id": float64(private), "name": "private"}}, got["granted_groups"], "only the private groups it is granted")
	require.Equal(t, float64(2), got["rate_override_count"])
	require.NotContains(t, got, "price_rule_count")

	rows := e.do(e.admin, "GET", "/segments", nil).ok(200).rows()
	require.Equal(t, got, rows[1], "the list carries the same counts")
	require.Equal(t, float64(1), rows[0]["member_count"])
}

func TestSegmentDelete(t *testing.T) {
	e := setup(t)
	vip, def := e.segment("VIP", "0.9"), e.defaultSegment()
	group := e.group("private", "restricted", "1")
	e.pair(vip, group, true, "0.5")
	e.exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) VALUES ($1, 'm1', 'multiplier', 0.8)`, group)
	e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, vip, []int64{e.plain, e.masked})
	e.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, e.masked)
	personal := e.personal(e.plain)

	e.do(e.plain, "DELETE", path("/segments/%d", vip), nil).failed(403, "permission_denied")
	e.do(e.admin, "DELETE", path("/segments/%d", def), nil).failed(409, "conflict")
	e.do(e.admin, "DELETE", path("/segments/%d", personal), nil).failed(409, "conflict")
	e.do(e.admin, "DELETE", "/segments/999999", nil).failed(404, "not_found")

	// A node of the version before segments deletes a user and leaves the
	// personal segment behind: that one can go.
	gone := e.user("gone@x.com")
	orphan := e.personal(gone)
	e.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, gone)
	e.do(e.admin, "DELETE", path("/segments/%d", orphan), nil).ok(204)
	require.Equal(t, def, e.segmentOf(gone), "the deleted user falls back to the default segment")
	require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE id = $1`, orphan))

	e.do(e.admin, "DELETE", path("/segments/%d", vip), nil).ok(204)
	e.do(e.admin, "GET", path("/segments/%d", vip), nil).failed(404, "not_found")
	require.Equal(t, def, e.segmentOf(e.masked), "its members, deleted ones too, fall back to the default segment")
	require.Equal(t, personal, e.segmentOf(e.plain), "a user who left it before stays where they are")
	require.Zero(t, e.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1`, vip))
	require.Equal(t, int64(1), e.id(`SELECT count(*) FROM price_rules`), "the group's prices stay")
	got := e.do(e.admin, "GET", path("/segments/%d", personal), nil).ok(200).data()
	require.Contains(t, got, "origin")
	require.Nil(t, got["origin"], "a personal segment forgets an origin that is gone")
}

func TestSegmentNameRace(t *testing.T) {
	e := setup(t)
	codes := concurrently(8, func(int) int {
		return e.do(e.admin, "POST", "/segments", map[string]any{"name": "VIP"}).code
	})
	require.Equal(t, 1, count(codes, 201), "%v", codes)
	require.Equal(t, 7, count(codes, 400), "the unique index settles it as a field error: %v", codes)
}
