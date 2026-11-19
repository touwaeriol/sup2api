package main

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/anthropic/internal/anthropic"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	provider "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
)

// TestConformance runs the platform conformance suite against the plugin as
// it is served: the apikey platform and the catalog route in one process,
// with this manifest. x-api-key is the only authoritative header, as in the
// golden cases.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform:    anthropic.New(),
		Manifest:    manifestJSON,
		Protocols:   []string{provider.ProtocolMessages, provider.ProtocolCountTokens},
		Credentials: []string{"x-api-key"},
	},
		conformanceCase(provider.ProtocolMessages, true, `{"model":"claude-sonnet-4-6","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"Hi"}]}`),
		conformanceCase(provider.ProtocolMessages, false, `{"model":"claude-sonnet-4-6","max_tokens":16,"messages":[{"role":"user","content":"Hi"}]}`),
		conformanceCase(provider.ProtocolCountTokens, false, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Hi"}]}`),
	)
}

// conformanceCase is one request of an apikey account on a custom base URL.
func conformanceCase(protocol string, stream bool, body string) conformance.Case {
	return conformance.Case{
		Account: &pluginv1.Account{
			Id: 1, Name: "conformance", Platform: "anthropic", Type: "apikey",
			CredentialsJson: `{"api_key":"sk-ant-conformance-0123456789"}`,
			SettingsJson:    `{"base_url":"https://anthropic.example.com"}`,
		},
		Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: "claude-sonnet-4-6", Stream: stream},
		Body: []byte(body),
	}
}
