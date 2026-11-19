package gemini

import (
	"context"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/standardtest"
)

// TestGolden replays the frozen answers of the plugin as it was released
// against the platform main serves: all cases, except that it now refuses
// a key the account form never sends.
func TestGolden(t *testing.T) {
	standardtest.Run(t, "gemini", "../../manifest.json", New(),
		goldentest.Except("validate valid with other fields", "the account form's keys only: the others are refused"))
}

// TestBaseURL: a request appends the route's whole path (/v1beta/...) to
// the saved base URL less the one /v1beta or /v1 it may end in, so a pasted
// /v1beta is not doubled, a pasted /v1 gives way to it, an address ending
// in a version of its own keeps that one, and an escaped "/" goes out as it
// was written.
func TestBaseURL(t *testing.T) {
	const route = "/v1beta/models/gemini-2.5-flash:generateContent"
	for base, root := range map[string]string{
		"https://x.example.com":            "https://x.example.com",
		"https://x.example.com/v1beta":     "https://x.example.com",
		"https://x.example.com/v1":         "https://x.example.com",
		"https://x.example.com/v1/v1beta":  "https://x.example.com/v1",
		"https://x.example.com/v1/v1":      "https://x.example.com/v1",
		"https://x.example.com/p/a%2Fb":    "https://x.example.com/p/a%2Fb",
		"https://x.example.com/a%2Fv1beta": "https://x.example.com/a%2Fv1beta",
	} {
		out, err := New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: &pluginv1.Account{Id: 1, Type: TypeAPIKey, CredentialsJson: `{"api_key":"AIza-test-key"}`, SettingsJson: `{"base_url":"` + base + `"}`},
			Meta:    &pluginv1.RequestMeta{Protocol: gemini.ProtocolGenerate, Model: "gemini-2.5-flash"},
			Body:    []byte(`{}`),
		})
		if err != nil || out.GetUrl() != root+route {
			t.Errorf("%s: url %s, %v, want %s", base, out.GetUrl(), err, root+route)
		}
	}
}
