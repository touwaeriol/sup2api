package pluginsdk_test

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const routeManifest = `{
  "apiVersion": 1, "key": "rt", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "externalServices": ["up.example.com"]
}`

// A console route call of a signed-in user may fetch with the proxy ticket
// of an account being authorized (CONTRACTS §117): the SDK binds the call's
// execution token to the handler's context and passes the ticket on. Without
// a ticket, to a host outside externalServices, without a user or after the
// call returned, the fetch is refused.
func TestRouteFetch(t *testing.T) {
	type outcome struct {
		answer *pluginv1.ExecutionHTTPResponse
		err    error
	}
	var got outcome
	var late context.Context
	r := pluginsdk.NewRouter()
	r.Handle(http.MethodPost, "/fetch", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		url, ticket := "https://up.example.com/token", "sealed-ticket"
		switch string(req.GetBody()) {
		case "no-ticket":
			ticket = ""
		case "elsewhere":
			url = "https://evil.example.net/token"
		}
		got.answer, got.err = pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Method: "POST", Url: url, ProxyTicket: ticket})
		late = ctx
		return &pluginv1.HTTPResponse{Status: 204}, nil
	})
	h := pluginsdktest.Start(t, r, pluginsdktest.Options{Manifest: []byte(routeManifest)})
	var seen []*pluginv1.ExecutionHTTPRequest
	h.Host.ExecuteHTTPFunc = func(_ context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		seen = append(seen, in)
		return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil
	}

	h.Do("POST", "/fetch", nil, "with-ticket")
	if got.err != nil || got.answer.GetStatus() != 200 || len(seen) != 1 {
		t.Fatalf("fetch with a ticket: %v %v (%d seen)", got.answer, got.err, len(seen))
	}
	if in := seen[0]; in.GetProxyTicket() != "sealed-ticket" || in.GetExecutionToken() == "" || in.GetUrl() != "https://up.example.com/token" {
		t.Fatalf("host saw %v", in)
	}
	if _, err := pluginsdk.Fetch(late, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/x", ProxyTicket: "t"}); err == nil {
		t.Fatal("fetch after the route call returned")
	}

	for body, want := range map[string]codes.Code{"no-ticket": codes.PermissionDenied, "elsewhere": codes.PermissionDenied} {
		h.Do("POST", "/fetch", nil, body)
		if status.Code(got.err) != want {
			t.Errorf("%s: %v, want %v", body, got.err, want)
		}
	}
	h.DoAs(0, "POST", "/fetch", nil, "with-ticket")
	if status.Code(got.err) != codes.FailedPrecondition {
		t.Errorf("fetch in a call without a user: %v", got.err)
	}
	if len(seen) != 1 {
		t.Fatalf("refused fetches reached the host: %d", len(seen))
	}
}

// The fetches of an Execute call go through the account's proxy: a proxy
// ticket there is refused.
func TestExecuteFetchRefusesProxyTicket(t *testing.T) {
	var err error
	p := &scripted{execute: func(ctx context.Context, _ *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		_, err = pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/token", ProxyTicket: "t"})
		return pluginsdk.ReportClassification(nil), nil
	}}
	h := startScripted(t, p)
	h.Host.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	if ex := h.Execute(context.Background(), executeRequest()); ex.Err != nil || len(ex.Fetches()) != 0 || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err=%v fetch=%v fetches=%d", ex.Err, err, len(ex.Fetches()))
	}
}
