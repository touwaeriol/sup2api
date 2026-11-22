package iam

import (
	"context"
	"fmt"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// console is a bootstrapped service behind its routes, with the id and the
// token of the super admin.
type console struct {
	*env
	client
	root  int64
	token string
}

func newConsole(t *testing.T) *console {
	t.Helper()
	e := setup(t)
	require.NoError(t, e.svc.Bootstrap(context.Background()))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := httpapi.NewRouter(engine, e.svc, e.authz)
	e.svc.RegisterRoutes(r)
	e.authz.RegisterRoutes(r)
	c := &console{env: e, client: client{t, engine}}
	c.root, c.token = c.login(adminEmail, adminPassword)
	return c
}

func (c *console) login(email, password string) (int64, string) {
	c.t.Helper()
	code, out := c.do("POST", "/auth/login", "", map[string]any{"email": email, "password": password})
	require.Equal(c.t, 200, code, "login %s: %v", email, out)
	data := out["data"].(map[string]any)
	return int64(data["user"].(map[string]any)["id"].(float64)), data["access_token"].(string)
}

// one runs a statement that returns an id.
func (c *console) one(sql string, args ...any) int64 {
	c.t.Helper()
	var id int64
	require.NoError(c.t, c.svc.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id))
	return id
}

// user creates a user in the segment (0 = the default one) and returns its id.
func (c *console) user(email string, segment int64) int64 {
	c.t.Helper()
	in := CreateUserInput{Email: email, Password: "password-1"}
	if segment != 0 {
		in.SegmentID = &segment
	}
	u, err := c.svc.CreateUser(context.Background(), c.root, in)
	require.NoError(c.t, err)
	return u.ID
}

// personal gives the user a segment of their own and returns its id.
func (c *console) personal(user int64) int64 {
	c.t.Helper()
	id := c.one(`INSERT INTO segments (kind, user_id, origin_id) VALUES ('personal', $1, default_segment_id()) RETURNING id`, user)
	c.one(`UPDATE users SET segment_id = $2 WHERE id = $1 RETURNING id`, user, id)
	return id
}

// as returns the token of a new user whose only role grants permissions.
func (c *console) as(name string, permissions ...string) string {
	c.t.Helper()
	code, out := c.do("POST", "/roles", c.token, map[string]any{"key": name, "name": map[string]string{"en": name, "zh": name}, "permission_keys": permissions})
	require.Equal(c.t, 201, code, "role %s: %v", name, out)
	email := name + "@example.com"
	_, err := c.svc.CreateUser(context.Background(), c.root, CreateUserInput{Email: email, Password: "password-1", RoleKeys: []string{name}})
	require.NoError(c.t, err)
	_, token := c.login(email, "password-1")
	return token
}

// emails lists the emails of a users list response.
func emails(out map[string]any) []string {
	got := []string{}
	for _, it := range out["data"].([]any) {
		got = append(got, it.(map[string]any)["email"].(string))
	}
	return got
}

// errDetails returns the details of an error response, nil without one.
func errDetails(out map[string]any) map[string]any {
	e, _ := out["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	return details
}

func TestUserSegment(t *testing.T) {
	t.Parallel()
	c := newConsole(t)
	def := c.one(`SELECT default_segment_id()`)
	vip := c.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	c.user("alice@example.com", 0)
	bob := c.user("bob@example.com", vip)
	carol := c.user("carol@example.com", 0)
	own := c.personal(carol)

	code, out := c.do("GET", fmt.Sprintf("/users/%d", bob), c.token, nil)
	require.Equal(t, 200, code)
	require.Equal(t, map[string]any{"id": float64(vip), "kind": core.SegmentNamed, "name": "vip"}, out["data"].(map[string]any)["segment"])
	require.NotContains(t, out["data"], "group_ids")
	code, out = c.do("GET", fmt.Sprintf("/users/%d", carol), c.token, nil)
	require.Equal(t, 200, code)
	require.Equal(t, map[string]any{"id": float64(own), "kind": core.SegmentPersonal, "name": "carol@example.com", "user_id": float64(carol)},
		out["data"].(map[string]any)["segment"], "a personal segment goes by its user's email")

	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"no filter", "", []string{adminEmail, "alice@example.com", "bob@example.com", "carol@example.com"}},
		{"a named segment", fmt.Sprintf("?segment=%d", vip), []string{"bob@example.com"}},
		{"the default segment", fmt.Sprintf("?segment=%d", def), []string{adminEmail, "alice@example.com"}},
		{"every personal segment", "?segment=personal", []string{"carol@example.com"}},
		{"one personal segment", fmt.Sprintf("?segment=%d", own), []string{"carol@example.com"}},
		{"with the other filters", fmt.Sprintf("?segment=%d&q=alice", def), []string{"alice@example.com"}},
		{"not a segment", "?segment=nope", []string{}},
	} {
		code, out := c.do("GET", "/users"+tc.query, c.token, nil)
		require.Equal(t, 200, code, tc.name)
		require.Equal(t, tc.want, emails(out), tc.name)
		require.EqualValues(t, len(tc.want), out["page"].(map[string]any)["total"], tc.name)
	}
	_, out = c.do("GET", fmt.Sprintf("/users?segment=%d&page_size=1&page=2", def), c.token, nil)
	require.Equal(t, []string{"alice@example.com"}, emails(out))
	require.EqualValues(t, 2, out["page"].(map[string]any)["total"])
}

