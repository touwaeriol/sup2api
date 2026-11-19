package grpcruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

// recordingAuthz allows everything and records the keys it was asked about.
type recordingAuthz struct{ asked []string }

func (a *recordingAuthz) Can(_ context.Context, _ int64, perm string) (bool, error) {
	a.asked = append(a.asked, perm)
	return true, nil
}
func (*recordingAuthz) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{}, nil
}
func (*recordingAuthz) CanGrant(context.Context, int64, []string) error { return nil }

// hostEnv is a host server of plugin "volc" (which declares the local
// permission "rules:read") over the given options and grants.
func hostEnv(t *testing.T, o Options, grants registry.Grants) *hostServer {
	t.Helper()
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, registrytest.Manifest("volc", "1.0.0"), []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	o.Node, o.DataDir = staticNode{"n", "b"}, t.TempDir()
	o.Concurrency = Concurrency{Hot: 4, Console: 4, Background: 4}
	rt := &Runtime{o: o, log: slog.Default()}
	return &hostServer{i: newInstance(rt, pkg, "", "", &settings{configJSON: "{}", grants: grants})}
}

// AuthzCheck answers for the plugin's own keys without a grant; a core key or
// another plugin's key needs "authz.read" and never reaches the authorizer
// without it.
func TestHostAuthzCheckOwnKeysOnly(t *testing.T) {
	granted := registry.Grants{"authz.read": json.RawMessage(`{}`)}
	cases := []struct {
		name, perm string
		grants     registry.Grants
		code       codes.Code
		asked      string // the key the authorizer sees; "" when refused
	}{
		{name: "own local key", perm: "rules:read", code: codes.OK, asked: "plugin.volc:rules:read"},
		{name: "own qualified key", perm: "plugin.volc:rules:read", code: codes.OK, asked: "plugin.volc:rules:read"},
		{name: "own qualified undeclared key", perm: "plugin.volc:other:do", code: codes.OK, asked: "plugin.volc:other:do"},
		{name: "core key without grant", perm: "user:read", code: codes.PermissionDenied},
		{name: "core key with grant", perm: "user:read", grants: granted, code: codes.OK, asked: "user:read"},
		{name: "own scoped local key", perm: "rules:read:flow/3", code: codes.OK, asked: "plugin.volc:rules:read:flow/3"},
		{name: "own local key at own scope", perm: "rules:read:own", code: codes.OK, asked: "plugin.volc:rules:read:own"},
		{name: "undeclared local key is a core key", perm: "rules:manage", code: codes.PermissionDenied},
		{name: "undeclared scoped local key is a core key", perm: "rules:manage:own", code: codes.PermissionDenied},
		{name: "scoped core key without grant", perm: "account:read:own", code: codes.PermissionDenied},
		{name: "other plugin key without grant", perm: "plugin.other:rules:read", code: codes.PermissionDenied},
		{name: "other plugin key with grant", perm: "plugin.other:rules:read", grants: granted, code: codes.OK, asked: "plugin.other:rules:read"},
		{name: "prefix of own module is another plugin", perm: "plugin.volcano:rules:read", code: codes.PermissionDenied},
		{name: "own module without local part", perm: "plugin.volc:", code: codes.PermissionDenied},
		{name: "own module without local part, granted, is asked as given", perm: "plugin.volc:", grants: granted, code: codes.OK, asked: "plugin.volc:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authz := &recordingAuthz{}
			h := hostEnv(t, Options{Authorizer: authz}, tc.grants)
			out, err := h.AuthzCheck(t.Context(), &pluginv1.AuthzCheckRequest{UserId: 7, Permission: tc.perm})
			if status.Code(err) != tc.code {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.code)
			}
			if tc.code == codes.OK && !out.GetAllowed() {
				t.Fatal("allowed = false")
			}
			var asked string
			if len(authz.asked) > 0 {
				asked = authz.asked[0]
			}
			if asked != tc.asked || len(authz.asked) > 1 {
				t.Fatalf("authorizer asked %v, want %q", authz.asked, tc.asked)
			}
		})
	}
}

func TestHostAuthzCheckArguments(t *testing.T) {
	h := hostEnv(t, Options{Authorizer: &recordingAuthz{}}, nil)
	for _, in := range []*pluginv1.AuthzCheckRequest{{Permission: "rules:read"}, {UserId: 7}} {
		if _, err := h.AuthzCheck(t.Context(), in); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%v: %v", in, err)
		}
	}
	if _, err := hostEnv(t, Options{}, nil).AuthzCheck(t.Context(), &pluginv1.AuthzCheckRequest{UserId: 7, Permission: "rules:read"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no authorizer: %v", err)
	}
}

// scopedAccounts adds core.AccountAccess to the account fake and records what
// it was asked.
type scopedAccounts struct {
	*fakeAccounts
	all     bool
	allowed []int64
	err     error
	asked   []string
}

func (a *scopedAccounts) AccountsInScope(_ context.Context, uid int64, action string, ids []int64) (bool, []int64, error) {
	a.asked = append(a.asked, fmt.Sprintf("%d:%s:%v", uid, action, ids))
	return a.all, a.allowed, a.err
}

// scopeEnv is hostEnv without any grant (AccountScopeCheck needs none),
// serving a request of user 7 until the test ends.
func scopeEnv(t *testing.T, o Options) *hostServer {
	t.Helper()
	h := hostEnv(t, o, registry.Grants{})
	t.Cleanup(h.i.callers.enter(context.Background(), 7))
	return h
}

