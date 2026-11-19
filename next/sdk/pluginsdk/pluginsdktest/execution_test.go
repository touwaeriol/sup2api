package pluginsdktest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The fake applies the host's own checks, whatever the SDK checked before:
// a plugin that bypasses the SDK's helpers is held to the same contract.
func TestFakeHostRelayChecks(t *testing.T) {
	ctx := context.Background()
	ok := &pluginv1.UpstreamRequest{Url: "https://up.example.com/v1", Headers: map[string]string{"authorization": "k"}}
	f := NewFakeHost()
	f.ExternalServices = []string{"up.example.com"}
	relay := func(e *Execution, r *pluginv1.UpstreamRequest, wait bool) codes.Code {
		_, err := f.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: e.Token(), Request: r, Wait: wait})
		return status.Code(err)
	}

	plain := f.Begin(&pluginv1.ExecuteRequest{})
	for name, tc := range map[string]struct {
		request *pluginv1.UpstreamRequest
		wait    bool
		want    codes.Code
	}{
		"upper-case header": {&pluginv1.UpstreamRequest{Url: ok.Url, Headers: map[string]string{"Authorization": "k"}}, false, codes.InvalidArgument},
		"transport header":  {&pluginv1.UpstreamRequest{Url: ok.Url, DefaultHeaders: map[string]string{"accept-encoding": "gzip"}}, false, codes.InvalidArgument},
		"relative url":      {&pluginv1.UpstreamRequest{Url: "/v1"}, false, codes.InvalidArgument},
		"wait without flag": {ok, true, codes.FailedPrecondition},
	} {
		if got := relay(plain, tc.request, tc.wait); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	if len(plain.Relays()) != 0 || len(plain.RelayErrors()) != 4 {
		t.Fatalf("refused relays were recorded as relayed: %v %v", plain.Relays(), plain.RelayErrors())
	}
	if got := relay(plain, ok, false); got != codes.OK {
		t.Fatalf("first relay: %v", got)
	}
	if got := relay(plain, ok, false); got != codes.FailedPrecondition {
		t.Fatalf("second relay: %v", got)
	}
	if relays := plain.Relays(); len(relays) != 1 || relays[0].Wait || relays[0].Request != ok {
		t.Fatalf("relays: %v", relays)
	}

	waiting := f.Begin(&pluginv1.ExecuteRequest{TaskSubmission: true})
	if got := relay(waiting, ok, true); got != codes.Unimplemented {
		t.Fatalf("waited relay without RelayFunc: %v", got)
	}
	if got := relay(f.BeginMonitor(&pluginv1.PollRequest{}), ok, false); got != codes.PermissionDenied {
		t.Fatalf("relay in Monitor: %v", got)
	}

	plain.End()
	if got := relay(plain, ok, false); got != codes.PermissionDenied {
		t.Fatalf("relay after the call returned: %v", got)
	}
	if _, err := f.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: "made-up", Url: ok.Url}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("fetch with a token the host never issued: %v", err)
	}
	if _, err := f.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: waiting.Token(), Url: "ftp://up.example.com"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("fetch of a non-http URL: %v", err)
	}
}

func TestRelayedFailsWithoutExactlyOneRelay(t *testing.T) {
	f := NewFakeHost()
	f.ExternalServices = []string{"up.example.com"}
	e := f.Begin(&pluginv1.ExecuteRequest{})
	probe := &fatalProbe{TB: t}
	e.Relayed(probe)
	if !probe.failed {
		t.Fatal("no relay passed Relayed")
	}
	_, _ = f.RelayUpstream(context.Background(), &pluginv1.RelayUpstreamRequest{ExecutionToken: e.Token(), Request: &pluginv1.UpstreamRequest{Url: "https://up.example.com"}})
	probe.failed = false
	if e.Relayed(probe); probe.failed {
		t.Fatal("one relay failed Relayed")
	}
	e.Err = status.Error(codes.Internal, "boom")
	if e.Relayed(probe); !probe.failed {
		t.Fatal("a failed call passed Relayed")
	}
}

type fatalProbe struct {
	testing.TB
	failed bool
}

func (p *fatalProbe) Helper()               {}
func (p *fatalProbe) Fatalf(string, ...any) { p.failed = true }

// A relay reaches the hosts a fetch does (manifest.Manifest.
// UpstreamHostAllowed), a WebSocket handshake by its host alike; another
// host is refused as the host refuses it, INVALID_ARGUMENT, and the request
// counts as not relayed.
func TestFakeHostRelayHosts(t *testing.T) {
	ctx := context.Background()
	relayAccount := &pluginv1.Account{Type: "relay", SettingsJson: `{"base_url":"https://relay.test:8443/v1"}`}
	f := NewFakeHost()
	f.Manifest = &manifest.Manifest{
		ExternalServices: []string{"api.example.com"},
		AccountTypes:     []manifest.AccountType{{ID: "relay", UpstreamFields: []string{"base_url"}}},
	}
	for url, want := range map[string]codes.Code{
		"https://api.example.com/v1":     codes.OK,
		"https://eu.api.example.com/v1":  codes.OK,
		"wss://api.example.com/v1/ws":    codes.OK,
		"https://relay.test/v1/chat":     codes.OK,
		"https://sub.relay.test/v1/chat": codes.InvalidArgument,
		"https://other.test/v1":          codes.InvalidArgument,
		"wss://other.test/v1/ws":         codes.InvalidArgument,
	} {
		e := f.Begin(&pluginv1.ExecuteRequest{Account: relayAccount})
		_, err := f.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: e.Token(), Request: &pluginv1.UpstreamRequest{Url: url}})
		if status.Code(err) != want || (want != codes.OK) != (len(e.Relays()) == 0) {
			t.Errorf("%s: %v, relays %d, want %v", url, err, len(e.Relays()), want)
		}
	}
}

