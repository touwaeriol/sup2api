package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// geminiEmbedContent is the content of an embedContent request (the
// fields the mock checks).
type geminiEmbedContent struct {
	Content struct {
		Parts []any `json:"parts"`
	} `json:"content"`
}

// geminiEmbedRequests is a batchEmbedContents request.
type geminiEmbedRequests struct {
	Requests []geminiEmbedContent `json:"requests"`
}

// geminiEmbed answers embedContent with {embedding: {values}} and
// batchEmbedContents with {embeddings: [{values}]}, one per request, both
// with usageMetadata.promptTokenCount (ai.google.dev/api/embeddings), the
// behaviour's input tokens.
func (s *server) geminiEmbed(w http.ResponseWriter, id int64, b behaviour, act string, body []byte) {
	n := geminiEmbedCount(act, body)
	if n == 0 {
		s.setStatus(id, 400)
		geminiError(w, 400, "INVALID_ARGUMENT", "mock: content is not specified")
		return
	}
	s.setStatus(id, 200)
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	out := map[string]any{"usageMetadata": map[string]any{"promptTokenCount": b.usage.InputTokens}}
	if act == "embedContent" {
		out["embedding"] = map[string]any{"values": mockVector(0)}
	} else {
		embeddings := make([]any, n)
		for i := range embeddings {
			embeddings[i] = map[string]any{"values": mockVector(i)}
		}
		out["embeddings"] = embeddings
	}
	writeJSON(w, 200, out)
}

// geminiEmbedCount is the number of embeddings a request asks for, 0 when
// a content has no parts (Google refuses it).
func geminiEmbedCount(act string, body []byte) int {
	var batch geminiEmbedRequests
	if act == "embedContent" {
		batch.Requests = make([]geminiEmbedContent, 1)
		_ = json.Unmarshal(body, &batch.Requests[0])
	} else {
		_ = json.Unmarshal(body, &batch)
	}
	for _, r := range batch.Requests {
		if len(r.Content.Parts) == 0 {
			return 0
		}
	}
	return len(batch.Requests)
}

// mockVector is the i-th embedding of a response: 8 deterministic values.
func mockVector(i int) []float64 {
	vec := make([]float64, 8)
	for j := range vec {
		vec[j] = float64((i+1)*(j+1)%7) / 7
	}
	return vec
}
