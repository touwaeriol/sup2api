package main

import (
	"testing"
)

// Both embedding methods answer in Google's shape on both versions, with
// the prompt tokens in usageMetadata; a content without parts is refused.
func TestGeminiEmbed(t *testing.T) {
	_, srv := newTestServer(t)
	key := map[string]string{"x-goog-api-key": "AIza-mock-1"}
	for _, version := range []string{"v1beta", "v1"} {
		prefix := "/" + version + "/models/gemini-embedding-001"
		m := decode(t, post(t, srv, prefix+":embedContent", key, `{"content":{"parts":[{"text":"hi"}]}}`))
		if values, _ := get(m, "embedding", "values").([]any); len(values) != 8 || num(get(m, "usageMetadata", "promptTokenCount")) != 120 {
			t.Fatalf("%s embedContent = %v", version, m)
		}
		m = decode(t, post(t, srv, prefix+":batchEmbedContents", key,
			`{"requests":[{"content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}]}`))
		if embeddings, _ := m["embeddings"].([]any); len(embeddings) != 2 || num(get(m, "usageMetadata", "promptTokenCount")) != 120 {
			t.Fatalf("%s batchEmbedContents = %v", version, m)
		}
	}
	if resp := post(t, srv, "/v1beta/models/gemini-embedding-001:embedContent", key, `{"content":{}}`); resp.StatusCode != 400 {
		t.Fatalf("no parts = %d", resp.StatusCode)
	}
	if resp := post(t, srv, "/v1beta/models/gemini-embedding-001:batchEmbedContents", key, `{"requests":[]}`); resp.StatusCode != 400 {
		t.Fatalf("no requests = %d", resp.StatusCode)
	}
}
