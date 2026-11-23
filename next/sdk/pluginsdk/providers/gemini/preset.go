package gemini

import (
	"net/url"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// Protocol ids of the built-in gemini platform (sdk/platforms/gemini.json).
const (
	ProtocolGenerate       = "gemini.generate"
	ProtocolStreamGenerate = "gemini.stream_generate"
	ProtocolCountTokens    = "gemini.count_tokens"
	ProtocolEmbed          = "gemini.embed"
	ProtocolBatchEmbed     = "gemini.batch_embed"
)

// APIKey is a Gemini API key account type: x-goog-api-key on
// https://generativelanguage.googleapis.com (a trailing /v1beta or /v1
// replaced by the route's version), the model of the request's metadata in
// the path of generateContent, streamGenerateContent (SSE), countTokens,
// embedContent and batchEmbedContents (bodies as they are, never
// converted), the Gemini error policy, a one-token generateContent test (streamed for an
// account that only serves streamGenerateContent) and the model listing.
func APIKey(id string) standard.Type {
	return standard.Type{
		ID:      id,
		Spec:    apikey.Spec{DefaultBaseURL: "https://generativelanguage.googleapis.com", Versions: []string{"/v1beta", "/v1"}},
		Auth:    standard.Auth{Header: "x-goog-api-key"},
		Headers: []standard.Header{standard.Set("content-type", standard.Fixed("application/json"))},
		Routes: map[string]standard.Route{
			ProtocolGenerate:       modelRoute(":generateContent"),
			ProtocolStreamGenerate: modelRoute(":streamGenerateContent?alt=sse"),
			ProtocolCountTokens:    modelRoute(":countTokens"),
			ProtocolEmbed:          modelRoute(":embedContent"),
			ProtocolBatchEmbed:     modelRoute(":batchEmbedContents"),
		},
		DefaultProtocol: ProtocolGenerate,
		Classify:        Policy,
		Test:            &standard.Test{Protocol: ProtocolGenerate, Model: testModel, Body: generatePing},
		// The same request streamed, for an account whose model only
		// streams (CONTRACTS §122).
		Tests:  []standard.Test{{Protocol: ProtocolStreamGenerate, Model: testModel, Body: generatePing}},
		Models: &apikey.ModelList{Path: "/v1beta/models?pageSize=1000", IdsPath: "models.#.name", StripPrefix: "models/"},
	}
}

// testModel is the model a test names when the console names none.
const testModel = "gemini-2.5-flash-lite"

// generatePing is the test body of both generate protocols (the model is in
// the path).
func generatePing(string) string { return GenerateContentPingBody(1) }

// modelRoute is a method of a model, /<version>/models/<model><method>: the
// version is the client's (Version), the model the request's meta.model
// without a "models/" prefix, required.
func modelRoute(method string) standard.Route {
	return standard.Route{
		Path: func(r *standard.Request) string {
			return "/" + Version(r.Meta) + "/models/" + url.PathEscape(r.Model) + method
		},
		Model: standard.Model{FromMeta: true, TrimPrefix: "models/", Required: true, Missing: "model is required (path /v1beta/models/{model}:...)"},
	}
}
