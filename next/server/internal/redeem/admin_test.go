package redeem

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestRedeemLocksAfterTenFailures(t *testing.T) {
	f := newFixture(t, nil)
	f.svc.windows = nil // the failures come faster than 5 a minute here
	user := testutil.InsertUser(t, f.db, "rd-lock@x.test")
	good := f.seed(10, nil)
	for range maxFailures {
		bad, err := newCode()
		require.NoError(t, err)
		_, err = f.svc.Redeem(t.Context(), user, bad)
		requireInvalid(t, err)
	}
	// Locked: even a good code is refused, and stays unused.
	_, err := f.svc.Redeem(t.Context(), user, good)
	e := core.AsError(err)
	require.Equal(t, "rate_limited", e.Code)
	require.InDelta(t, lockFor.Seconds(), e.Details["retry_after_seconds"], 1)
	require.Equal(t, statusUnused, f.status(good))

	// Another user is not locked; the lock ends after an hour.
	other := testutil.InsertUser(t, f.db, "rd-lock-other@x.test")
	_, err = f.svc.Redeem(t.Context(), other, good)
	require.NoError(t, err)
	f.mr.FastForward(lockFor + time.Second)
	require.Zero(t, f.svc.fails.lockedFor(t.Context(), user))
}

func TestRedeemFiveAMinute(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-rate@x.test")
	for range perMinute {
		_, err := f.svc.Redeem(t.Context(), user, "nope")
		requireInvalid(t, err)
	}
	_, err := f.svc.Redeem(t.Context(), user, f.seed(10, nil))
	require.Equal(t, "rate_limited", core.AsError(err).Code)
}

func TestGenerateStoresNoPlainCode(t *testing.T) {
	f := newFixture(t, nil)
	admin := testutil.InsertUser(t, f.db, "rd-admin@x.test")
	user := testutil.InsertUser(t, f.db, "rd-gen-user@x.test")
	exp := time.Now().Add(time.Hour)
	out, err := f.svc.Generate(t.Context(), admin, GenerateRequest{Count: 3, Value: decimal.RequireFromString("12.5"), ExpiresAt: &exp, Notes: "batch"})
	require.NoError(t, err)
	require.Len(t, out.Codes, 3)
	for _, c := range out.Codes {
		require.Regexp(t, `^[0-9A-F]{4}(-[0-9A-F]{4}){7}$`, c)
		for _, needle := range []string{c, normalize(c), strings.ToLower(normalize(c))} {
			require.Zero(t, f.rowsHolding(needle), "plain code stored: %s", needle)
		}
	}
	var actor int64
	require.NoError(t, f.db.Gorm.Table("audit_logs").Select("user_id").Where("action = ?", "redeem_code.generate").Scan(&actor).Error)
	require.Equal(t, admin, actor)

	got, err := f.svc.Redeem(t.Context(), user, out.Codes[1])
	require.NoError(t, err)
	require.True(t, got.Amount.Equal(decimal.RequireFromString("12.5")))
}

// rowsHolding counts the redeem_codes and audit_logs rows whose text holds s.
func (f *fixture) rowsHolding(s string) int64 {
	f.t.Helper()
	var n int64
	for _, table := range []string{"redeem_codes t", "audit_logs t"} {
		var c int64
		require.NoError(f.t, f.db.Gorm.Table(table).Where("strpos(row_to_json(t)::text, ?) > 0", s).Count(&c).Error)
		n += c
	}
	return n
}

func TestGenerateValidates(t *testing.T) {
	f := newFixture(t, nil)
	past := time.Now().Add(-time.Minute)
	for _, in := range []GenerateRequest{
		{Count: 0, Value: decimal.NewFromInt(1)},
		{Count: 1001, Value: decimal.NewFromInt(1)},
		{Count: 1, Value: decimal.Zero},
		{Count: 1, Value: decimal.NewFromInt(100001)},
		{Count: 1, Value: decimal.RequireFromString("0.000000001")},
		{Count: 1, Value: decimal.NewFromInt(1), ExpiresAt: &past},
		{Count: 1, Value: decimal.NewFromInt(1), Notes: strings.Repeat("字", 201)},
	} {
		_, err := f.svc.Generate(t.Context(), 0, in)
		require.Equal(t, "invalid_argument", core.AsError(err).Code, "%+v", in)
	}
}

