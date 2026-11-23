package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/group"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// fakeTokens reads the bearer token as the user id, and "sak_<user id>:<a>,<b>"
// as an access key of that user limited to permissions a and b.
type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "")
}

func (fakeTokens) VerifyAccessKey(_ context.Context, raw, _ string) (core.AccessKeyIdentity, error) {
	user, perms, _ := strings.Cut(strings.TrimPrefix(raw, core.AccessKeyPrefix), ":")
	uid, err := strconv.ParseInt(user, 10, 64)
	return core.AccessKeyIdentity{UserID: uid, Permissions: strings.Split(perms, ",")}, err
}

// fakeAuthz holds the permissions of each user. Like authz.Service it answers
// within the limits of the request's access key.
type fakeAuthz map[int64][]string

func (a fakeAuthz) Can(ctx context.Context, uid int64, permission string) (bool, error) {
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(permission), err
}
func (a fakeAuthz) PermissionSet(ctx context.Context, uid int64) (core.PermissionSet, error) {
	return core.RestrictToAccessKey(ctx, uid, core.PermissionSetOf(a[uid]...)), nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

// fixture is one database with rows of every kind. Every name holds "lk".
//
//	admin  reads every kind in full; owns account "lk acct b" and proxy
//	       "lk proxy b"
//	alice  has the self-service and "own" permissions; owns key "lk key one",
//	       account "lk acct a" and proxy "lk proxy a"; her segment is granted
//	       group alpha
//	bob    has no permission; owns key "lk key bob"; only his display name
//	       holds "zhang" and "_"
//	carol  has account:group:bind alone; she is in alice's segment
//	dave   has scoped account grants only, naming groups alpha and hidden;
//	       bob is his owner
//	erin   has user:read:team; she owns bob, so her team is bob and dave
type fixture struct {
	t  *testing.T
	db *store.DB
	h  http.Handler

	admin, alice, bob, carol, dave, erin int64
	// zeta is public, alpha restricted (alice's segment only) and named after
	// zeta's id, hidden restricted and granted to nobody; oddGroup is disabled.
	zeta, alpha, hidden int64
	keyOne, keyBob      int64
	acctA, acctB, acctC int64 // acctC has no creator
	proxyA, proxyB      int64
}

// oddGroup holds every character an ILIKE pattern gives a meaning to.
const oddGroup = `lk 100%_off\`

func newFixture(t *testing.T) *fixture {
	gin.SetMode(gin.TestMode)
	f := &fixture{t: t, db: testutil.DB(t)}
	f.admin = f.insert(`INSERT INTO users (email, password_hash) VALUES ('lk-admin@x.test', 'x') RETURNING id`)
	f.alice = f.insert(`INSERT INTO users (email, display_name, password_hash) VALUES ('lk-alice@x.test', 'Alice', 'x') RETURNING id`)
	f.bob = f.insert(`INSERT INTO users (email, display_name, password_hash) VALUES ('lk-bob@x.test', 'Bob_Zhang', 'x') RETURNING id`)
	f.erin = f.insert(`INSERT INTO users (email, password_hash) VALUES ('lk-erin@x.test', 'x') RETURNING id`)
	testutil.SetOwner(t, f.db, f.bob, f.erin)
	f.dave = f.insert(`INSERT INTO users (email, password_hash) VALUES ('lk-dave@x.test', 'x') RETURNING id`)
	testutil.SetOwner(t, f.db, f.dave, f.bob)
	f.insert(`INSERT INTO users (email, password_hash, deleted_at) VALUES ('lk-gone@x.test', 'x', now()) RETURNING id`)

	// A group id no other fixture name contains, for the search by id.
	f.insert(`SELECT setval('groups_id_seq', 4710)`)
	groupRow := `INSERT INTO groups (name, visibility, status, model_filter_mode, model_allowlist) VALUES ($1, $2, $3, 'whitelist', $4::jsonb) RETURNING id`
	f.zeta = f.insert(groupRow, "lk zeta", "public", "active", `["lk-claude-*"]`)
	f.alpha = f.insert(groupRow, fmt.Sprintf("lk alpha %d", f.zeta), "restricted", "active", `["lk-gemini-*"]`)
	f.hidden = f.insert(groupRow, "lk hidden", "restricted", "active", `[]`)
	f.insert(groupRow, oddGroup, "public", "disabled", `[]`)
	// alice and carol are in the segment granted alpha. Its rate in hidden is
	// not a grant.
	members := f.insert(`INSERT INTO segments (name) VALUES ('lk alpha members') RETURNING id`)
	f.carol = f.insert(`INSERT INTO users (email, password_hash, segment_id) VALUES ('lk-carol@x.test', 'x', $1) RETURNING id`, members)
	f.insert(`UPDATE users SET segment_id = $2 WHERE id = $1 RETURNING id`, f.alice, members)
	f.insert(`INSERT INTO segment_groups (segment_id, group_id, granted) VALUES ($1, $2, true) RETURNING group_id`, members, f.alpha)
	f.insert(`INSERT INTO segment_groups (segment_id, group_id, rate_override) VALUES ($1, $2, 0.5) RETURNING group_id`, members, f.hidden)

	key := `INSERT INTO api_keys (user_id, name, key_prefix, key_hash, deleted_at) VALUES ($1, $2, $3, $2, $4::timestamptz) RETURNING id`
	f.keyOne = f.insert(key, f.alice, "lk key one", "sk-s2a-aaaa", nil)
	f.insert(key, f.alice, "lk key two", "sk-s2a-cccc", "2026-01-01T00:00:00Z")
	f.keyBob = f.insert(key, f.bob, "lk key bob", "sk-s2a-bbbb", nil)

	account := `INSERT INTO accounts (name, plugin_key, type, credentials_enc, status, created_by, owner_id, deleted_at)
		VALUES ($1, 'anthropic', 'apikey', ''::bytea, $2, $3, $3, $4::timestamptz) RETURNING id`
	f.acctA = f.insert(account, "lk acct a", "active", f.alice, nil)
	f.acctB = f.insert(account, "lk acct b", "active", f.admin, nil)
	f.acctC = f.insert(account, "lk acct c", "disabled", nil, nil)
	f.insert(account, "lk acct d", "active", f.admin, "2026-01-01T00:00:00Z")
	f.insert(`INSERT INTO account_groups (account_id, group_id) VALUES ($1, $3), ($2, $3) RETURNING group_id`, f.acctB, f.acctC, f.zeta)

	proxy := `INSERT INTO proxies (name, protocol, host, port, created_by) VALUES ($1, 'http', 'proxy.example', 3128, $2) RETURNING id`
	f.proxyA = f.insert(proxy, "lk proxy a", f.alice)
	f.proxyB = f.insert(proxy, "lk proxy b", f.admin)

	price := `INSERT INTO model_prices (model, mode, expression, expr_hash, source, enabled) VALUES ($1, 'expression', '0', 'h', 'manual', $2) RETURNING id`
	f.insert(price, "lk-claude-a", true)
	f.insert(price, "lk-claude-b", false)
	f.insert(price, "lk-gemini-1", true)
	f.insert(price, "lk-gpt-x", true)

	engine := gin.New()
	authz := fakeAuthz{
		f.admin: {"user:read", "group:read", "usage:all:read", "account:read", "account:read:own", "price:read", "proxy:read", "proxy:read:own"},
		f.alice: {"apikey:self:manage", "account:read:own", "proxy:read:own"},
		f.carol: {"account:group:bind", fmt.Sprintf("account:read:group/%d", f.zeta)},
		f.dave: {fmt.Sprintf("account:read:group/%d", f.hidden), fmt.Sprintf("account:update:group/%d/account/%d", f.alpha, f.acctB),
			"account:test:group/*", "proxy:read:own"},
		f.erin: {"user:read:team"},
	}
	r := httpapi.NewRouter(engine, fakeTokens{}, authz)
	New(f.db, authz).RegisterRoutes(r)
	// The self-service lists that mine=true is defined by.
	group.New(f.db, nil, nil, nil, nil).RegisterRoutes(r)
	billing.New(f.db, nil, nil, nil, nil).RegisterRoutes(r)
	f.h = engine
	return f
}

// insert runs a statement that returns one id.
func (f *fixture) insert(sql string, args ...any) (id int64) {
	f.t.Helper()
	require.NoError(f.t, f.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id))
	return id
}

// get calls GET /api/v1/lookup/<path> as uid.
func (f *fixture) get(uid int64, path string) *httptest.ResponseRecorder {
	return f.call(strconv.FormatInt(uid, 10), "/lookup/"+path)
}

// call calls GET /api/v1<path> with a bearer token.
func (f *fixture) call(token, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/v1"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	return w
}

// data decodes the "data" of an answer, which must be a 200.
func data[T any](t *testing.T, w *httptest.ResponseRecorder) (out T) {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &struct{ Data *T }{&out}))
	return out
}

// named is a row of any list, as far as these tests read it.
type named struct {
	ID   any
	Name string
}

func names(rows []named) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// found decodes the answer of a lookup: the names in order, and "more".
func found(t *testing.T, w *httptest.ResponseRecorder) ([]string, bool) {
	t.Helper()
	out := data[struct {
		Items []named
		More  bool
	}](t, w)
	return names(out.Items), out.More
}

func TestLookup(t *testing.T) {
	f := newFixture(t)
	zeta, alpha := strconv.FormatInt(f.zeta, 10), strconv.FormatInt(f.alpha, 10)
	alphaName := "lk alpha " + zeta

	hidden, bob := strconv.FormatInt(f.hidden, 10), strconv.FormatInt(f.bob, 10)
	keyOne, keyBob := strconv.FormatInt(f.keyOne, 10), strconv.FormatInt(f.keyBob, 10)
	acctA, acctB, proxyB := strconv.FormatInt(f.acctA, 10), strconv.FormatInt(f.acctB, 10), strconv.FormatInt(f.proxyB, 10)

	finds := []struct {
		name string
		uid  int64
		path string
		want []string // names, in order
		more bool
	}{
		{"name contains, any case, by name", f.admin, "users?q=LK-A", []string{"lk-admin@x.test", "lk-alice@x.test"}, false},
		{"no q lists by name", f.admin, "groups", []string{oddGroup, alphaName, "lk hidden", "lk zeta"}, false},
		{"digits match the id too, id first", f.admin, "groups?q=" + zeta, []string{"lk zeta", alphaName}, false},
		{"digits too long for an id are a name", f.admin, "groups?q=99999999999999999999", []string{}, false},
		{"ids, unknown left out, q and limit ignored", f.admin, "groups?ids=" + zeta + "," + alpha + ",999999&q=none&limit=1", []string{alphaName, "lk zeta"}, false},
		{"limit cuts and says so", f.admin, "groups?q=lk&limit=2", []string{oddGroup, alphaName}, true},
		{"limit not reached", f.admin, "groups?q=lk&limit=4", []string{oddGroup, alphaName, "lk hidden", "lk zeta"}, false},
		{"percent is literal", f.admin, "groups?q=%25", []string{oddGroup}, false},
		{"underscore is literal", f.admin, "groups?q=_", []string{oddGroup}, false},
		{"backslash is literal", f.admin, "groups?q=%5C", []string{oddGroup}, false},
		{"quote is text", f.admin, "groups?q='", []string{}, false},
		{"users of the caller's team", f.erin, "users?q=lk", []string{"lk-bob@x.test", "lk-dave@x.test"}, false},
		{"users by display name", f.admin, "users?q=zhang", []string{"lk-bob@x.test"}, false},
		{"underscore is literal in the display name", f.admin, "users?q=_", []string{"lk-bob@x.test"}, false},
		{"deleted user", f.admin, "users?q=lk-gone", []string{}, false},
		{"deleted key", f.admin, "api-keys?q=lk", []string{"lk key bob", "lk key one"}, false},

		{"groups mine: public and granted, active", f.alice, "groups?mine=true", []string{alphaName, "lk zeta"}, false},
		{"groups mine needs no permission", f.bob, "groups?mine=true", []string{"lk zeta"}, false},
		{"groups with account:group:bind: the caller's usable groups", f.carol, "groups", []string{alphaName, "lk zeta"}, false},
		{"api-keys of one user", f.admin, "api-keys?user_id=" + strconv.FormatInt(f.alice, 10), []string{"lk key one"}, false},
		{"api-keys by id, of one user", f.admin, "api-keys?ids=" + keyOne + "," + keyBob + "&user_id=" + bob, []string{"lk key bob"}, false},
		{"api-keys mine", f.alice, "api-keys?mine=true", []string{"lk key one"}, false},
		{"accounts", f.admin, "accounts?q=lk", []string{"lk acct a", "lk acct b", "lk acct c"}, false},
		{"accounts of the preferred group first", f.admin, "accounts?q=lk&prefer_group_id=" + zeta, []string{"lk acct b", "lk acct c", "lk acct a"}, false},
		{"accounts with the own permission", f.alice, "accounts", []string{"lk acct a"}, false},
		{"accounts with a group scope", f.carol, "accounts", []string{"lk acct b", "lk acct c"}, false},
		{"accounts mine under the full permission", f.admin, "accounts?mine=true", []string{"lk acct b"}, false},
		{"proxies", f.admin, "proxies", []string{"lk proxy a", "lk proxy b"}, false},
		{"proxies with the own permission", f.alice, "proxies", []string{"lk proxy a"}, false},
		{"models include disabled prices", f.admin, "models", []string{"lk-claude-a", "lk-claude-b", "lk-gemini-1", "lk-gpt-x"}, false},
		{"models have no numeric id", f.admin, "models?q=1", []string{"lk-gemini-1"}, false},
		{"models by name", f.admin, "models?ids=lk-gpt-x,nope", []string{"lk-gpt-x"}, false},
		{"models mine: enabled and allowed by one of the caller's groups", f.alice, "models?mine=true", []string{"lk-claude-a", "lk-gemini-1"}, false},

		// Rows outside the caller's range are not found by id either, in ids
		// or in q, whether mine or the "own" permission sets the range.
		{"groups mine: another group in ids", f.alice, "groups?mine=true&ids=" + hidden + "," + zeta, []string{"lk zeta"}, false},
		{"groups mine: another group's id in q", f.alice, "groups?mine=true&q=" + hidden, []string{}, false},
		{"groups bind: another group in ids", f.carol, "groups?ids=" + hidden + "," + zeta, []string{"lk zeta"}, false},
		{"groups bind: a disabled group by name", f.carol, "groups?q=off", []string{}, false},
		{"groups named by the caller's scopes", f.dave, "groups", []string{alphaName, "lk hidden"}, false},
		{"groups named: another group in ids", f.dave, "groups?ids=" + zeta + "," + hidden, []string{"lk hidden"}, false},
		{"groups named: another group by name", f.dave, "groups?q=zeta", []string{}, false},
		{"api-keys mine: another user's key in ids", f.alice, "api-keys?mine=true&ids=" + keyBob + "," + keyOne, []string{"lk key one"}, false},
		{"api-keys mine: another user's key id in q", f.alice, "api-keys?mine=true&q=" + keyBob, []string{}, false},
		{"api-keys mine: another user's name in q", f.alice, "api-keys?mine=true&q=bob", []string{}, false},
		{"api-keys mine: user_id of another user", f.alice, "api-keys?mine=true&user_id=" + bob, []string{}, false},
		{"accounts own: another's account in ids", f.alice, "accounts?ids=" + acctB + "," + acctA, []string{"lk acct a"}, false},
		{"accounts own: another's account id in q", f.alice, "accounts?q=" + acctB, []string{}, false},
		{"accounts own: preferring a group lists no more", f.alice, "accounts?prefer_group_id=" + zeta, []string{"lk acct a"}, false},
		{"accounts mine: another's account in ids", f.admin, "accounts?mine=true&ids=" + acctA + "," + acctB, []string{"lk acct b"}, false},
		{"users team: others and oneself in ids", f.erin, "users?ids=" + strconv.FormatInt(f.alice, 10) + "," + strconv.FormatInt(f.erin, 10) + "," + bob, []string{"lk-bob@x.test"}, false},
		{"users team: another's id in q", f.erin, "users?q=" + strconv.FormatInt(f.admin, 10), []string{}, false},
		{"users team: another's email in q", f.erin, "users?q=alice", []string{}, false},
		{"proxies own: another's proxy in ids", f.alice, "proxies?ids=" + proxyB, []string{}, false},
		{"proxies own: another's proxy id in q", f.alice, "proxies?q=" + proxyB, []string{}, false},
		{"proxies mine under the full permission", f.admin, "proxies?mine=true", []string{"lk proxy b"}, false},
		{"models mine: others in ids", f.alice, "models?mine=true&ids=lk-gpt-x,lk-claude-b,lk-claude-a", []string{"lk-claude-a"}, false},
		{"models mine: others in q", f.alice, "models?mine=true&q=gpt", []string{}, false},
	}
	for _, tc := range finds {
		t.Run(tc.name, func(t *testing.T) {
			got, more := found(t, f.get(tc.uid, tc.path))
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.more, more)
		})
	}

	// The whole answer: a numeric id with hint and status, empty fields left
	// out, the model name as the id of a model.
	shapes := []struct {
		name string
		uid  int64
		path string
		want string // the one item, "" for none
	}{
		{"user", f.admin, "users?ids=" + strconv.FormatInt(f.alice, 10),
			fmt.Sprintf(`{"id":%d,"name":"lk-alice@x.test","hint":"Alice","status":"active"}`, f.alice)},
		{"group has no hint", f.admin, "groups?ids=" + zeta, `{"id":` + zeta + `,"name":"lk zeta","status":"active"}`},
		{"group with account:group:bind", f.carol, "groups?ids=" + zeta, `{"id":` + zeta + `,"name":"lk zeta","status":"active"}`},
		{"group named by a scope", f.dave, "groups?ids=" + hidden, `{"id":` + hidden + `,"name":"lk hidden","status":"active"}`},
		{"key: owner", f.admin, "api-keys?q=one",
			fmt.Sprintf(`{"id":%d,"name":"lk key one","hint":"lk-alice@x.test","status":"active"}`, f.keyOne)},
		{"key mine: prefix", f.alice, "api-keys?mine=true",
			fmt.Sprintf(`{"id":%d,"name":"lk key one","hint":"sk-s2a-aaaa","status":"active"}`, f.keyOne)},
		{"account: plugin, status as stored", f.admin, "accounts?q=acct%20c",
			fmt.Sprintf(`{"id":%d,"name":"lk acct c","hint":"anthropic","status":"disabled"}`, f.acctC)},
		{"proxy: address", f.alice, "proxies",
			fmt.Sprintf(`{"id":%d,"name":"lk proxy a","hint":"http://proxy.example:3128","status":"active"}`, f.proxyA)},
		{"model", f.admin, "models?q=gpt", `{"id":"lk-gpt-x","name":"lk-gpt-x"}`},
		{"nothing found", f.admin, "models?q=none", ""},
	}
	for _, tc := range shapes {
		t.Run("shape/"+tc.name, func(t *testing.T) {
			w := f.get(tc.uid, tc.path)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.JSONEq(t, `{"data":{"items":[`+tc.want+`],"more":false}}`, w.Body.String())
		})
	}

	refused := []struct {
		name   string
		uid    int64
		path   string
		status int
	}{
		{"users without user:read", f.alice, "users", 403},
		{"groups without group:read", f.alice, "groups", 403},
		{"groups without either permission", f.bob, "groups", 403},
		{"api-keys without usage:all:read", f.alice, "api-keys", 403},
		{"api-keys mine without apikey:self:manage", f.bob, "api-keys?mine=true", 403},
		{"accounts without either permission", f.bob, "accounts", 403},
		{"accounts mine without either permission", f.bob, "accounts?mine=true", 403},
		{"models without price:read", f.alice, "models", 403},
		{"models mine without apikey:self:manage", f.bob, "models?mine=true", 403},
		{"proxies without either permission", f.bob, "proxies", 403},
		{"proxies mine without either permission", f.bob, "proxies?mine=true", 403},
		{"users by id without user:read", f.alice, "users?ids=" + bob, 403},
		{"groups by id without group:read", f.alice, "groups?ids=" + hidden, 403},
		{"api-keys by id without usage:all:read", f.alice, "api-keys?ids=" + keyBob, 403},
		{"api-keys of oneself without usage:all:read", f.alice, "api-keys?user_id=" + strconv.FormatInt(f.alice, 10), 403},
		{"api-keys mine under usage:all:read alone", f.admin, "api-keys?mine=true", 403},
		{"models by name without price:read", f.alice, "models?ids=lk-gpt-x", 403},
		{"users have no mine", f.admin, "users?mine=true", 400},
		{"limit 0", f.admin, "groups?limit=0", 400},
		{"limit over the maximum", f.admin, "groups?limit=51", 400},
		{"limit not a number", f.admin, "groups?limit=x", 400},
		{"ids not numbers", f.admin, "groups?ids=1,x", 400},
		{"ids not positive", f.admin, "groups?ids=0", 400},
		{"ids too long for an id", f.admin, "groups?ids=99999999999999999999", 400},
		{"over 100 ids", f.admin, "groups?ids=" + strings.Repeat("1,", 100) + "1", 400},
		{"empty model name in ids", f.admin, "models?ids=a,,b", 400},
		{"mine not a boolean", f.admin, "groups?mine=maybe", 400},
		{"q too long", f.admin, "groups?q=" + strings.Repeat("a", 257), 400},
		{"q not text", f.admin, "groups?q=%00", 400},
		{"user_id not positive", f.admin, "api-keys?user_id=-1", 400},
		{"prefer_group_id not a number", f.admin, "accounts?prefer_group_id=abc", 400},
		{"unknown kind", f.admin, "nothing", 404},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			w := f.get(tc.uid, tc.path)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}

// An access key may call the lookup (CONTRACTS §96.4), within what the key
// itself was given.
func TestLookupAccessKey(t *testing.T) {
	f := newFixture(t)
	key := func(uid int64, permissions string) string {
		return core.AccessKeyPrefix + strconv.FormatInt(uid, 10) + ":" + permissions
	}
	cases := []struct {
		name  string
		token string
		path  string
		want  []string // names; nil when the lookup is refused (403)
	}{
		{"a kind the key was given", key(f.admin, "group:read"), "groups?q=zeta", []string{"lk zeta"}},
		{"a kind the key was not given", key(f.admin, "group:read"), "users", nil},
		{"a permission the user does not hold", key(f.alice, "user:read"), "users", nil},
		{"user:read on a key of a team holder reaches the team", key(f.erin, "user:read"), "users", []string{"lk-bob@x.test", "lk-dave@x.test"}},
		{"the own permission of a user who holds the full one", key(f.admin, "account:read:own"), "accounts", []string{"lk acct b"}},
		{"mine, given its permission", key(f.alice, "apikey:self:manage"), "api-keys?mine=true", []string{"lk key one"}},
		{"mine, not given its permission", key(f.alice, "account:read:own"), "api-keys?mine=true", nil},
		{"groups mine needs no permission", key(f.alice, "account:read:own"), "groups?mine=true&q=zeta", []string{"lk zeta"}},
		{"groups named by the key's scopes", key(f.dave, fmt.Sprintf("account:read:group/%d", f.hidden)), "groups", []string{"lk hidden"}},
		{"groups named by the user but not the key", key(f.dave, "proxy:read:own"), "groups", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := f.call(tc.token, "/lookup/"+tc.path)
			if tc.want == nil {
				require.Equal(t, 403, w.Code, w.Body.String())
				return
			}
			got, _ := found(t, w)
			require.Equal(t, tc.want, got)
		})
	}
}

// mine=true is defined by the self-service lists (CONTRACTS §96.3), whose
// conditions entities.go repeats: the groups are those of GET /me/groups. The
// models are a superset of what GET /me/prices lists for these groups: a
// group may allow a model that none of its accounts provides.
func TestLookupMineIsTheSelfServiceRange(t *testing.T) {
	f := newFixture(t)
	alice := strconv.FormatInt(f.alice, 10)
	groups := data[[]named](t, f.call(alice, "/me/groups"))
	var models []string
	for _, g := range groups {
		prices := data[struct{ Prices []struct{ Model string } }](t, f.call(alice, fmt.Sprintf("/me/prices?group_id=%v", g.ID)))
		for _, p := range prices.Prices {
			models = append(models, p.Model)
		}
	}
	require.Len(t, groups, 2)
	require.Equal(t, []string{"lk-claude-a"}, models)

	got, _ := found(t, f.get(f.alice, "groups?mine=true"))
	require.ElementsMatch(t, names(groups), got)
	got, _ = found(t, f.get(f.alice, "models?mine=true"))
	require.Subset(t, got, models)
	// alpha allows lk-gemini-1 but has no account: its prices do not list it.
	require.Contains(t, got, "lk-gemini-1")

	// account:group:bind alone finds the caller's GET /me/groups too: the
	// groups an "own" account holder may bind.
	carol := data[[]named](t, f.call(strconv.FormatInt(f.carol, 10), "/me/groups"))
	got, _ = found(t, f.get(f.carol, "groups"))
	require.ElementsMatch(t, names(carol), got)
}

// A model an account group prices on its own, without a global price, is a
// model of the price table, and in the caller's range for that group alone,
// as its model policy allows: GET /me/prices?group_id= lists it there.
func TestLookupGroupOnlyModels(t *testing.T) {
	f := newFixture(t)
	rule := `INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		VALUES ($1, $2, 'expression', '{}', 'flat(1)', 'h') RETURNING id`
	f.insert(rule, f.zeta, "lk-claude-own")
	f.insert(rule, f.zeta, "lk-gemini-own") // zeta allows lk-claude-* only
	f.insert(rule, f.hidden, "lk-claude-hidden")
	got, _ := found(t, f.get(f.admin, "models?q=own"))
	require.Equal(t, []string{"lk-claude-own", "lk-gemini-own"}, got)

	prices := data[struct{ Prices []struct{ Model string } }](t, f.call(strconv.FormatInt(f.alice, 10), fmt.Sprintf("/me/prices?group_id=%d", f.zeta)))
	require.Contains(t, prices.Prices, struct{ Model string }{"lk-claude-own"})
	got, _ = found(t, f.get(f.alice, "models?mine=true"))
	require.Equal(t, []string{"lk-claude-a", "lk-claude-own", "lk-gemini-1"}, got)
}
