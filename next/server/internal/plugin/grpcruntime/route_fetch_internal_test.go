package grpcruntime

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// FetchWithProxyTicket makes fakeAccounts a core.ProxyTicketFetcher: it
// records the plugin, the user of the call's context and the ticket.
func (f *fakeAccounts) FetchWithProxyTicket(ctx context.Context, key string, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	uid, _ := core.UserID(ctx)
	f.calls = append(f.calls, fmt.Sprintf("ticket:%s:%d:%s", key, uid, in.GetProxyTicket()))
	return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
}

// httpRPC plays the plugin's HTTPService.
type httpRPC struct {
	handle func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error)
}

func (r httpRPC) HandleHTTP(ctx context.Context, in *pluginv1.HTTPRequest, _ ...grpc.CallOption) (*pluginv1.HTTPResponse, error) {
	return r.handle(ctx, in)
}

// A console route call of a user carries an execution token for the
// plugin's fetches (CONTRACTS §117): each must name a proxy ticket and an
// externalServices host, goes to the account module with the call's user,
// and the token is void once the call returns. A call without a user gets
// none; a fetch of an Execute call may not name a ticket.
func TestRouteCallFetch(t *testing.T) {
	h, acc := accountsEnv(t, nil)
	h.i.pkg.Manifest.ExternalServices = []string{"auth.example.com"}
	p := &proc{limits: limitsOf(h.i.launchSpec())}
	h.p = p
	h.i.proc.Store(p)
	ctx := core.WithUserID(context.Background(), 7)
	fetch := func(token, url, ticket string) error {
		_, err := h.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: token, Method: "POST", Url: url, ProxyTicket: ticket})
		return err
	}

	var token string
	got := map[string]codes.Code{}
	p.http = httpRPC{handle: func(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		token = in.GetExecutionToken()
		got["ticket"] = status.Code(fetch(token, "https://auth.example.com/oauth/token", "T"))
		got["no ticket"] = status.Code(fetch(token, "https://auth.example.com/oauth/token", ""))
		got["other host"] = status.Code(fetch(token, "https://evil.example.net/x", "T"))
		_, err := h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: token, Request: &pluginv1.UpstreamRequest{Url: "https://auth.example.com/x"}})
		got["relay"] = status.Code(err)
		return &pluginv1.HTTPResponse{Status: 200}, nil
	}}
	if _, err := (httpAdapter{h.i}).HandleHTTP(ctx, &pluginv1.HTTPRequest{Caller: &pluginv1.Caller{UserId: 7}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]codes.Code{"ticket": codes.OK, "no ticket": codes.PermissionDenied, "other host": codes.PermissionDenied, "relay": codes.PermissionDenied}
	if len(token) != 64 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("token %q, fetches %v, want %v", token, got, want)
	}
	if fmt.Sprint(acc.calls) != "[ticket:volc:7:T]" {
		t.Fatalf("account module calls: %v", acc.calls)
	}
	if err := fetch(token, "https://auth.example.com/oauth/token", "T"); status.Code(err) != codes.PermissionDenied || len(acc.calls) != 1 {
		t.Fatalf("fetch after the call returned: %v", err)
	}

	p.http = httpRPC{handle: func(_ context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		token = in.GetExecutionToken()
		return &pluginv1.HTTPResponse{Status: 200}, nil
	}}
	if _, err := (httpAdapter{h.i}).HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Caller: &pluginv1.Caller{}}); err != nil || token != "" {
		t.Fatalf("a call without a user: token %q, %v", token, err)
	}
}

func TestExecuteFetchRefusesProxyTicket(t *testing.T) {
	side := &pluginSide{}
	_, adapter := executionInstance(t, side)
	var code codes.Code
	side.execute = func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		_, err := h.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: in.GetExecutionToken(), Url: "https://api.example.com/x", ProxyTicket: "T"})
		code = status.Code(err)
		return hostUsage, nil
	}
	if _, err := adapter.Execute(context.Background(), &pluginv1.ExecuteRequest{}, core.ExecutionClient{Fetch: ok200}); err != nil || code != codes.PermissionDenied {
		t.Fatalf("fetch with a ticket in Execute: %v, %v", code, err)
	}
}
