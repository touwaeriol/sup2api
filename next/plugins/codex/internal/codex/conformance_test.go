package codex

import (
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
)

// TestConformance runs the platform conformance suite on the inputs of
// executeCases: both account types, the HTTP request and a WebSocket turn.
// The HTTP protocol also gets its non-stream form: the type is streamOnly,
// so the core sends a non-streaming client request upstream as a stream
// (meta.stream false, the body's stream set to true).
func TestConformance(t *testing.T) {
	var cases []conformance.Case
	for _, tc := range executeCases() {
		in := tc.in
		cases = append(cases, conformance.Case{Name: tc.name, Account: in.GetAccount(), Meta: in.GetMeta(), Body: in.GetBody(),
			WebSocket: in.GetMeta().GetProtocol() == ProtocolResponsesWS})
		if in.GetMeta().GetProtocol() == ProtocolResponses {
			meta := &pluginv1.RequestMeta{Protocol: ProtocolResponses, Model: in.GetMeta().GetModel(), ApiKeyId: in.GetMeta().GetApiKeyId()}
			cases = append(cases, conformance.Case{Name: tc.name + " non-stream", Account: in.GetAccount(), Meta: meta, Body: in.GetBody()})
		}
	}
	conformance.Run(t, conformance.Plugin{
		Platform:      mustPlugin(DefaultEndpoints),
		Manifest:      testManifest,
		Protocols:     []string{ProtocolResponses},
		Credentials:   []string{"authorization", "chatgpt-account-id"},
		Authoritative: authoritativeHeaders,
	}, cases...)
}
