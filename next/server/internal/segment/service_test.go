package segment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "u")
}

// fakeAuthz holds, per user, which permissions they have; a user without an
// entry has none.
type fakeAuthz map[int64]func(permission string) bool

func (a fakeAuthz) Can(_ context.Context, uid int64, permission string) (bool, error) {
	return a[uid] != nil && a[uid](permission), nil
}
func (fakeAuthz) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{}, nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }
func (fakeAuthz) CanActOnUsers(context.Context, int64, core.UserAction, ...int64) error {
	return nil
}

// env is the segment service behind the real router. admin has every
// permission, masked every one but user:read, plain none. All three are
// members of the default segment. The models of testPricedModels have a global
// price, as a segment's model rate needs.
type env struct {
	t                    *testing.T
	db                   *store.DB
	svc                  *Service
	h                    http.Handler
	admin, masked, plain int64
	// ratesChanged counts the calls of Deps.RatesChanged.
	ratesChanged atomic.Int32
}

// testPricedModels are the models every env has a global price of.
var testPricedModels = []string{"m", "gpt-4o", "claude"}

func setup(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := &env{t: t, db: testutil.DB(t)}
	e.admin, e.masked, e.plain = e.user("admin@x.com"), e.user("masked@x.com"), e.user("plain@x.com")
	for _, m := range testPricedModels {
		e.exec(`INSERT INTO model_prices (model, mode, expression, expr_hash, source) VALUES ($1, 'expression', '0', 'h', 'manual')`, m)
	}
	authz := fakeAuthz{
		e.admin:  func(string) bool { return true },
		e.masked: func(p string) bool { return p != "user:read" },
	}
	engine := gin.New()
	e.svc = New(Deps{
		DB: e.db, Authz: authz, Users: authz,
		Platforms: func(_ context.Context, ids []int64) (map[int64][]string, error) {
			out := map[int64][]string{}
			for _, id := range ids {
				out[id] = []string{"anthropic"}
			}
			return out, nil
		},
		RatesChanged: func(context.Context) { e.ratesChanged.Add(1) },
	})
	e.svc.RegisterRoutes(httpapi.NewRouter(engine, fakeTokens{}, authz))
	e.h = engine
	return e
}

// What another writer did to a transaction is a conflict to try again, not a
// failure; any other error stays what it is.
func TestTxConflicts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	failure := errors.New("failure")
	for _, tc := range []struct {
		name string
		fn   func(tx *gorm.DB) error
		want error
	}{
		{"a row referred to is gone", func(tx *gorm.DB) error {
			_, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO segment_groups (segment_id, group_id, granted) VALUES (default_segment_id(), 999999, true)`)
			return err
		}, core.ErrConflict},
		{"a deadlock", func(tx *gorm.DB) error {
			// The error the server sends for one (SQLSTATE 40P01).
			_, err := store.SQLTx(tx).ExecContext(ctx, `DO $$ BEGIN RAISE EXCEPTION 'deadlock detected' USING ERRCODE = '40P01'; END $$`)
			return err
		}, core.ErrConflict},
		{"another error", func(*gorm.DB) error { return failure }, failure},
		{"none", func(*gorm.DB) error { return nil }, nil},
	} {
		err := e.svc.tx(ctx, tc.fn)
		if tc.want == core.ErrConflict {
			require.Equal(t, core.ErrConflict.Code, core.AsError(err).Code, tc.name)
			continue
		}
		require.Equal(t, tc.want, err, tc.name)
	}
}

// id runs an INSERT ... RETURNING id (or any query of one integer).
func (e *env) id(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id))
	return id
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.db.SQL.ExecContext(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

func (e *env) user(email string) int64 {
	return e.id(`INSERT INTO users (email, password_hash, display_name) VALUES ($1, 'x', $2) RETURNING id`, email, "name of "+email)
}

func (e *env) defaultSegment() int64 { return e.id(`SELECT default_segment_id()`) }

func (e *env) segment(name string) int64 {
	return e.id(`INSERT INTO segments (name) VALUES ($1) RETURNING id`, name)
}

// personal makes the user personal, coming from the segment they are in.
func (e *env) personal(user int64) int64 {
	id := e.id(`INSERT INTO segments (kind, user_id, origin_id) SELECT 'personal', id, segment_id FROM users WHERE id = $1 RETURNING id`, user)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, user, id)
	return id
}

func (e *env) group(name, visibility, rate string) int64 {
	return e.id(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ($1, $2, $3::numeric) RETURNING id`, name, visibility, rate)
}

// pair sets what a segment has in a group; override "" = none.
func (e *env) pair(segment, group int64, granted bool, override string) {
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, $3, NULLIF($4, '')::numeric)`,
		segment, group, granted, override)
}

func (e *env) segmentOf(user int64) int64 {
	return e.id(`SELECT segment_id FROM users WHERE id = $1`, user)
}

// response is a decoded API answer.
type response struct {
	t    *testing.T
	code int
	body map[string]any
}

func (e *env) do(uid int64, method, path string, body any) response {
	e.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", fmt.Sprintf("Bearer u%d", uid))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	r := response{t: e.t, code: w.Code}
	_ = json.Unmarshal(w.Body.Bytes(), &r.body)
	return r
}

// ok requires the status and returns the response.
func (r response) ok(code int) response {
	r.t.Helper()
	require.Equal(r.t, code, r.code, "%v", r.body)
	return r
}

func (r response) data() map[string]any { return r.body["data"].(map[string]any) }

func (r response) rows() []map[string]any {
	items := r.body["data"].([]any)
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = it.(map[string]any)
	}
	return out
}

func (r response) total() float64 { return r.body["page"].(map[string]any)["total"].(float64) }

// failed requires an error of the status and the code.
func (r response) failed(status int, code string) map[string]any {
	r.t.Helper()
	require.Equal(r.t, status, r.code, "%v", r.body)
	e := r.body["error"].(map[string]any)
	require.Equal(r.t, code, e["code"])
	return e
}

// invalid requires a 400 whose first field error is about the field, and
// returns that field error.
func (r response) invalid(field string) map[string]any {
	r.t.Helper()
	fe := r.failed(400, "invalid_argument")["details"].(map[string]any)["fields"].([]any)[0].(map[string]any)
	require.Equal(r.t, field, fe["field"])
	return fe
}

func idOf(row map[string]any) int64 { return int64(row["id"].(float64)) }

// names lists what pick gives of each row.
func names(rows []map[string]any, pick func(map[string]any) any) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = pick(r)
	}
	return out
}

func path(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// concurrently runs fn(0..n-1) at once and returns the status codes.
func concurrently(n int, fn func(i int) int) []int {
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = fn(i)
		}()
	}
	wg.Wait()
	return codes
}

func count(codes []int, code int) int {
	n := 0
	for _, c := range codes {
		if c == code {
			n++
		}
	}
	return n
}