func TestListByStatus(t *testing.T) {
	f := newFixture(t, nil)
	admin := testutil.InsertUser(t, f.db, "rd-list-admin@x.test")
	user := testutil.InsertUser(t, f.db, "rd-list-user@x.test")
	past := time.Now().Add(-time.Minute)
	used, expired, voided, unused := f.seed(1, nil), f.seed(2, &past), f.seed(3, nil), f.seed(4, nil)
	_, err := f.svc.Redeem(t.Context(), user, used)
	require.NoError(t, err)
	require.NoError(t, f.svc.Void(t.Context(), admin, f.codeID(voided)))

	for status, code := range map[string]string{statusUsed: used, statusExpired: expired, statusVoided: voided, statusUnused: unused} {
		items, total, err := f.svc.List(t.Context(), status, 1, 20)
		require.NoError(t, err)
		require.EqualValues(t, 1, total, status)
		require.Equal(t, status, items[0].Status)
		require.Equal(t, normalize(code)[codeLen-4:], items[0].CodeTail)
	}
	items, total, err := f.svc.List(t.Context(), statusUsed, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, user, *items[0].UsedBy)
	require.Equal(t, "rd-list-user@x.test", *items[0].UsedByEmail)
	require.WithinDuration(t, time.Now(), *items[0].UsedAt, time.Minute)
	_, _, err = f.svc.List(t.Context(), "bogus", 1, 20)
	require.Equal(t, "invalid_argument", core.AsError(err).Code)
}

func TestVoidOnlyUnused(t *testing.T) {
	f := newFixture(t, nil)
	admin := testutil.InsertUser(t, f.db, "rd-void-admin@x.test")
	used, voided := f.seed(1, nil), f.seed(2, nil)
	_, err := f.svc.Redeem(t.Context(), admin, used)
	require.NoError(t, err)
	require.NoError(t, f.svc.Void(t.Context(), admin, f.codeID(voided)))

	require.Equal(t, "conflict", core.AsError(f.svc.Void(t.Context(), admin, f.codeID(used))).Code)
	require.Equal(t, "conflict", core.AsError(f.svc.Void(t.Context(), admin, f.codeID(voided))).Code)
	require.Equal(t, "not_found", core.AsError(f.svc.Void(t.Context(), admin, 1<<40)).Code)
	var voidedBy int64
	require.NoError(t, f.db.Gorm.Table("redeem_codes").Select("voided_by").Where("id = ?", f.codeID(voided)).Scan(&voidedBy).Error)
	require.Equal(t, admin, voidedBy)
	var audited int64
	require.NoError(t, f.db.Gorm.Table("audit_logs").Where("action = ? AND user_id = ?", "redeem_code.void", admin).Count(&audited).Error)
	require.EqualValues(t, 1, audited)
}

// fakeTokens reads the bearer token as the user id.
type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "")
}
func (fakeTokens) VerifyAccessKey(context.Context, string, string) (core.AccessKeyIdentity, error) {
	return core.AccessKeyIdentity{}, core.ErrUnauthenticated
}

// fakeAuthz holds each user's permissions.
type fakeAuthz map[int64][]string

func (a fakeAuthz) Can(ctx context.Context, uid int64, p string) (bool, error) {
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(p), err
}
func (a fakeAuthz) PermissionSet(_ context.Context, uid int64) (core.PermissionSet, error) {
	return core.PermissionSetOf(a[uid]...), nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

type caller func(uid int64, method, path, body string) *httptest.ResponseRecorder

// routes serves the service's routes with az's permissions.
func routes(f *fixture, az fakeAuthz) caller {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	f.svc.RegisterRoutes(httpapi.NewRouter(engine, fakeTokens{}, az))
	return func(uid int64, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(uid, 10))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
}

func TestRoutesNeedPermissions(t *testing.T) {
	f := newFixture(t, nil)
	admin := testutil.InsertUser(t, f.db, "rd-http-admin@x.test")
	user := testutil.InsertUser(t, f.db, "rd-http-user@x.test")
	call := routes(f, fakeAuthz{admin: {"redeem:manage"}, user: {"redeem:use"}})
	id := strconv.FormatInt(f.codeID(f.seed(5, nil)), 10)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/redeem-codes", ""},
		{http.MethodPost, "/redeem-codes", `{"count":1,"value":1}`},
		{http.MethodDelete, "/redeem-codes/" + id, ""},
	} {
		require.Equal(t, http.StatusForbidden, call(user, c.method, c.path, c.body).Code, c.path)
	}
	require.Equal(t, http.StatusForbidden, call(admin, http.MethodPost, "/me/redeem", `{"code":"x"}`).Code)

	w := call(admin, http.MethodPost, "/redeem-codes", `{"count":1,"value":7}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var gen struct{ Data Generated }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &gen))

	w = call(user, http.MethodPost, "/me/redeem", `{"code":"`+gen.Data.Codes[0]+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"amount":"7","balance":"7"}}`, w.Body.String())
	require.Equal(t, http.StatusNoContent, call(admin, http.MethodDelete, "/redeem-codes/"+id, "").Code)
}