// A fetch reaches the manifest's externalServices and the hosts of the
// account's own upstream addresses, as the host decides
// (manifest.Manifest.UpstreamHostAllowed), for an Execute and a Monitor call
// alike; nothing without a manifest.
func TestFakeHostFetchHosts(t *testing.T) {
	ctx := context.Background()
	relayAccount := &pluginv1.Account{Type: "relay", SettingsJson: `{"base_url":"https://relay.test:8443/v1"}`}
	keyAccount := &pluginv1.Account{Type: "apikey", SettingsJson: `{"base_url":"https://relay.test"}`}
	f := NewFakeHost()
	f.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	fetch := func(e *Execution, url string) codes.Code {
		_, err := f.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: e.Token(), Url: url})
		return status.Code(err)
	}
	if got := fetch(f.Begin(&pluginv1.ExecuteRequest{Account: relayAccount}), "https://api.example.com/x"); got != codes.PermissionDenied {
		t.Fatalf("fetch without a manifest: %v", got)
	}
	f.Manifest = &manifest.Manifest{
		ExternalServices: []string{"api.example.com:443"},
		AccountTypes:     []manifest.AccountType{{ID: "relay", UpstreamFields: []string{"base_url"}}, {ID: "apikey"}},
	}
	for name, tc := range map[string]struct {
		e    *Execution
		url  string
		want codes.Code
	}{
		"external service":          {f.Begin(&pluginv1.ExecuteRequest{Account: keyAccount}), "https://API.example.com/x", codes.OK},
		"subdomain of one":          {f.Begin(&pluginv1.ExecuteRequest{}), "https://eu.api.example.com/x", codes.OK},
		"the account's address":     {f.Begin(&pluginv1.ExecuteRequest{Account: relayAccount}), "http://relay.test/tasks/1", codes.OK},
		"the account's, in Monitor": {f.BeginMonitor(&pluginv1.PollRequest{Account: relayAccount}), "https://relay.test/tasks/1", codes.OK},
		"a type with no address":    {f.Begin(&pluginv1.ExecuteRequest{Account: keyAccount}), "https://relay.test/x", codes.PermissionDenied},
		"another host":              {f.BeginMonitor(&pluginv1.PollRequest{Account: relayAccount}), "https://other.test/x", codes.PermissionDenied},
		"no account in Monitor":     {f.BeginMonitor(&pluginv1.PollRequest{}), "https://relay.test/x", codes.PermissionDenied},
	} {
		if got := fetch(tc.e, tc.url); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	f.ExternalServices = []string{"other.test"}
	if got := fetch(f.Begin(&pluginv1.ExecuteRequest{}), "https://api.example.com/x"); got != codes.PermissionDenied {
		t.Errorf("ExternalServices did not replace the manifest's: %v", got)
	}
}

// A relayed body has no bound, whatever the plugin was handed, as on the
// host.
func TestFakeHostRelayBodyUnbounded(t *testing.T) {
	f := NewFakeHost()
	f.ExternalServices = []string{"up.example.com"}
	for _, tc := range []struct{ handed, relayed int }{{0, 2 << 20}, {3 << 20, 5 << 20}} {
		e := f.Begin(&pluginv1.ExecuteRequest{Body: make([]byte, tc.handed)})
		_, err := f.RelayUpstream(context.Background(), &pluginv1.RelayUpstreamRequest{ExecutionToken: e.Token(),
			Request: &pluginv1.UpstreamRequest{Url: "https://up.example.com", Body: make([]byte, tc.relayed)}})
		if err != nil {
			t.Errorf("handed %d, relayed %d: %v", tc.handed, tc.relayed, err)
		}
	}
}

// A ws(s) URL is refused on a protocol the fake knows to be HTTP only.
func TestFakeHostWebSocketURL(t *testing.T) {
	f := NewFakeHost()
	f.ExternalServices = []string{"up.example.com"}
	f.Manifest = &manifest.Manifest{Platforms: []manifest.Platform{{ID: "p", Endpoints: []manifest.Endpoint{
		{ID: "chat", Protocol: "p.chat", Kind: manifest.EndpointKindProxy},
		{ID: "live", Protocol: "p.live", Kind: manifest.EndpointKindWebSocket},
	}}}}
	for protocol, want := range map[string]codes.Code{
		"p.chat":              codes.InvalidArgument,
		"openai.responses":    codes.InvalidArgument,
		"p.live":              codes.OK,
		"openai.responses_ws": codes.OK,
		"unknown.protocol":    codes.OK,
	} {
		e := f.Begin(&pluginv1.ExecuteRequest{Meta: &pluginv1.RequestMeta{Protocol: protocol}})
		_, err := f.RelayUpstream(context.Background(), &pluginv1.RelayUpstreamRequest{ExecutionToken: e.Token(),
			Request: &pluginv1.UpstreamRequest{Method: "GET", Url: "wss://up.example.com/live"}})
		if status.Code(err) != want {
			t.Errorf("%s: %v, want %v", protocol, err, want)
		}
	}
}
