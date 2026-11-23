package gateway

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
)

// Gemini's /v1 endpoints are served like the /v1beta ones (CONTRACTS §134):
// same protocol, the plugin told the client's endpoint so it keeps the
// version upstream, the usage record naming the /v1 endpoint.
func TestGeminiV1Endpoints(t *testing.T) {
	e, _, _, gp := apiEnv(t)
	gen := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}}
	r := e.post("/v1/models/gemini-2.5-pro:generateContent", gen, map[string]string{"x-goog-api-key": testKey})
	if r.status != 200 {
		t.Fatalf("v1 generate: %d %s", r.status, r.body)
	}
	m := gp.builds[0].GetMeta()
	if m.GetEndpoint() != "/v1/models/:model:generateContent" || m.GetClientProtocol() != gemini.ProtocolGenerate ||
		m.GetProtocol() != gemini.ProtocolGenerate || gemini.Version(m) != gemini.VersionV1 {
		t.Fatalf("v1 meta %+v", m)
	}
	rec := e.record()
	if !rec.Success || rec.Protocol != gemini.ProtocolGenerate || rec.Endpoint != "/v1/models/:model:generateContent" {
		t.Fatalf("v1 record %+v", rec)
	}
	r = e.post("/v1/models/gemini-2.5-pro:countTokens", gen, map[string]string{"x-goog-api-key": testKey})
	if r.status != 200 {
		t.Fatalf("v1 countTokens: %d %s", r.status, r.body)
	}
}
