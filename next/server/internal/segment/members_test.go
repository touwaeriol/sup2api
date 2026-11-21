package segment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

func TestAddMembers(t *testing.T) {
	e := setup(t)
	vip, def := e.segment("VIP", "1"), e.defaultSegment()
	group := e.group("private", "restricted", "1")
	third := e.user("third@x.com")
	moved := func(segment int64, users ...int64) any {
		return e.do(e.admin, "POST", path("/segments/%d/members", segment), map[string]any{"user_ids": users}).ok(200).data()["moved"]
	}

	require.Equal(t, float64(2), moved(vip, e.plain, e.masked, e.plain), "a user named twice moves once")
	require.Equal(t, vip, e.segmentOf(e.plain))
	require.Equal(t, vip, e.segmentOf(e.masked))
	require.Equal(t, float64(1), moved(vip, e.plain, third), "a user already there does not move")
	require.Equal(t, float64(0), moved(vip))

	// A personal user who is moved loses the personal segment and its settings.
	personal := e.personal(e.plain)
	e.pair(personal, group, true, "0.5")
	require.Equal(t, float64(1), moved(def, e.plain))
	require.Equal(t, def, e.segmentOf(e.plain))
	require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE id = $1`, personal))
	require.Zero(t, e.id(`SELECT count(*) FROM segment_groups`))

	// Nobody moves when one of the users is unknown; a deleted user is.
	e.exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, e.masked)
	for _, unknown := range []int64{999999, e.masked} {
		r := e.do(e.admin, "POST", path("/segments/%d/members", def), map[string]any{"user_ids": []int64{third, unknown}})
		require.Equal(t, path("unknown user %d", unknown), r.invalid("user_ids")["message"])
	}
	require.Equal(t, vip, e.segmentOf(third))
	require.Equal(t, vip, e.segmentOf(e.masked), "a deleted user stays where they were")

	personal = e.personal(e.plain)
	e.do(e.admin, "POST", path("/segments/%d/members", personal), map[string]any{"user_ids": []int64{third}}).failed(409, "conflict")
	e.do(e.admin, "POST", "/segments/999999/members", map[string]any{"user_ids": []int64{third}}).failed(404, "not_found")
	e.do(e.plain, "POST", path("/segments/%d/members", vip), map[string]any{"user_ids": []int64{third}}).failed(403, "permission_denied")
}

// Deleting a segment while users are moved into it and its members are made
// personal settles as a conflict or a missing row, never as a failure, and
// leaves every personal segment with its user in it.
func TestSegmentDeleteRace(t *testing.T) {
	e := setup(t)
	for round := range 5 {
		vip := e.segment(path("vip %d", round), "0.9")
		users := []int64{e.user(path("a%d@x.com", round)), e.user(path("b%d@x.com", round)), e.user(path("c%d@x.com", round))}
		e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, vip, users)
		calls := []func() int{
			func() int { return e.do(e.admin, "DELETE", path("/segments/%d", vip), nil).code },
			func() int {
				return e.do(e.admin, "POST", path("/segments/%d/members", vip), map[string]any{"user_ids": []int64{e.plain, users[2], users[0]}}).code
			},
			func() int {
				return e.do(e.admin, "POST", path("/segments/%d/members", e.defaultSegment()), map[string]any{"user_ids": users}).code
			},
		}
		for _, u := range users {
			calls = append(calls, func() int {
				return e.do(e.admin, "POST", path("/users/%d/personal-segment", u), map[string]any{"copy": true}).code
			})
		}
		codes := concurrently(len(calls), func(i int) int { return calls[i]() })
		for _, code := range codes {
			require.Contains(t, []int{200, 201, 204, 404, 409}, code, "%v", codes)
		}
		require.Equal(t, 204, codes[0], "nothing keeps the delete from going through: %v", codes)
		require.Zero(t, e.id(`SELECT count(*) FROM segments s JOIN users u ON u.id = s.user_id WHERE u.segment_id <> s.id`))
	}
}

// A mover holding the users of personal segments that came from a segment
// keeps the segment's delete waiting before it touches those segments: the
// delete clears their origin_id, the mover deletes them (DropPersonal), and
// without the users' locks the two took the rows in different orders and
// deadlocked (one of them failed with 409). The mover here deletes the
// segment the delete's cascade reaches last before the delete starts, and
// the others once the delete waits.
func TestSegmentDeleteWaitsForMovers(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	vip := e.segment("vip", "0.9")
	users := []int64{e.user("a@x.com"), e.user("b@x.com"), e.user("c@x.com")}
	e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2)`, vip, users)
	for _, u := range users {
		e.do(e.admin, "POST", path("/users/%d/personal-segment", u), map[string]any{"copy": true}).ok(201)
	}
	last := e.id(`SELECT user_id FROM segments WHERE origin_id = $1 ORDER BY ctid DESC LIMIT 1`, vip)

	holding, waiting := make(chan struct{}), make(chan struct{})
	moved := make(chan error, 1)
	go func() {
		moved <- e.db.Tx(ctx, func(tx *gorm.DB) error {
			if _, err := lockUsers(ctx, tx, `id = ANY(?)`, store.Int64s(users)); err != nil {
				return err
			}
			move := func(ids ...int64) error { // as moveUsers, past its lock
				if err := setUserSegment(store.G(ctx, tx), gorm.Expr("default_segment_id()"), "id = ANY(?)", store.Int64s(ids)).Error; err != nil {
					return err
				}
				return DropPersonal(ctx, tx, ids...)
			}
			if err := move(last); err != nil {
				return err
			}
			close(holding)
			<-waiting
			return move(users...)
		})
	}()
	select {
	case <-holding:
	case err := <-moved:
		t.Fatalf("the mover failed: %v", err)
	}
	deleted := make(chan int, 1)
	go func() { deleted <- e.do(e.admin, "DELETE", path("/segments/%d", vip), nil).code }()
	require.Eventually(t, func() bool {
		return e.id(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0
	}, 10*time.Second, 5*time.Millisecond, "the delete never waited")
	close(waiting)
	require.NoError(t, <-moved)
	require.Equal(t, 204, <-deleted)
	require.Zero(t, e.id(`SELECT count(*) FROM segments WHERE id = $1 OR origin_id = $1`, vip))
	for _, u := range users {
		require.Equal(t, e.defaultSegment(), e.segmentOf(u))
	}
}

