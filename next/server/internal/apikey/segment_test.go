package apikey

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
)

// addKey stores a key of owner in group as the database holds it, without the
// checks of the endpoints, and returns the key.
func (e *env) addKey(owner, group int64) string {
	e.t.Helper()
	raw, err := generateKey()
	require.NoError(e.t, err)
	e.exec(`INSERT INTO api_keys (user_id, group_id, name, key_prefix, key_hash) VALUES ($1, $2, 'k', $3, $4)`,
		owner, group, raw[:displayPrefix], HashKey(raw))
	return raw
}

// keyGroupStatus is the group_status of the user's only key in GET /me/api-keys.
func (e *env) keyGroupStatus(user int64) any {
	e.t.Helper()
	status, out := e.do(user, "GET", "/me/api-keys", nil)
	require.Equal(e.t, 200, status)
	keys := out["data"].([]any)
	require.Len(e.t, keys, 1)
	return keys[0].(map[string]any)["group_status"]
}

// Authentication reads what the owner's segment gets in the key's group: a
// private group needs the grant; the principal carries the group's base rate
// and the segment's own rate there, the latter applying (core.RateLevels).
func TestAuthenticateSegmentTerms(t *testing.T) {
	e := setup(t)
	for _, c := range []struct {
		name                  string
		visibility, groupRate string
		// row is the segment's (granted, rate_override) in the group; nil = none.
		row []any
		// rate is "" when the group is not open to the owner.
		rate, source string
	}{
		{"public, group rate", "public", "1.5", nil, "1.5", core.RateSourceGroup},
		{"public, own rate", "public", "1.5", []any{false, "2.5"}, "2.5", core.RateSourceSegment},
		{"public, own rate 0 is free", "public", "1.5", []any{false, "0"}, "0", core.RateSourceSegment},
		{"private granted, group rate", "restricted", "2", []any{true, nil}, "2", core.RateSourceGroup},
		{"private granted, own rate", "restricted", "2", []any{true, "0.25"}, "0.25", core.RateSourceSegment},
		{"private granted, own rate 0 is free", "restricted", "2", []any{true, "0"}, "0", core.RateSourceSegment},
		{"private, nothing set", "restricted", "2", nil, "", ""},
		{"private, own rate without the grant", "restricted", "2", []any{false, "0.25"}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			group := e.exec1(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ($1, $2, $3::numeric) RETURNING id`, c.name, c.visibility, c.groupRate)
			seg := e.exec1(`INSERT INTO segments (name) VALUES ($1) RETURNING id`, c.name)
			e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.user, seg)
			if c.row != nil {
				e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, $3, $4::numeric)`, seg, group, c.row[0], c.row[1])
			}
			p, err := e.svc.Authenticate(t.Context(), e.addKey(e.user, group))
			// Authentication and the key endpoints tell alike whether the
			// owner may use the group: by the one rule, segment.OpenToKeyOwner.
			var open bool
			require.NoError(t, e.db.Tx(t.Context(), func(tx *gorm.DB) (err error) {
				open, err = groupAvailable(t.Context(), tx, e.user, group)
				return err
			}))
			require.Equal(t, c.rate != "", open)
			if c.rate == "" {
				require.Equal(t, CodeGroupNotAllowed, codeOf(err))
				return
			}
			require.NoError(t, err)
			rate, source := p.Rates().Rate()
			require.Equal(t, []any{seg, c.groupRate, c.rate, c.source}, []any{p.SegmentID, p.Group.RateMultiplier.String(), rate.String(), source})
		})
	}
}

// Authentication asks whether the owner may use the key's group by the one
// rule every other reader uses (CONTRACTS §97.9), not a copy of it that a
// change to the rule would leave behind.
func TestLookupUsesOpenToUser(t *testing.T) {
	require.Contains(t, lookupColumns, segment.OpenToKeyOwner)
	require.Contains(t, selectKey, segment.OpenToKeyOwner)
}

