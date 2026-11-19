package grpcruntime

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

// SendPluginAccountRequest makes fakeAccounts a core.PluginAccountRequester.
// The host rule is the account module's, which reads the account
// (account.TestSendPluginAccountRequestHosts); the fake records the
// manifest it is decided on.
func (f *fakeAccounts) SendPluginAccountRequest(_ context.Context, key string, m *manifest.Manifest, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	f.calls = append(f.calls, "request:"+key)
	f.manifest = m
	for _, a := range f.rows[key] {
		if a.ID == in.GetAccountId() {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte("ok")}, nil
		}
	}
	return nil, core.ErrNotFound.WithMessage("account not found")
}

// A plugin holding no grant sends for its own accounts (CONTRACTS §124).
func TestHostSendAccountRequestNeedsNoGrant(t *testing.T) {
	h, acc := accountsEnv(t, registry.Grants{})
	if out, err := h.SendAccountRequest(t.Context(), &pluginv1.SendAccountRequestRequest{AccountId: 1, Url: "https://chatgpt.com/reset"}); err != nil ||
		out.GetStatus() != 200 || len(acc.calls) != 1 {
		t.Fatalf("send: %v %v, calls %v", out, err, acc.calls)
	}
}

// Only an account and an http(s) URL with a host are required: the body,
// the URL and the method are not bounded.
func TestHostSendAccountRequestArguments(t *testing.T) {
	h, acc := accountsEnv(t, registry.Grants{})
	for name, in := range map[string]*pluginv1.SendAccountRequestRequest{
		"no account": {Url: "https://chatgpt.com/reset"},
		"no url":     {AccountId: 1},
		"bad url":    {AccountId: 1, Url: "https://chatgpt.com/%zz"},
	} {
		if _, err := h.SendAccountRequest(t.Context(), in); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, u := range []string{"ftp://chatgpt.com/x", "chatgpt.com/x", "https:///x"} {
		if _, err := h.SendAccountRequest(t.Context(), &pluginv1.SendAccountRequestRequest{AccountId: 1, Url: u}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: %v", u, err)
		}
	}
	if len(acc.calls) != 0 {
		t.Fatalf("invalid requests reached accounts: %v", acc.calls)
	}
	in := &pluginv1.SendAccountRequestRequest{AccountId: 1, Url: "https://chatgpt.com/" + strings.Repeat("a", 64<<10),
		Method: strings.Repeat("P", 64), Body: make([]byte, 1<<20)}
	if _, err := h.SendAccountRequest(t.Context(), in); err != nil {
		t.Fatalf("large request: %v", err)
	}
	if acc.manifest != h.i.pkg.Manifest {
		t.Fatal("the account module did not get the plugin's manifest to decide the host on")
	}
}

func TestHostSendAccountRequestOwnAccountsOnly(t *testing.T) {
	h, acc := accountsEnv(t, registry.Grants{})
	if _, err := h.SendAccountRequest(t.Context(), &pluginv1.SendAccountRequestRequest{AccountId: 9, Url: "https://chatgpt.com/x"}); status.Code(err) != codes.NotFound {
		t.Fatalf("another plugin's account: %v", err)
	}
	if acc.calls[0] != "request:volc" {
		t.Fatalf("called as %v", acc.calls)
	}
	h.i.rt.o.Accounts = nil
	if _, err := h.SendAccountRequest(t.Context(), &pluginv1.SendAccountRequestRequest{AccountId: 1, Url: "https://chatgpt.com/x"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no account module: %v", err)
	}
}
