package usagerules

import (
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The two image fields are standard token fields: counted into Tokens, not
// filed as metrics, warned about when not a number, never negative.
func TestImageTokenFields(t *testing.T) {
	rules := manifest.UsageRules{Semantics: "inclusive", JSON: &manifest.UsageMap{Map: map[string]string{
		manifest.UsageInputTokens:       "usage.in",
		manifest.UsageImageInputTokens:  "usage.img_in",
		manifest.UsageImageOutputTokens: "usage.img_out",
	}}}
	u := New(rules)
	u.ApplyJSON([]byte(`{"usage":{"in":70,"img_in":40,"img_out":900}}`))
	if got, want := u.Tokens(), (core.UsageTokens{Input: 70, ImageInput: 40, ImageOutput: 900}); got != want {
		t.Fatalf("tokens = %+v, want %+v", got, want)
	}
	if len(u.Metrics) != 0 {
		t.Fatalf("image tokens filed as metrics: %v", u.Metrics)
	}

	logs := captureLogs(t)
	u = New(rules)
	u.ApplyJSON([]byte(`{"usage":{"img_in":"many","img_out":-5}}`))
	if got := u.Tokens(); got.ImageInput != 0 || got.ImageOutput != 0 {
		t.Fatalf("tokens = %+v", got)
	}
	if !strings.Contains(logs.String(), "usage field is not a number") || !strings.Contains(logs.String(), manifest.UsageImageInputTokens) {
		t.Fatalf("no warning for a string image count: %s", logs)
	}
}

// imageRules are the usage rules of a built-in openai image endpoint.
func imageRules(t *testing.T, id string) manifest.UsageRules {
	t.Helper()
	for _, p := range platforms.Builtin() {
		for _, e := range p.Endpoints {
			if p.ID == "openai" && e.ID == id {
				return *e.Usage
			}
		}
	}
	t.Fatalf("no openai endpoint %s", id)
	return manifest.UsageRules{}
}

// The rules of /v1/images/generations and /v1/images/edits read the official
// response: the JSON body's usage with or without the output breakdown, and
// the stream's completed event (the only one with usage, whose usage has no
// output breakdown in the reference: the whole output is the image output).
func TestImageEndpointRules(t *testing.T) {
	const withDetails = `{"created":1,"data":[{"b64_json":"aGk="}],"usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},` +
		`"output_tokens":4200,"output_tokens_details":{"image_tokens":4000,"text_tokens":200},"total_tokens":4250}}`
	const noDetails = `{"created":1,"data":[{"b64_json":"aGk="}],"usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},` +
		`"output_tokens":4200,"total_tokens":4250}}`
	for id, event := range map[string]string{"images_generations": "image_generation", "images_edits": "image_edit"} {
		rules := imageRules(t, id)
		if rules.Semantics != "inclusive" {
			t.Errorf("%s semantics %q", id, rules.Semantics)
		}
		for name, c := range map[string]struct {
			body string
			want core.UsageTokens
		}{
			"json with the output breakdown":    {withDetails, core.UsageTokens{Input: 50, Output: 4200, ImageInput: 40, ImageOutput: 4000}},
			"json without the output breakdown": {noDetails, core.UsageTokens{Input: 50, Output: 4200, ImageInput: 40}},
		} {
			u := New(rules)
			u.ApplyJSON([]byte(c.body))
			if got := u.Tokens(); got != c.want || len(u.Metrics) != 0 {
				t.Errorf("%s %s: tokens %+v metrics %v, want %+v", id, name, got, u.Metrics, c.want)
			}
		}

		completed := `{"type":"` + event + `.completed","b64_json":"aGk=","usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},"output_tokens":4000,"total_tokens":4050}}`
		completedWithDetails := `{"type":"` + event + `.completed","b64_json":"aGk=","usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},` +
			`"output_tokens":4200,"output_tokens_details":{"image_tokens":4000,"text_tokens":200},"total_tokens":4250}}`
		for name, c := range map[string]struct {
			data string
			want core.UsageTokens
		}{
			"stream as documented":            {completed, core.UsageTokens{Input: 50, Output: 4000, ImageInput: 40, ImageOutput: 4000}},
			"stream with an output breakdown": {completedWithDetails, core.UsageTokens{Input: 50, Output: 4200, ImageInput: 40, ImageOutput: 4200}},
		} {
			u := New(rules)
			u.ApplySSE(event+".partial_image", []byte(`{"type":"`+event+`.partial_image","b64_json":"aGk=","partial_image_index":0}`))
			if u.Final() || u.Tokens() != (core.UsageTokens{}) {
				t.Fatalf("%s: a partial image counted %+v", id, u.Tokens())
			}
			u.ApplySSE(event+".completed", []byte(c.data))
			if got := u.Tokens(); got != c.want || !u.Final() {
				t.Errorf("%s %s: tokens %+v final %v, want %+v", id, name, got, u.Final(), c.want)
			}
		}
		// The event is matched by its data type when the upstream wrote no
		// event: line.
		u := New(rules)
		u.ApplySSE("", []byte(completed))
		if u.Tokens().ImageOutput != 4000 {
			t.Errorf("%s: unnamed completed event not read: %+v", id, u.Tokens())
		}
	}
}
