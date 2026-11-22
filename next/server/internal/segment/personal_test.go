package segment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// personalFixture: plain and masked are in segment vip, which is granted
// private and has a rate and a model rate in public.
type personalFixture struct {
	*env
	vip, public, private int64
}

func newPersonalFixture(t *testing.T) personalFixture {
	f := personalFixture{env: setup(t)}
	f.vip, f.public, f.private = f.segment("VIP"), f.group("public", "public", "1"), f.group("private", "restricted", "2")
	f.pair(f.vip, f.private, true, "")
	f.pair(f.vip, f.public, false, "0.5")
	f.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'm', 0.4)`, f.vip, f.public)
	f.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, f.vip, []int64{f.plain, f.masked})
	return f
}

func (f personalFixture) personalize(user int64, copySettings bool) response {
	return f.do(f.admin, "POST", path("/users/%d/personal-segment", user), map[string]any{"copy": copySettings})
}

// From scratch: nothing of the segment the user leaves comes along.
func TestMakePersonal(t *testing.T) {
	f := newPersonalFixture(t)
	blank := f.personalize(f.plain, false).ok(201).data()
	require.Equal(t, "personal", blank["kind"])
	require.Equal(t, "plain@x.com", blank["name"], "a personal segment goes by its user's email")
	require.Equal(t, float64(f.plain), blank["user_id"])
	require.Equal(t, map[string]any{"id": float64(f.plain), "email": "plain@x.com", "display_name": "name of plain@x.com"}, blank["user"])
	require.Equal(t, map[string]any{"id": float64(f.vip), "kind": "named", "name": "VIP"}, blank["origin"])
	require.Equal(t, float64(1), blank["member_count"])
	require.Equal(t, []any{}, blank["granted_groups"])
	require.Equal(t, []any{}, blank["rate_overrides"])
	require.Equal(t, idOf(blank), f.segmentOf(f.plain))
	require.Equal(t, int64(0), f.id(`SELECT count(*) FROM segment_model_rates WHERE segment_id = $1`, idOf(blank)))
	require.Equal(t, int32(0), f.ratesChanged.Load(), "no model rates came along")
	require.Equal(t, float64(1), f.do(f.masked, "GET", "/segments?kind=personal", nil).ok(200).total())
}

// A copy starts from the grants, rates and model rates of that segment.
func TestMakePersonalCopy(t *testing.T) {
	f := newPersonalFixture(t)
	copied := f.personalize(f.masked, true).ok(201).data()
	require.Equal(t, []any{map[string]any{"id": float64(f.private), "name": "private"}}, copied["granted_groups"])
	require.Equal(t, []any{map[string]any{"id": float64(f.public), "name": "public", "rate": "0.5"}}, copied["rate_overrides"])
	require.Equal(t, int64(1), f.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1 AND group_id = $2 AND NOT granted AND rate_override = 0.5`,
		idOf(copied), f.public))
	require.Equal(t, int64(2), f.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1`, f.vip), "the origin keeps its own")
	require.Equal(t, int64(1), f.id(`SELECT count(*) FROM segment_model_rates WHERE segment_id = $1 AND group_id = $2 AND model = 'm' AND rate = 0.4`,
		idOf(copied), f.public))
	require.Equal(t, int64(1), f.id(`SELECT count(*) FROM segment_model_rates WHERE segment_id = $1`, f.vip), "the origin keeps its own")
	require.Equal(t, int32(1), f.ratesChanged.Load(), "billing hears of the copied model rates")
}

func TestMakePersonalRefusals(t *testing.T) {
	f := newPersonalFixture(t)
	f.personalize(f.plain, false).ok(201)
	f.personalize(f.plain, true).failed(409, "conflict")
	f.personalize(999999, false).failed(404, "not_found")
	gone := f.user("gone@x.com")
	f.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, gone)
	f.personalize(gone, false).failed(404, "not_found")
	f.do(f.plain, "POST", path("/users/%d/personal-segment", f.masked), map[string]any{"copy": false}).failed(403, "permission_denied")
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
	targets := []string{"", path("?to=%d", e.segment("VIP"))}
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

// removePersonal moves the user out of their personal segment as the
// admin, with the query.
func (e *env) removePersonal(user int64, query string) response {
	return e.do(e.admin, "DELETE", path("/users/%d/personal-segment%s", user, query), nil)
}

// personalWithSettings makes the user personal with a grant and a rate in a
// new group.
func (e *env) personalWithSettings(user int64) int64 {
	id := e.personal(user)
	e.pair(id, e.group(path("private %d", id), "restricted", "1"), true, "0.5")
	return id
}

// Without ?to= the user goes back to the default segment.
func TestRemovePersonal(t *testing.T) {
	e := setup(t)
	first := e.personalWithSettings(e.plain)
	e.removePersonal(e.plain, "").ok(204)
	require.Equal(t, e.defaultSegment(), e.segmentOf(e.plain))
	require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE id = $1`, first), "the personal segment is deleted")
	require.Zero(t, e.id(`SELECT count(*) FROM segment_groups`), "with its grants and rates")
	e.removePersonal(e.plain, "").failed(404, "not_found")
	e.removePersonal(999999, "").failed(404, "not_found")
	e.do(e.plain, "DELETE", path("/users/%d/personal-segment", e.masked), nil).failed(403, "permission_denied")
}

// ?to= must name a named segment.
func TestRemovePersonalTo(t *testing.T) {
	e := setup(t)
	vip := e.segment("VIP")
	e.personalWithSettings(e.plain)
	other := e.personal(e.masked)
	for _, to := range []string{"999999", "x", path("%d", other)} {
		e.removePersonal(e.plain, "?to="+to).invalid("to")
	}
	require.NotEqual(t, e.defaultSegment(), e.segmentOf(e.plain), "nothing moved")
	e.removePersonal(e.plain, path("?to=%d", vip)).ok(204)
	require.Equal(t, vip, e.segmentOf(e.plain))
}
