package grpcruntime

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// blockedHosts is the administrator's plugin network access (CONTRACTS
// §113) refusing the listed hosts and their subdomains.
type blockedHosts []string

func (b blockedHosts) PluginRequestRefusal(_ context.Context, _ string, target *url.URL) string {
	if manifest.ExternalServiceHost(b, target.Hostname()) {
		return target.Hostname() + " is on the blocked list of plugin network access"
	}
	return ""
}

// A fetch - of Execute, of Monitor, or of a console route call with a proxy
// ticket - to a host the administrator's plugin network access refuses is
// PERMISSION_DENIED and nothing is sent, even when the host is one the
// manifest declares or the account's own upstream address.
func TestFetchToBlockedHostRefused(t *testing.T) {
	account := &pluginv1.Account{Type: "apikey", SettingsJson: `{"base_url":"https://relay.operator.example/v1"}`}
	var sent atomic.Int32
	count := func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		sent.Add(1)
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	try := func(ctx context.Context, h *hostServer, token string) {
		for raw, code := range map[string]codes.Code{
			"https://api.example.com/x":                 codes.OK,
			"https://sub.blocked.example.com/x":         codes.PermissionDenied, // declared through example.com, blocked
			"https://relay.operator.example/v1/tasks/1": codes.PermissionDenied, // the account's own address, blocked
		} {
			_, err := fetch(ctx, h, token, raw, 0)
			if status.Code(err) != code {
				t.Errorf("%s: %v, want %s", raw, err, code)
			}
			if code != codes.OK && !strings.Contains(status.Convert(err).Message(), "is on the blocked list of plugin network access") {
				t.Errorf("%s: %v", raw, err)
			}
		}
	}
	side := &pluginSide{
		execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			try(ctx, h, in.GetExecutionToken())
			return hostUsage, nil
		},
		monitor: func(ctx context.Context, h *hostServer, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
			try(ctx, h, in.GetExecutionToken())
			return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{}}, nil
		},
	}
	i, a := executionInstance(t, side)
	i.pkg.Manifest.ExternalServices = []string{"api.example.com", "example.com"}
	i.pkg.Manifest.AccountTypes[0].UpstreamFields = []string{"base_url"}
	i.rt.o.NetworkAccess = blockedHosts{"blocked.example.com", "relay.operator.example"}
	if _, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{Account: account}, core.ExecutionClient{Fetch: count}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Monitor(context.Background(), &pluginv1.PollRequest{Account: account}, count); err != nil {
		t.Fatal(err)
	}
	if sent.Load() != 2 {
		t.Fatalf("sent %d, want only the two allowed fetches", sent.Load())
	}
}

func TestRouteFetchToBlockedHostRefused(t *testing.T) {
	h, acc := accountsEnv(t, nil)
	h.i.pkg.Manifest.ExternalServices = []string{"auth.example.com"}
	h.i.rt.o.NetworkAccess = blockedHosts{"auth.example.com"}
	p := &proc{limits: limitsOf(h.i.launchSpec())}
	h.p = p
	h.i.proc.Store(p)
	ctx := core.WithUserID(context.Background(), 7)
	var err error
	p.http = httpRPC{handle: func(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		_, err = h.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: in.GetExecutionToken(), Method: "POST",
			Url: "https://auth.example.com/oauth/token", ProxyTicket: "T"})
		return &pluginv1.HTTPResponse{Status: 200}, nil
	}}
	if _, herr := (httpAdapter{h.i}).HandleHTTP(ctx, &pluginv1.HTTPRequest{Caller: &pluginv1.Caller{UserId: 7}}); herr != nil {
		t.Fatal(herr)
	}
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "blocked list of plugin network access") {
		t.Fatalf("route fetch: %v", err)
	}
	if len(acc.calls) != 0 {
		t.Fatalf("sent: %v", acc.calls)
	}
}
