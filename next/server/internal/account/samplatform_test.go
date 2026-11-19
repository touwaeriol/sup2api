package account

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// chatResponsesConverters is the core's openai.chat -> openai.responses pair.
type chatResponsesConverters struct{}

func (f chatResponsesConverters) Conversion(client, upstream string) (core.Conversion, bool) {
	return fairConversion(client, upstream, f.canConvert(client, upstream))
}

func (chatResponsesConverters) canConvert(client, upstream string) bool {
	return client == "openai.chat" && upstream == "openai.responses"
}

// An endpoint of a supported platform left out of the type's endpoint subset
// is listed as converted when the core converts it to a protocol of the
// subset (CONTRACTS §83.3), as the gateway routes it.
func TestServedEndpointsSamePlatformConversion(t *testing.T) {
	g := testGen(&fakePlatform{})
	for i := range g.plats {
		if g.plats[i].Platform.ID != "openai" {
			continue
		}
		withResponses := ep("POST", "/v1/responses", "openai.responses", "usage")
		g.plats[i].Platform.Endpoints = append(g.plats[i].Platform.Endpoints, withResponses)
		for j := range g.plats[i].Platform.Endpoints {
			e := &g.plats[i].Platform.Endpoints[j]
			e.ID = map[string]string{"openai.chat": "chat_completions", "openai.embeddings": "embeddings", "openai.responses": "responses"}[e.Protocol]
		}
	}
	b := core.AccountTypeBinding{Plugin: core.PluginInfo{Key: "codex"}, Type: manifest.AccountType{ID: "chatgpt",
		Platforms: []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"responses"},
			Adapted: map[string]manifest.AdaptOptions{"chat_completions": {}}}}}}
	want := "POST /v1/responses openai native; POST /v1/chat/completions openai fair"
	if got := endpointList(servedEndpoints(g, b, chatResponsesConverters{})); got != want {
		t.Fatalf("endpoints:\n got %s\nwant %s", got, want)
	}
	// No converter: only the subset.
	if got := endpointList(servedEndpoints(g, b, nil)); got != "POST /v1/responses openai native" {
		t.Fatalf("without converters: %s", got)
	}
	// Not declared in adapted: only the subset (CONTRACTS §83.3).
	undeclared := b
	undeclared.Type.Platforms = []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"responses"}}}
	if got := endpointList(servedEndpoints(g, undeclared, chatResponsesConverters{})); got != "POST /v1/responses openai native" {
		t.Fatalf("not adapted: %s", got)
	}
	// A type serving chat natively lists it once, natively.
	b.Type.Platforms[0].Endpoints = nil
	want = "POST /v1/chat/completions openai native; POST /v1/embeddings openai native; POST /v1/responses openai native"
	if got := endpointList(servedEndpoints(g, b, chatResponsesConverters{})); got != want {
		t.Fatalf("native:\n got %s\nwant %s", got, want)
	}
}
