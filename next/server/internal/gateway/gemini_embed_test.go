package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const (
	embedModel = "gemini-embedding-001"
	// embedNoUsage is a model whose embeddings the test upstream answers
	// without usageMetadata, as an upstream that strips it would.
	embedNoUsage = "embedding-no-usage"
)

var (
	embedBody      = json.RawMessage(`{"content":{"parts":[{"text":"alpha"},{"text":"beta"}]},"taskType":"RETRIEVAL_QUERY"}`)
	batchEmbedBody = json.RawMessage(`{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"alpha"}]}},` +
		`{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"beta"}]}}]}`)
)

// geminiTestPath is /<version>/models/<model><method> with the version the
// gemini preset sends (gemini.Version): the one the Gemini client called.
func geminiTestPath(m *pluginv1.RequestMeta, method string) string {
	return "/" + gemini.Version(m) + "/models/" + m.GetModel() + method
}

// geminiEmbedAnswer is Google's answer to embedContent ({embedding}) and
// batchEmbedContents ({embeddings}), with usageMetadata.promptTokenCount
// unless the model is embedNoUsage.
func geminiEmbedAnswer(w http.ResponseWriter, path string) {
	out := `{"embedding":{"values":[0.1,0.2]}`
	if strings.HasSuffix(path, ":batchEmbedContents") {
		out = `{"embeddings":[{"values":[0.1]},{"values":[0.2]}]`
	}
	if !strings.Contains(path, embedNoUsage) {
		out += `,"usageMetadata":{"promptTokenCount":7}`
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(out + "}"))
}

// embedEnv is apiEnv with prices for the embedding models.
func embedEnv(t *testing.T) (*env, *apiUpstream, *fakePlatform) {
	e, u, _, gp := apiEnv(t)
	e.pricer.mu.Lock()
	for _, m := range []string{embedModel, embedNoUsage} {
		e.pricer.rules[m] = &core.PriceRule{ID: 10, Model: m, Mode: "visual"}
	}
	e.pricer.mu.Unlock()
	return e, u, gp
}

// embedContent and batchEmbedContents go upstream on the version the client
// called, with the body as it is, and are billed on the answer's
// usageMetadata.promptTokenCount.
func TestGeminiEmbedEndpoints(t *testing.T) {
	e, u, gp := embedEnv(t)
	key := map[string]string{"x-goog-api-key": testKey}
	for _, version := range []string{gemini.VersionV1Beta, gemini.VersionV1} {
		for _, c := range []struct {
			method, protocol, answer string
			body                     json.RawMessage
		}{
			{":embedContent", gemini.ProtocolEmbed, "embedding.values", embedBody},
			{":batchEmbedContents", gemini.ProtocolBatchEmbed, "embeddings.1.values", batchEmbedBody},
		} {
			path := "/" + version + "/models/" + embedModel + c.method
			r := e.post(path, c.body, key)
			require.Equal(t, 200, r.status, string(r.body))
			assert.True(t, r.json().Get(c.answer).IsArray(), string(r.body))
			up := u.last()
			assert.Equal(t, path, up.path)
			assert.JSONEq(t, string(c.body), string(up.body))
			m := gp.builds[len(gp.builds)-1].GetMeta()
			assert.Equal(t, c.protocol, m.GetProtocol())
			assert.Equal(t, embedModel, m.GetModel())
			rec := e.record()
			assert.True(t, rec.Success && rec.Billable, "%s %+v", path, rec)
			assert.Equal(t, c.protocol, rec.Protocol)
			assert.Equal(t, "/"+version+"/models/:model"+c.method, rec.Endpoint)
			assert.Equal(t, embedModel, rec.Model)
			assert.Equal(t, core.UsageTokens{Input: 7}, rec.Tokens)
		}
	}
}

// An answer without usageMetadata is unreported like any answer the core
// reads no usage from (CONTRACTS §25.3): the client keeps it, the record
// is free.
func TestGeminiEmbedWithoutUsage(t *testing.T) {
	e, _, _ := embedEnv(t)
	r := e.post("/v1beta/models/"+embedNoUsage+":embedContent", embedBody, map[string]string{"x-goog-api-key": testKey})
	require.Equal(t, 200, r.status, string(r.body))
	rec := e.record()
	assert.False(t, rec.Billable)
	assert.Equal(t, core.UsageErrorUnreported, rec.ErrorType)
	assert.Equal(t, core.UsageExtractUnreported, rec.UsageExtract)
}

// The pre-charge estimate of both methods counts the texts of every
// content (request.promptTextPaths), on both versions.
func TestGeminiEmbedPromptText(t *testing.T) {
	bodies := map[string]json.RawMessage{gemini.ProtocolEmbed: embedBody, gemini.ProtocolBatchEmbed: batchEmbedBody}
	var seen int
	for _, p := range platforms.Builtin() {
		for _, ep := range p.Endpoints {
			body, ok := bodies[ep.Protocol]
			if !ok {
				continue
			}
			seen++
			assert.Equal(t, "alpha\nbeta", extractPromptText(body, ep.Request.PromptTextPaths, 0), ep.ID)
		}
	}
	assert.Equal(t, 4, seen)
}