func TestCreateUserSegment(t *testing.T) {
	t.Parallel()
	c := newConsole(t)
	def := c.one(`SELECT default_segment_id()`)
	vip := c.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	own := c.personal(c.user("carol@example.com", 0))
	// Creating a user grants the default role: the creator holds what it grants.
	creator := c.as("creator", "user:create", "apikey:self:manage", "balance:self:read", "usage:self:read", "gateway:use")

	for _, tc := range []struct {
		name, token string
		segment     any // segment_id of the body; nil = left out
		code        int
		want        int64 // the segment the user lands in
	}{
		{"without a segment", c.token, nil, 201, def},
		{"a named segment", c.token, vip, 201, vip},
		{"a personal segment", c.token, own, 400, 0},
		{"a segment that does not exist", c.token, 999999, 400, 0},
		{"the default without segment:manage", creator, nil, 201, def},
		{"a segment without segment:manage", creator, vip, 403, 0},
	} {
		body := map[string]any{"email": fmt.Sprintf("new%d@example.com", c.one(`SELECT count(*) FROM users`)), "password": "password-1"}
		if tc.segment != nil {
			body["segment_id"] = tc.segment
		}
		code, out := c.do("POST", "/users", tc.token, body)
		require.Equal(t, tc.code, code, "%s: %v", tc.name, out)
		details := errDetails(out)
		switch code {
		case 201:
			require.EqualValues(t, tc.want, out["data"].(map[string]any)["segment"].(map[string]any)["id"], tc.name)
		case 400:
			require.Equal(t, "segment_id", details["fields"].([]any)[0].(map[string]any)["field"], tc.name)
			require.Equal(t, "用户组不存在", details["fields"].([]any)[0].(map[string]any)["message"], tc.name)
		case 403:
			require.Equal(t, "segment:manage", details["permission"], tc.name)
		}
	}
}

func TestDeleteUserLeavesSegment(t *testing.T) {
	t.Parallel()
	c := newConsole(t)
	ctx := context.Background()
	def := c.one(`SELECT default_segment_id()`)
	vip := c.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	bob := c.user("bob@example.com", vip)
	carol := c.user("carol@example.com", 0)
	own := c.personal(carol)
	group := c.one(`INSERT INTO groups (name, visibility) VALUES ('private', 'restricted') RETURNING id`)
	c.one(`INSERT INTO segment_groups (segment_id, group_id, granted) VALUES ($1, $2, true) RETURNING group_id`, own, group)

	require.NoError(t, c.svc.DeleteUser(ctx, c.root, carol))
	require.NoError(t, c.svc.DeleteUser(ctx, c.root, bob))

	for _, id := range []int64{carol, bob} {
		require.Equal(t, def, c.one(`SELECT segment_id FROM users WHERE id = $1`, id), "a deleted user is back in the default segment")
	}
	require.Zero(t, c.one(`SELECT count(*) FROM segments WHERE id = $1`, own), "the personal segment goes with its user")
	require.Zero(t, c.one(`SELECT count(*) FROM segment_groups WHERE segment_id = $1`, own))
	require.EqualValues(t, 1, c.one(`SELECT count(*) FROM segments WHERE id = $1`, vip), "a named segment outlives its members")
}

func TestSegmentMembers(t *testing.T) {
	t.Parallel()
	c := newConsole(t)
	vip := c.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	for _, name := range []string{"ann", "bea", "cy"} {
		c.user(name+"@example.com", vip)
	}
	gone := c.user("gone@example.com", vip)
	require.NoError(t, c.svc.DeleteUser(context.Background(), c.root, gone))
	own := c.personal(c.user("carol@example.com", 0))
	members := fmt.Sprintf("/segments/%d/members", vip)

	for _, tc := range []struct {
		name, token string
		missing     string // the permission the caller lacks
	}{
		{"both permissions", c.as("both", "segment:read", "user:read"), ""},
		{"only segment:read", c.as("segments", "segment:read"), "user:read"},
		{"only user:read", c.as("users", "user:read"), "segment:read"},
	} {
		code, out := c.do("GET", members, tc.token, nil)
		if tc.missing == "" {
			require.Equal(t, 200, code, tc.name)
			continue
		}
		require.Equal(t, 403, code, tc.name)
		require.Equal(t, tc.missing, errDetails(out)["permission"], tc.name)
	}

	code, out := c.do("GET", members+"?page_size=2", c.token, nil)
	require.Equal(t, 200, code)
	require.Equal(t, []string{"ann@example.com", "bea@example.com"}, emails(out))
	require.EqualValues(t, 3, out["page"].(map[string]any)["total"])
	_, out = c.do("GET", members+"?page_size=2&page=2", c.token, nil)
	require.Equal(t, []string{"cy@example.com"}, emails(out))
	_, out = c.do("GET", members+"?q=BEA", c.token, nil)
	require.Equal(t, []string{"bea@example.com"}, emails(out))
	require.EqualValues(t, 1, out["page"].(map[string]any)["total"])

	// A member reads as a row of GET /users.
	_, list := c.do("GET", "/users?q=bea", c.token, nil)
	require.Equal(t, list["data"], out["data"])
	require.Contains(t, out["data"].([]any)[0], "balance")

	_, out = c.do("GET", fmt.Sprintf("/segments/%d/members", own), c.token, nil)
	require.Equal(t, []string{"carol@example.com"}, emails(out))
	code, out = c.do("GET", "/segments/999999/members", c.token, nil)
	require.Equal(t, 404, code)
	require.Equal(t, "not_found", errCode(out))
	require.Equal(t, "用户组不存在", out["error"].(map[string]any)["message"], "the segment package's wording, localized")
}

// The segment is an administrator's label on a user: the token response a
// user gets about themselves does not carry it.
func TestOwnTokenResponseOmitsTheSegment(t *testing.T) {
	c := newConsole(t)
	code, out := c.do("POST", "/auth/login", "", map[string]any{"email": adminEmail, "password": adminPassword})
	require.Equal(t, 200, code)
	require.NotContains(t, out["data"].(map[string]any)["user"], "segment")
}
