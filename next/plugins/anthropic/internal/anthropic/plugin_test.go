package anthropic

import (
	"context"
	"slices"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/standardtest"
)

func start(t *testing.T) *pluginsdktest.Harness {
	t.Helper()
	return pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithInfo("anthropic", "0.4.0")}})
}

// TestCapabilities: one process serves the apikey platform and the model
// catalog route.
func TestCapabilities(t *testing.T) {
	got := start(t).Info.GetCapabilities()
	slices.Sort(got)
	if want := []string{manifest.CapHTTPRoutes, manifest.CapPlatformAdapter}; !slices.Equal(got, want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
}

// TestGolden replays the frozen golden cases of the anthropic plugin
// against the platform the plugin serves, which now refuses a key the
// account form never sends.
func TestGolden(t *testing.T) {
	standardtest.Run(t, "anthropic", "../../manifest.json", New().Platform,
		goldentest.Except("validate valid with other fields", "the account form's keys only: the others are refused"))
}

// TestAPIKey: the type passes the library's checks and keeps what the
// released plugin declared: the anthropic preset with no filter fields.
func TestAPIKey(t *testing.T) {
	at := APIKey()
	if err := standard.Check(at); err != nil {
		t.Fatal(err)
	}
	if at.ID != "apikey" || len(at.FilterFields) != 0 {
		t.Fatalf("type %q filters %v, want apikey filtering nothing", at.ID, at.FilterFields)
	}
}

// TestBaseURL: a request appends the route's whole path to the saved base
// URL less the one /v1 it may end in, so a pasted /v1 is not doubled, a
// relay whose own address ends in /v1 is reached at .../v1/v1, and an
// escaped "/" goes out as it was written (and is no version segment).
func TestBaseURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://x.example.com":         "https://x.example.com/v1/messages",
		"https://x.example.com/v1":      "https://x.example.com/v1/messages",
		"https://x.example.com/v1/v1":   "https://x.example.com/v1/v1/messages",
		"https://x.example.com/p/a%2Fb": "https://x.example.com/p/a%2Fb/v1/messages",
		"https://x.example.com/a%2Fv1":  "https://x.example.com/a%2Fv1/v1/messages",
	} {
		out, err := New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"sk-ant-test"}`, SettingsJson: `{"base_url":"` + base + `"}`},
			Meta:    &pluginv1.RequestMeta{Protocol: anthropic.ProtocolMessages, Model: "claude-sonnet-4-6"},
			Body:    []byte(`{}`),
		})
		if err != nil || out.GetUrl() != want {
			t.Errorf("%s: url %s, %v, want %s", base, out.GetUrl(), err, want)
		}
	}
}
