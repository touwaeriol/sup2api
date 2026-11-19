package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenRequired are the samples every pair must have (CONVERT-MATRIX
// §10.1), with what each must contain beyond the request side.
var goldenRequired = []struct {
	name     string
	upstream string // "": none needed; "any", "sse", or "both" (json and sse)
}{
	{"text_basic", "both"},
	{"image_input", ""},
	{"tools_call", "sse"},
	{"tool_roundtrip", ""},
	{"reasoning", "any"},
	{"finish_length", "any"},
	{"usage_cache", "any"},
	{"error_midstream", "sse"},
	{"lossy", ""},
	{"impossible", ""},
}

// goldenImagesRequired are the samples of the image pairs (CONVERT-MATRIX
// §9.9): the image API has no conversation, tools or reasoning to sample.
var goldenImagesRequired = []struct {
	name     string
	upstream string
}{
	{"text_basic", "both"},
	{"no_instructions", ""},
	{"two_images", "any"},
	{"partial_image", "sse"},
	{"no_image", "both"},
	{"image_failed", "both"},
	{"image_and_failed", "both"},
	{"no_usage", "any"},
	{"error_event", "sse"},
	{"error_midstream", "sse"},
	{"lossy", ""},
	{"impossible", ""},
}

// imagePair reports an image pair (imagesResponses).
func imagePair(p PairInfo) bool { return p.From == protocolImages || p.From == protocolImagesEdit }

// goldenSpecs lists the required samples of a pair: pairs to Anthropic
// also need max_tokens_default and thinking_tool_loop, compositions
// compose_equals_chain.
func goldenSpecs(p PairInfo) map[string]string {
	out := map[string]string{}
	required := goldenRequired
	if imagePair(p) {
		required = goldenImagesRequired
	}
	for _, c := range required {
		out[c.name] = c.upstream
	}
	if p.To == "anthropic.messages" {
		out["max_tokens_default"], out["thinking_tool_loop"] = "", ""
	}
	if len(p.Hops) == 3 {
		out["compose_equals_chain"] = "both"
	}
	return out
}

func TestGoldenCoverage(t *testing.T) {
	known := map[string]bool{}
	for _, p := range Default().Pairs() {
		pair := p.From + "~" + p.To
		known[pair] = true
		for name, up := range goldenSpecs(p) {
			dir := filepath.Join("testdata", "golden", pair, name)
			for _, problem := range caseProblems(t, dir, name, up, lossyCategories(p)) {
				t.Errorf("%s/%s: %s", pair, name, problem)
			}
		}
	}
	dirs, _ := filepath.Glob(filepath.Join("testdata", "golden", "*"))
	for _, dir := range dirs {
		if !known[filepath.Base(dir)] {
			t.Errorf("%s: no such converter pair", dir)
		}
	}
}

// caseProblems lists what a required sample lacks.
func caseProblems(t *testing.T, dir, name, upstream string, lossy []LossCategory) []string {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return []string{"missing"}
	}
	has := func(f string) bool { return fileExists(filepath.Join(dir, f)) }
	var out []string
	if !has("in.request.json") {
		out = append(out, "no in.request.json")
	}
	switch {
	case name == "impossible":
		if !has("want.request_error.txt") {
			out = append(out, "no want.request_error.txt")
		}
		return out
	case !has("want.request.json") || !has("want.losses.json"):
		out = append(out, "no want.request.json or want.losses.json")
	}
	var js, sse bool
	for _, up := range loadCase(t, dir).upstreams(t, dir) {
		js = js || strings.HasSuffix(up, ".json")
		sse = sse || strings.HasSuffix(up, ".sse")
	}
	switch {
	case upstream == "any" && !js && !sse,
		upstream == "sse" && !sse,
		upstream == "both" && !(js && sse):
		out = append(out, "needs upstream reply: "+upstream)
	}
	if name == "lossy" {
		out = append(out, lossyProblems(dir, lossy)...)
	}
	return out
}

// lossyCategories are the loss categories the lossy sample of a pair must
// show: every one, but only other for an image pair (an image request has
// no tools, reasoning or input images to lose).
func lossyCategories(p PairInfo) []LossCategory {
	if imagePair(p) {
		return []LossCategory{LossOther}
	}
	return []LossCategory{LossTool, LossReasoning, LossImage, LossOther}
}

// lossyProblems checks the lossy sample loses something of each category.
func lossyProblems(dir string, categories []LossCategory) []string {
	got := map[LossCategory][]string{}
	for _, f := range []string{"want.losses.json", "want.response_losses.json"} {
		var m map[LossCategory][]string
		if raw, err := os.ReadFile(filepath.Join(dir, f)); err == nil && json.Unmarshal(raw, &m) == nil {
			for c, paths := range m {
				got[c] = append(got[c], paths...)
			}
		}
	}
	var out []string
	for _, c := range categories {
		if len(got[c]) == 0 {
			out = append(out, "lossy loses nothing of category "+string(c))
		}
	}
	return out
}
