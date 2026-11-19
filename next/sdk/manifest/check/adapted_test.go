package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// accountTypes[].platforms[].adapted names endpoints of the platform the
// subset leaves out, never a WebSocket or task endpoint, with complete model
// ids (CONTRACTS §121.1).
func TestAccountAdapted(t *testing.T) {
	codex := []string{"responses", "responses_websocket"}
	for _, tc := range []struct {
		name      string
		platform  string
		endpoints []string
		adapted   map[string]manifest.AdaptOptions
		tooling   bool
		field     string
		code      string
	}{
		{"absent", "openai", codex, nil, false, "", ""},
		{"chat and images", "openai", codex, map[string]manifest.AdaptOptions{
			"chat_completions": {}, "images_generations": {Model: "gpt-6-luna"}, "images_edits": {Model: "gpt-6-luna"}}, false, "", ""},
		{"unknown endpoint", "openai", codex, map[string]manifest.AdaptOptions{"speech": {}}, false, ".speech", "unknown_endpoint"},
		{"in subset", "openai", codex, map[string]manifest.AdaptOptions{"responses": {}}, false, ".responses", "native"},
		{"all endpoints", "openai", nil, map[string]manifest.AdaptOptions{"chat_completions": {}}, false, ".chat_completions", "native"},
		{"websocket", "openai", []string{"chat_completions"}, map[string]manifest.AdaptOptions{"responses_websocket": {}}, false, ".responses_websocket", "not_convertible"},
		{"task", "video", []string{"query"}, map[string]manifest.AdaptOptions{"submit": {}}, false, ".submit", "not_convertible"},
		{"model", "openai", codex, map[string]manifest.AdaptOptions{"images_generations": {Model: "gpt-6*"}}, false, ".images_generations.model", "invalid"},
		{"missing platform", "missing", []string{"a"}, map[string]manifest.AdaptOptions{"b": {}}, false, "", "unknown_platform"},
		{"missing platform offline", "missing", []string{"a"}, map[string]manifest.AdaptOptions{"b": {}}, true, "", ""},
		{"missing platform offline model", "missing", []string{"a"}, map[string]manifest.AdaptOptions{"b": {Model: " "}}, true, ".b.model", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := validator{opt: ValidateOptions{Tooling: tc.tooling, OtherPlatforms: []PlatformOwner{{ID: "video", Endpoints: []manifest.Endpoint{
				{ID: "submit", Protocol: "video.submit", Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionSubmit}},
				{ID: "query", Protocol: "video.query", Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionQuery}}}}}}}
			v.accountAdapted("platforms[0]", manifest.AccountPlatform{Platform: tc.platform, Endpoints: tc.endpoints, Adapted: tc.adapted}, nil)
			if tc.code == "" {
				if len(v.errs) != 0 {
					t.Fatalf("unexpected errors: %+v", v.errs)
				}
				return
			}
			if len(v.errs) != 1 || v.errs[0].Code != tc.code || v.errs[0].Field != "platforms[0].adapted"+tc.field {
				t.Fatalf("errors = %+v, want %s at %s", v.errs, tc.code, tc.field)
			}
		})
	}
}

// Adapts answers by the protocol of an endpoint listed in adapted.
func TestAccountPlatformAdapts(t *testing.T) {
	p := builtinPlatform(t, manifest.PlatformOpenAI)
	ap := manifest.AccountPlatform{Platform: "openai", Endpoints: []string{"responses"},
		Adapted: map[string]manifest.AdaptOptions{"images_generations": {Model: "m"}}}
	if !ap.Adapts(&p, "openai.images") || ap.Adapts(&p, "openai.chat") || ap.Adapts(&p, "openai.responses") ||
		(manifest.AccountPlatform{Platform: "openai"}).Adapts(&p, "openai.images") {
		t.Fatal("Adapts")
	}
}

func builtinPlatform(t *testing.T, id string) manifest.Platform {
	t.Helper()
	for _, p := range platforms.Builtin() {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no built-in platform %q", id)
	return manifest.Platform{}
}