// Nothing of a segment is cached: moving the owner to another segment, or
// changing what the segment gets in the group, shows on the next
// authentication and in the key list.
func TestSegmentChangeAppliesAtOnce(t *testing.T) {
	e := setup(t)
	vip := e.exec1(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('vip', 'restricted', 2) RETURNING id`)
	team := e.exec1(`INSERT INTO segments (name) VALUES ('team') RETURNING id`)
	solo := e.exec1(`INSERT INTO segments (name) VALUES ('solo') RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $3, true, NULL), ($2, $3, true, 3)`, team, solo, vip)
	raw := e.addKey(e.user, vip)

	move := `UPDATE users SET segment_id = $2 WHERE id = $1`
	for _, step := range []struct {
		name string
		sql  string // "" = nothing changes
		args []any
		// rate is "" when the group is not open to the owner.
		rate, source string
	}{
		{"the default segment has no grant", "", nil, "", ""},
		{"moved to a granted segment", move, []any{e.user, team}, "2", core.RateSourceGroup},
		{"moved to a segment with its own rate", move, []any{e.user, solo}, "3", core.RateSourceSegment},
		{"own rate removed", `UPDATE segment_groups SET rate_override = NULL WHERE segment_id = $1`, []any{solo}, "2", core.RateSourceGroup},
		{"own rate set", `UPDATE segment_groups SET rate_override = 1.5 WHERE segment_id = $1`, []any{solo}, "1.5", core.RateSourceSegment},
		{"grant revoked", `UPDATE segment_groups SET granted = false WHERE segment_id = $1`, []any{solo}, "", ""},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.sql != "" {
				e.exec(step.sql, step.args...)
			}
			p, err := e.svc.Authenticate(t.Context(), raw)
			if step.rate == "" {
				require.Equal(t, CodeGroupNotAllowed, codeOf(err))
				require.Equal(t, "unavailable", e.keyGroupStatus(e.user))
				return
			}
			require.NoError(t, err)
			rate, source := p.Rates().Rate()
			require.Equal(t, []any{step.rate, step.source}, []any{rate.String(), source})
			require.Equal(t, "active", e.keyGroupStatus(e.user))
		})
	}
}

// A key is created in, or moved to, a private group only when its owner's
// segment is granted the group; a rate of the segment there is not a grant.
func TestKeyGroupNeedsSegmentGrant(t *testing.T) {
	e := setup(t)
	pub := e.exec1(`INSERT INTO groups (name) VALUES ('pub') RETURNING id`)
	vip := e.exec1(`INSERT INTO groups (name, visibility) VALUES ('vip', 'restricted') RETURNING id`)
	team := e.exec1(`INSERT INTO segments (name) VALUES ('team') RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted) VALUES ($1, $2, true)`, team, vip)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, rate_override) SELECT segment_id, $2, 0.5 FROM users WHERE id = $1`, e.user, vip)

	status, out := e.do(e.user, "POST", "/me/api-keys", map[string]any{"name": "laptop", "group_id": pub})
	require.Equal(t, 201, status, out)
	path := fmt.Sprintf("/me/api-keys/%d", int64(out["data"].(map[string]any)["id"].(float64)))
	refused := func(method, path string, body map[string]any) {
		t.Helper()
		status, out := e.do(e.user, method, path, body)
		require.Equal(t, 400, status, out)
		field := out["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)[0].(map[string]any)
		require.Equal(t, []any{"group_id", "not_available"}, []any{field["field"], field["code"]})
	}
	refused("POST", "/me/api-keys", map[string]any{"name": "vip", "group_id": vip})
	refused("PATCH", path, map[string]any{"group_id": vip})

	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.user, team)
	status, out = e.do(e.user, "PATCH", path, map[string]any{"group_id": vip})
	require.Equal(t, 200, status, out)
	require.Equal(t, "active", out["data"].(map[string]any)["group_status"])
	status, out = e.do(e.user, "POST", "/me/api-keys", map[string]any{"name": "vip", "group_id": vip})
	require.Equal(t, 201, status, out)
	require.Equal(t, "active", out["data"].(map[string]any)["group_status"])

	// The grant is the segment's: a user of another segment is still refused.
	status, _ = e.do(e.admin, "POST", "/me/api-keys", map[string]any{"name": "vip", "group_id": vip})
	require.Equal(t, 400, status)
}