// Every writer of the package at once, on the same users, segments and
// groups: none fails, and what they leave is consistent.
func TestMixedWritersRace(t *testing.T) {
	e := setup(t)
	groups := []int64{e.group("g1", "restricted", "1"), e.group("g2", "restricted", "2"), e.group("g3", "public", "1")}
	users := make([]int64, 12)
	for i := range users {
		users[i] = e.user(path("s%d@x.com", i))
	}
	call := func(method, path string, body any) func() int {
		return func() int { return e.do(e.admin, method, path, body).code }
	}
	for round := range 25 {
		a, b := e.segment(path("a %d", round), "0.9"), e.segment(path("b %d", round), "0.8")
		e.exec(`UPDATE users SET segment_id = $1 WHERE id = ANY($2) AND segment_id IN (SELECT id FROM segments WHERE kind = 'named')`, a, users[:6])
		e.pair(a, groups[0], true, "0.5")
		calls := []func() int{
			call("DELETE", path("/segments/%d", a), nil),
			call("DELETE", path("/segments/%d", b), nil),
			call("POST", path("/segments/%d/members", b), map[string]any{"user_ids": users}),
			call("POST", path("/segments/%d/members", a), map[string]any{"user_ids": users}),
			call("POST", path("/segments/%d/members", e.defaultSegment()), map[string]any{"user_ids": users[3:9]}),
			call("GET", "/segments?kind=personal", nil),
		}
		for _, u := range users[:8] {
			calls = append(calls,
				call("POST", path("/users/%d/personal-segment", u), map[string]any{"copy": true}),
				call("DELETE", path("/users/%d/personal-segment?to=%d", u, b), nil))
		}
		for _, g := range groups {
			calls = append(calls, call("PUT", path("/groups/%d/segments", g), map[string]any{"items": []any{
				map[string]any{"segment_id": b, "granted": true, "rate_override": "0.3"},
				map[string]any{"segment_id": a, "granted": false, "rate_override": nil},
			}}))
		}
		for _, s := range []int64{a, b} {
			calls = append(calls,
				call("GET", path("/segments/%d/groups", s), nil),
				call("PUT", path("/segments/%d/groups", s), map[string]any{"items": []any{
					map[string]any{"group_id": groups[2], "rate_override": "0.7"},
					map[string]any{"group_id": groups[0], "granted": true},
					map[string]any{"group_id": groups[1], "granted": false, "rate_override": nil},
				}}))
		}
		codes := concurrently(len(calls), func(i int) int { return calls[i]() })
		for _, code := range codes {
			require.Less(t, code, 500, "round %d: %v", round, codes)
		}
		require.Zero(t, e.id(`SELECT count(*) FROM segments s JOIN users u ON u.id = s.user_id WHERE u.segment_id <> s.id`),
			"a personal segment without its user")
		require.Zero(t, e.id(`SELECT count(*) FROM users u JOIN segments s ON s.id = u.segment_id WHERE s.kind = 'personal' AND s.user_id <> u.id`),
			"a user in the personal segment of another")
		require.Zero(t, e.id(`SELECT count(*) FROM segment_groups WHERE NOT granted AND rate_override IS NULL`), "a pair that says nothing")
	}
}