// AccountScopeCheck forwards to core.AccountAccess, the rule the core proxy
// enforces, with its errors mapped like every core error.
func TestHostAccountScopeCheckForwards(t *testing.T) {
	in := &pluginv1.AccountScopeCheckRequest{UserId: 7, Action: "update", AccountIds: []int64{3, 1}}

	acc := &scopedAccounts{fakeAccounts: &fakeAccounts{}, allowed: []int64{3}}
	out, err := scopeEnv(t, Options{Accounts: acc}).AccountScopeCheck(t.Context(), in)
	if err != nil || out.GetAll() || !slices.Equal(out.GetAllowedIds(), []int64{3}) {
		t.Fatalf("own scope: %v %v", out, err)
	}
	if !slices.Equal(acc.asked, []string{"7:update:[3 1]"}) {
		t.Fatalf("asked %v", acc.asked)
	}

	acc = &scopedAccounts{fakeAccounts: &fakeAccounts{}, all: true}
	if out, err := scopeEnv(t, Options{Accounts: acc}).AccountScopeCheck(t.Context(), &pluginv1.AccountScopeCheckRequest{UserId: 7, Action: "read"}); err != nil || !out.GetAll() || len(out.GetAllowedIds()) != 0 {
		t.Fatalf("all scope without ids: %v %v", out, err)
	}

	for name, tc := range map[string]struct {
		err  error
		code codes.Code
	}{
		"neither account key": {core.AccountsDenied("update"), codes.PermissionDenied},
		"unknown action":      {core.InvalidFields(core.FieldError{Field: "action", Code: "invalid"}), codes.InvalidArgument},
		"database failure":    {errors.New("pool closed"), codes.Internal},
	} {
		acc := &scopedAccounts{fakeAccounts: &fakeAccounts{}, err: tc.err}
		if _, err := scopeEnv(t, Options{Accounts: acc}).AccountScopeCheck(t.Context(), in); status.Code(err) != tc.code {
			t.Fatalf("%s: %v, want %v", name, err, tc.code)
		}
	}
}

// A user_id that is not the caller of a request the plugin is serving is
// refused and does not reach the account module; no grant is needed.
func TestHostAccountScopeCheckNeedsCaller(t *testing.T) {
	acc := &scopedAccounts{fakeAccounts: &fakeAccounts{}, all: true}
	h := scopeEnv(t, Options{Accounts: acc})
	if _, err := h.AccountScopeCheck(t.Context(), &pluginv1.AccountScopeCheckRequest{UserId: 8, Action: "read", AccountIds: []int64{1}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a user the plugin is not serving: %v", err)
	}
	if len(acc.asked) != 0 {
		t.Fatalf("a refused call reached the account module: %v", acc.asked)
	}

	// The caller counts for as long as one of their requests is in flight.
	second := h.i.callers.enter(context.Background(), 8)
	leave := h.i.callers.enter(context.Background(), 8)
	leave()
	if _, err := h.AccountScopeCheck(t.Context(), &pluginv1.AccountScopeCheckRequest{UserId: 8, Action: "read"}); err != nil {
		t.Fatalf("one of two requests still in flight: %v", err)
	}
	second()
	if _, err := h.AccountScopeCheck(t.Context(), &pluginv1.AccountScopeCheckRequest{UserId: 8, Action: "read"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("after the request ended: %v", err)
	}
	// A request without a user (webhook, public route) admits nobody.
	h.i.callers.enter(context.Background(), 0)
	if h.i.callers.has(0) {
		t.Fatal("user 0 counted as a caller")
	}
}

func TestHostAccountScopeCheckArguments(t *testing.T) {
	acc := &scopedAccounts{fakeAccounts: &fakeAccounts{}, all: true}
	h := scopeEnv(t, Options{Accounts: acc})
	tooMany := make([]int64, manifest.MaxAccountBatch+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, in := range map[string]*pluginv1.AccountScopeCheckRequest{
		"no user":       {Action: "read", AccountIds: []int64{1}},
		"negative user": {UserId: -1, Action: "read"},
		"no action":     {UserId: 7, AccountIds: []int64{1}},
		"zero id":       {UserId: 7, Action: "read", AccountIds: []int64{1, 0}},
		"negative id":   {UserId: 7, Action: "read", AccountIds: []int64{-2}},
		"too many ids":  {UserId: 7, Action: "read", AccountIds: tooMany},
	} {
		if _, err := h.AccountScopeCheck(t.Context(), in); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(acc.asked) != 0 {
		t.Fatalf("invalid arguments reached the account module: %v", acc.asked)
	}
	if _, err := h.AccountScopeCheck(t.Context(), &pluginv1.AccountScopeCheckRequest{UserId: 7, Action: "read", AccountIds: tooMany[1:]}); err != nil {
		t.Fatalf("a full batch: %v", err)
	}

	// An account module that cannot answer scope questions, or none at all.
	ok := &pluginv1.AccountScopeCheckRequest{UserId: 7, Action: "read"}
	for name, o := range map[string]Options{"reader only": {Accounts: &fakeAccounts{}}, "none": {}} {
		if _, err := scopeEnv(t, o).AccountScopeCheck(t.Context(), ok); status.Code(err) != codes.Unavailable {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
