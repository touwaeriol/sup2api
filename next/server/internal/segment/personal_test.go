package segment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMakePersonal(t *testing.T) {
	e := setup(t)
	vip := e.segment("VIP", "0.9")
	public, private := e.group("public", "public", "1"), e.group("private", "restricted", "2")
	e.pair(vip, private, true, "")
	e.pair(vip, public, false, "0.5")
	e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, vip, []int64{e.plain, e.masked})
	personalize := func(user int64, copySettings bool) response {
		return e.do(e.admin, "POST", path("/users/%d/personal-segment", user), map[string]any{"copy": copySettings})
	}

	// From scratch: nothing of the segment the user leaves comes along.
	blank := personalize(e.plain, false).ok(201).data()
	require.Equal(t, "personal", blank["kind"])
	require.Equal(t, "plain@x.com", blank["name"], "a personal segment goes by its user's email")
	require.Equal(t, float64(e.plain), blank["user_id"])
	require.Equal(t, map[string]any{"id": float64(e.plain), "email": "plain@x.com", "display_name": "name of plain@x.com"}, blank["user"])
	require.Equal(t, map[string]any{"id": float64(vip), "kind": "named", "name": "VIP"}, blank["origin"])
	require.Equal(t, "1", blank["discount"])
	require.Equal(t, float64(1), blank["member_count"])
	require.Equal(t, []any{}, blank["granted_groups"])
	require.Equal(t, []any{}, blank["rate_overrides"])
	require.NotContains(t, blank, "price_rule_count")
	require.Equal(t, idOf(blank), e.segmentOf(e.plain))

	// A copy starts from the discount, grants and rates of that segment.
	copied := personalize(e.masked, true).ok(201).data()
	require.Equal(t, "0.9", copied["discount"])
	require.Equal(t, []any{map[string]any{"id": float64(private), "name": "private"}}, copied["granted_groups"])
	require.Equal(t, []any{map[string]any{"id": float64(public), "name": "public", "rate": "0.5"}}, copied["rate_overrides"])
	require.Equal(t, int64(1), e.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1 AND group_id = $2 AND NOT granted AND rate_override = 0.5`,
		idOf(copied), public))
	require.Equal(t, int64(2), e.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1`, vip), "the origin keeps its own")

	personalize(e.plain, true).failed(409, "conflict")
	personalize(999999, false).failed(404, "not_found")
	gone := e.user("gone@x.com")
	e.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, gone)
	personalize(gone, false).failed(404, "not_found")
	e.do(e.plain, "POST", path("/users/%d/personal-segment", e.masked), map[string]any{"copy": false}).failed(403, "permission_denied")

	list := e.do(e.masked, "GET", "/segments?kind=personal", nil)
	require.Equal(t, float64(2), list.ok(200).total())
}

func TestMakePersonalRace(t *testing.T) {
	e := setup(t)
	codes := concurrently(8, func(int) int {
		return e.do(e.admin, "POST", path("/users/%d/personal-segment", e.plain), map[string]any{"copy": true}).code
	})
	require.Equal(t, 1, count(codes, 201), "%v", codes)
	require.Equal(t, 7, count(codes, 409), "one personal segment per user: %v", codes)
	require.Equal(t, int64(1), e.id(`SELECT count(*) FROM segments WHERE kind = 'personal'`))
}

// Moving one personal user back twice at once, to different segments: one
// request does it, the other finds no personal segment left.
func TestRemovePersonalRace(t *testing.T) {
	e := setup(t)
	targets := []string{"", path("?to=%d", e.segment("VIP", "1"))}
	for round := range 10 {
		e.personal(e.plain)
		codes := concurrently(4, func(i int) int {
			return e.do(e.admin, "DELETE", path("/users/%d/personal-segment%s", e.plain, targets[i%2]), nil).code
		})
		require.Equal(t, 1, count(codes, 204), "round %d: %v", round, codes)
		require.Equal(t, 3, count(codes, 404), "round %d: %v", round, codes)
		require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE kind = 'personal'`))
	}
}

func TestRemovePersonal(t *testing.T) {
	e := setup(t)
	vip, def := e.segment("VIP", "1"), e.defaultSegment()
	group := e.group("private", "restricted", "1")
	remove := func(user int64, query string) response {
		return e.do(e.admin, "DELETE", path("/users/%d/personal-segment%s", user, query), nil)
	}
	// personalWithSettings makes the user personal with a grant and a rate.
	personalWithSettings := func(user int64) int64 {
		id := e.personal(user)
		e.pair(id, group, true, "0.5")
		return id
	}

	// Without ?to= the user goes back to the default segment.
	first := personalWithSettings(e.plain)
	remove(e.plain, "").ok(204)
	require.Equal(t, def, e.segmentOf(e.plain))
	require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE id = $1`, first), "the personal segment is deleted")
	require.Zero(t, e.id(`SELECT count(*) FROM segment_groups`), "with its grants and rates")
	remove(e.plain, "").failed(404, "not_found")

	// ?to= must name a named segment.
	personalWithSettings(e.plain)
	other := e.personal(e.masked)
	for _, to := range []string{"999999", "x", path("%d", other)} {
		remove(e.plain, "?to="+to).invalid("to")
	}
	require.NotEqual(t, def, e.segmentOf(e.plain), "nothing moved")
	remove(e.plain, path("?to=%d", vip)).ok(204)
	require.Equal(t, vip, e.segmentOf(e.plain))

	remove(999999, "").failed(404, "not_found")
	e.do(e.plain, "DELETE", path("/users/%d/personal-segment", e.masked), nil).failed(403, "permission_denied")
}
