package grpcruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

// fakeAccounts is a core.PluginAccountReader that records the plugin key it
// was called with: the whole point of the two RPCs is that the host, not the
// plugin, decides whose accounts are read.
type fakeAccounts struct {
	// rows per plugin key.
	rows  map[string][]core.PluginAccountSummary
	creds map[int64]string
	// calls records "<method>:<pluginKey>" in order.
	calls []string
	// manifest is the manifest the last SendPluginAccountRequest was given.
	manifest *manifest.Manifest
	// lastQuery is the query of the last ListPluginAccounts call.
	lastQuery core.PluginAccountQuery
	// audits counts ReadPluginAccount calls that returned an account (the
	// real implementation writes one audit row per such call).
	audits int
}

func (f *fakeAccounts) ListPluginAccounts(_ context.Context, key string, q core.PluginAccountQuery) ([]core.PluginAccountSummary, error) {
	f.calls = append(f.calls, "list:"+key)
	f.lastQuery = q
	rows := f.rows[key]
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func (f *fakeAccounts) ReadPluginAccount(_ context.Context, key string, id int64) (*pluginv1.Account, error) {
	f.calls = append(f.calls, "read:"+key)
	for _, a := range f.rows[key] {
		if a.ID == id {
			f.audits++
			return &pluginv1.Account{Id: a.ID, Name: a.Name, Type: a.Type, CredentialsJson: f.creds[id], SettingsJson: string(a.Settings),
				Proxy: &pluginv1.AccountProxy{Id: 3, Protocol: "socks5", Host: "proxy.test", Port: 1080, Username: "u", Password: "pw"}}, nil
		}
	}
	return nil, core.ErrNotFound.WithMessage("account not found")
}

func (f *fakeAccounts) ApplyAccountAutomaticState(context.Context, int64, *pluginv1.AccountAutomaticState) (*pluginv1.SetAccountAutomaticStateResponse, error) {
	return nil, core.ErrPermissionDenied
}

func (f *fakeAccounts) ApplyPluginAccountAutomaticState(_ context.Context, key string, id int64, state *pluginv1.AccountAutomaticState) (*pluginv1.SetAccountAutomaticStateResponse, error) {
	f.calls = append(f.calls, "state:"+key)
	for _, a := range f.rows[key] {
		if a.ID == id {
			return &pluginv1.SetAccountAutomaticStateResponse{Changed: true, Status: "auto_disabled", Reason: state.GetReason()}, nil
		}
	}
	return nil, core.ErrNotFound
}

func TestHostAutomaticStatePermissionAndScope(t *testing.T) {
	in := &pluginv1.SetAccountAutomaticStateRequest{AccountId: 1, State: &pluginv1.AccountAutomaticState{Action: pluginv1.AccountAutomaticState_ACTION_DISABLE}}
	for _, grants := range []registry.Grants{
		{}, {"accounts.auto_state": json.RawMessage(`{}`)},
		{"accounts.auto_state": json.RawMessage(`{"types":"all"}`)},
		{"accounts.auto_state": json.RawMessage(`{"types":"own","extra":true}`)},
	} {
		h, accounts := accountsEnv(t, grants)
		if _, err := h.SetAccountAutomaticState(t.Context(), in); status.Code(err) != codes.PermissionDenied || len(accounts.calls) != 0 {
			t.Fatalf("invalid grant reached accounts: %v %v", err, accounts.calls)
		}
	}
	h, accounts := accountsEnv(t, registry.Grants{"accounts.auto_state": json.RawMessage(`{"types":"own"}`)})
	if out, err := h.SetAccountAutomaticState(t.Context(), in); err != nil || !out.GetChanged() || len(accounts.calls) != 1 || accounts.calls[0] != "state:volc" {
		t.Fatalf("authorized call: %v %v", out, err)
	}
	for _, id := range []int64{9, 999999} {
		in.AccountId = id
		if _, err := h.SetAccountAutomaticState(t.Context(), in); status.Code(err) != codes.NotFound {
			t.Fatalf("foreign/missing account: %v", err)
		}
	}
	if _, err := h.SetAccountAutomaticState(t.Context(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid request: %v", err)
	}
}

func accountsEnv(t *testing.T, grants registry.Grants) (*hostServer, *fakeAccounts) {
	t.Helper()
	m := registrytest.Manifest("volc", "1.0.0")
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	acc := &fakeAccounts{
		rows: map[string][]core.PluginAccountSummary{
			"volc": {
				{ID: 1, Name: "a1", Type: "apikey", Status: "active", Schedulable: true,
					Settings: json.RawMessage(`{"base_url":"https://ark"}`)},
				{ID: 2, Name: "a2", Type: "apikey", Status: "disabled", Schedulable: false},
			},
			"other": {{ID: 9, Name: "theirs", Type: "relay_key", Status: "active", Schedulable: true}},
		},
		creds: map[int64]string{1: `{"access_key":"AK","secret_key":"SK"}`, 9: `{"api_key":"theirs"}`},
	}
	rt := &Runtime{o: Options{Node: staticNode{"n", "b"}, Concurrency: Concurrency{Hot: 4, Console: 4, Background: 4}, DataDir: t.TempDir(), Accounts: acc},
		log: slog.Default()}
	i := newInstance(rt, pkg, "", "", &settings{configJSON: "{}", grants: grants})
	return &hostServer{i: i}, acc
}

// The account calls need no grant (CONTRACTS §124): the account module
// answers with the plugin's own accounts only.
func TestHostListAccounts(t *testing.T) {
	ctx := context.Background()
	h, acc := accountsEnv(t, registry.Grants{})

	out, err := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The host, not the request, decides whose accounts are read.
	if len(acc.calls) != 1 || acc.calls[0] != "list:volc" {
		t.Fatalf("calls = %v", acc.calls)
	}
	if len(out.GetAccounts()) != 2 {
		t.Fatalf("accounts = %v", out.GetAccounts())
	}
	a := out.GetAccounts()[0]
	if a.GetId() != 1 || a.GetName() != "a1" || a.GetType() != "apikey" || a.GetStatus() != "active" ||
		!a.GetEnabled() || a.GetSettingsJson() != `{"base_url":"https://ark"}` {
		t.Fatalf("summary = %v", a)
	}
	if out.GetAccounts()[1].GetEnabled() || out.GetAccounts()[1].GetSettingsJson() != "{}" {
		t.Fatalf("second summary = %v", out.GetAccounts()[1])
	}
	// No field of the response may carry a credential.
	if b, _ := json.Marshal(out.GetAccounts()); strings.Contains(strings.ToLower(string(b)), "secret") ||
		strings.Contains(string(b), "AK") {
		t.Fatalf("credential in the list response: %s", b)
	}

	// Paging: a full page yields a cursor, a short page does not.
	out, err = h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Limit: 1})
	if err != nil || out.GetNextCursor() != "1" || len(out.GetAccounts()) != 1 {
		t.Fatalf("page: %v %v", out, err)
	}
	if out, _ := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Limit: 2}); out.GetNextCursor() != "2" {
		t.Fatalf("cursor on a full page: %q", out.GetNextCursor())
	}
	if out, _ := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Limit: 50}); out.GetNextCursor() != "" {
		t.Fatalf("cursor on a short page: %q", out.GetNextCursor())
	}

	// The limit is capped: there is no way to ask for everything at once.
	if _, err := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Limit: 100000}); err != nil {
		t.Fatal(err)
	}
	if acc.lastQuery.Limit != AccountsDefaultListLimit {
		t.Fatalf("limit = %d", acc.lastQuery.Limit)
	}
	if _, err := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Cursor: "7", Type: "apikey", IncludeInactive: true}); err != nil {
		t.Fatal(err)
	}
	if acc.lastQuery.AfterID != 7 || acc.lastQuery.Type != "apikey" || !acc.lastQuery.IncludeInactive {
		t.Fatalf("query = %+v", acc.lastQuery)
	}
	for _, c := range []string{"abc", "-1", "1;drop"} {
		if _, err := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Cursor: c}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("cursor %q: %v", c, err)
		}
	}
}

