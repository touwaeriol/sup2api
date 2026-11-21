package check

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"testing"
)

func TestAdditionalUsageManifestContract(t *testing.T) {
	p := validPlatform()
	r := manifest.AdditionalUsageRule{Name: "advisor", JSONPath: "usage.iterations", SSEPath: "usage.iterations", SSEEvent: "message_delta", TypePath: "type", TypeValue: "advisor_message", ModelPath: "model", Semantics: "exclusive", Map: map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens"}}
	p.Usage.Additional = []manifest.AdditionalUsageRule{r}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatal(got)
	}
	for _, mutate := range []func(*manifest.AdditionalUsageRule){func(r *manifest.AdditionalUsageRule) { r.Name = "" }, func(r *manifest.AdditionalUsageRule) { r.ModelPath = "" }, func(r *manifest.AdditionalUsageRule) { r.Semantics = "" }, func(r *manifest.AdditionalUsageRule) {
		r.Map = map[string]string{"input_tokens": "input+cache", "output_tokens": "out"}
	}} {
		copy := r
		mutate(&copy)
		p.Usage.Additional = []manifest.AdditionalUsageRule{copy}
		if len(platformCodes(p)) == 0 {
			t.Fatal("invalid additional usage accepted")
		}
	}
	p.Usage.Additional = []manifest.AdditionalUsageRule{r, r}
	if len(platformCodes(p)) == 0 {
		t.Fatal("duplicate counters accepted")
	}
	r.UsePrimaryModel = true
	r.ModelPath = ""
	p.Usage.Additional = []manifest.AdditionalUsageRule{r}
	if codes := platformCodes(p); len(codes) > 0 {
		t.Fatal("host-primary rule rejected", codes)
	}
}
