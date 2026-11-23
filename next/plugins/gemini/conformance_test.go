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
		Platform: gemini.New(),
		Manifest: manifestJSON,
		Protocols: []string{providers.ProtocolGenerate, providers.ProtocolStreamGenerate, providers.ProtocolCountTokens,
			providers.ProtocolEmbed, providers.ProtocolBatchEmbed},
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
	contents := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	embed := `{"content":{"parts":[{"text":"hi"}]}}`
	batch := `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"hi"}]}}]}`
	var out []conformance.Case
	for _, p := range []struct {
		protocol, endpoint, model, body string
		stream                          bool
	}{
		{providers.ProtocolGenerate, "/v1beta/models/:model:generateContent", "gemini-2.5-flash", contents, false},
		{providers.ProtocolStreamGenerate, "/v1/models/:model:streamGenerateContent", "gemini-2.5-flash", contents, true},
		{providers.ProtocolCountTokens, "/v1beta/models/:model:countTokens", "gemini-2.5-flash", contents, false},
		{providers.ProtocolEmbed, "/v1/models/:model:embedContent", "gemini-embedding-001", embed, false},
		{providers.ProtocolBatchEmbed, "/v1beta/models/:model:batchEmbedContents", "gemini-embedding-001", batch, false},
	} {
		out = append(out, conformance.Case{
			Account: &pluginv1.Account{Id: 1, Type: gemini.TypeAPIKey, CredentialsJson: `{"api_key":"AIza-conformance-key-0123"}`, SettingsJson: settings[p.stream]},
			Meta:    &pluginv1.RequestMeta{Protocol: p.protocol, ClientProtocol: p.protocol, Endpoint: p.endpoint, Model: p.model, Stream: p.stream},
			Body:    []byte(p.body),
		})
	}
	return out
}
