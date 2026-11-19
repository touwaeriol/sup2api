package main

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/gemini/internal/gemini"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	providers "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
)

// TestConformance runs the platform plugin rules (design §14.1) over every
// protocol the plugin speaks. The model is in the URL, not in the body.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform:    gemini.New(),
		Manifest:    manifestJSON,
		Protocols:   []string{providers.ProtocolGenerate, providers.ProtocolStreamGenerate, providers.ProtocolCountTokens},
		Credentials: []string{"x-goog-api-key"},
	}, conformanceCases()...)
}

// conformanceCases are the request of each protocol in the one form the
// core sends it in (generateContent and countTokens never stream,
// streamGenerateContent always does), with the endpoint the core maps to it.
func conformanceCases() []conformance.Case {
	// The streaming case runs on a custom base URL (a pasted /v1beta not
	// doubled), the others on the default one.
	settings := map[bool]string{false: `{}`, true: `{"base_url":"https://gemini.example.com/v1beta"}`}
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	var out []conformance.Case
	for _, p := range []struct {
		protocol, endpoint string
		stream             bool
	}{
		{providers.ProtocolGenerate, "generate_content", false},
		{providers.ProtocolStreamGenerate, "stream_generate_content", true},
		{providers.ProtocolCountTokens, "count_tokens", false},
	} {
		out = append(out, conformance.Case{
			Account: &pluginv1.Account{Id: 1, Type: gemini.TypeAPIKey, CredentialsJson: `{"api_key":"AIza-conformance-key-0123"}`, SettingsJson: settings[p.stream]},
			Meta:    &pluginv1.RequestMeta{Protocol: p.protocol, Endpoint: p.endpoint, Model: "gemini-2.5-flash", Stream: p.stream},
			Body:    body,
		})
	}
	return out
}