func TestHostGetAccount(t *testing.T) {
	ctx := context.Background()
	h, acc := accountsEnv(t, registry.Grants{})
	out, err := h.GetAccount(ctx, &pluginv1.GetAccountRequest{AccountId: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := out.GetAccount()
	if a.GetId() != 1 || a.GetCredentialsJson() != `{"access_key":"AK","secret_key":"SK"}` || a.GetProxy().GetPassword() != "pw" {
		t.Fatalf("account = %v", a)
	}
	if acc.audits != 1 || len(acc.calls) != 1 || acc.calls[0] != "read:volc" {
		t.Fatalf("audits = %d, calls = %v", acc.audits, acc.calls)
	}

	// An account of another plugin's account type is NOT_FOUND, never
	// PERMISSION_DENIED: the two must be indistinguishable to the caller, or
	// the call becomes an account-id oracle.
	_, err = h.GetAccount(ctx, &pluginv1.GetAccountRequest{AccountId: 9})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("other plugin's account: %v (%v)", status.Code(err), err)
	}
	_, missing := h.GetAccount(ctx, &pluginv1.GetAccountRequest{AccountId: 4242})
	if status.Code(missing) != codes.NotFound || missing.Error() != err.Error() {
		t.Fatalf("existence leak: %v vs %v", err, missing)
	}
	if acc.audits != 1 {
		t.Fatalf("a refused read was audited as a disclosure: %d", acc.audits)
	}

	if _, err := h.GetAccount(ctx, &pluginv1.GetAccountRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing account id: %v", err)
	}
}

// Without an account module wired in, both calls fail closed.
func TestHostAccountsUnavailable(t *testing.T) {
	ctx := context.Background()
	h, _ := accountsEnv(t, registry.Grants{})
	h.i.rt.o.Accounts = nil
	if _, err := h.ListAccounts(ctx, &pluginv1.ListAccountsRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("list: %v", err)
	}
	if _, err := h.GetAccount(ctx, &pluginv1.GetAccountRequest{AccountId: 1}); status.Code(err) != codes.Unavailable {
		t.Fatalf("read: %v", err)
	}
}

type fakeUsers struct{ calls int }

func (f *fakeUsers) LookupUsers(_ context.Context, ids []int64) ([]core.UserSummary, error) {
	f.calls++
	return []core.UserSummary{{ID: ids[0], DisplayName: "Ann", Status: "active"}}, nil
}

// GetUsers needs users.read and at most GetUsersMaxIDs ids (CONTRACTS §75.3).
func TestHostGetUsers(t *testing.T) {
	ctx := context.Background()
	h, _ := accountsEnv(t, registry.Grants{})
	users := &fakeUsers{}
	h.i.rt.o.Users = users
	if _, err := h.GetUsers(ctx, &pluginv1.GetUsersRequest{UserIds: []int64{1}}); status.Code(err) != codes.PermissionDenied || users.calls != 0 {
		t.Fatalf("without users.read: %v", err)
	}
	h, _ = accountsEnv(t, registry.Grants{"users.read": json.RawMessage(`{}`)})
	h.i.rt.o.Users = users
	if _, err := h.GetUsers(ctx, &pluginv1.GetUsersRequest{UserIds: make([]int64, GetUsersMaxIDs+1)}); status.Code(err) != codes.InvalidArgument || users.calls != 0 {
		t.Fatalf("too many ids: %v", err)
	}
	if out, err := h.GetUsers(ctx, &pluginv1.GetUsersRequest{UserIds: []int64{7}}); err != nil || len(out.GetUsers()) != 1 ||
		out.GetUsers()[0].GetId() != 7 || out.GetUsers()[0].GetDisplayName() != "Ann" || out.GetUsers()[0].GetStatus() != "active" {
		t.Fatalf("lookup: %v %v", out, err)
	}
}
