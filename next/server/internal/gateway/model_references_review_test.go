package gateway

import (
	"encoding/json"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
	"testing"
)

func reviewInlineRule() manifest.RequestModelReference {
	return manifest.RequestModelReference{Name: "advisor", ArrayPath: "messages[].content", ModelPath: "tool.definition.model", Match: map[string]string{"type": "tool_addition", "tool.definition.type": "advisor_20260301"}}
}
func TestReviewNestedReferencesDeclaredLocationsOnly(t *testing.T) {
	raw := []byte(`{"messages":[{"content":"string shorthand"},{"content":[{"type":"tool_addition","tool":{"definition":{"type":"advisor_20260301","model":"allowed"}}},{"type":"tool_use","input":{"messages":[{"content":[{"type":"tool_addition","tool":{"definition":{"type":"advisor_20260301","model":"hidden-input"}}}]}]}}]}]}`)
	got, e := modelReferenceSnapshot(raw, []manifest.RequestModelReference{reviewInlineRule()})
	if e != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, e)
	}
}

func TestReviewSnapshotCountsModelsNotOverrideFacts(t *testing.T) {
	refs := make([]map[string]any, 64)
	for i := range refs {
		refs[i] = map[string]any{"model": "a", "speed": "fast"}
	}
	raw, _ := json.Marshal(map[string]any{"fallbacks": refs})
	_, e := modelReferenceSnapshot(raw, []manifest.RequestModelReference{{Name: "fallback", ArrayPath: "fallbacks", ModelPath: "model", ParameterOverrides: []string{"speed"}}})
	if e != nil {
		t.Fatal("64-model bound incorrectly counts override facts", e)
	}
}

func TestReviewSignedHistoryIdentityMapping(t *testing.T) {
	c := &call{model: "primary", modelRefs: []modelReference{{kind: "advisor", path: "messages.0.content.0.tool_changes.0.tool.definition.model", model: "signed-model", identityOnly: true}}}
	c.ep.Protocol = "anthropic.messages"
	rt := &typeRoute{upstream: "anthropic.messages"}
	raw := []byte(`{"messages":[{"content":[{"tool_changes":[{"tool":{"definition":{"model":"signed-model"}}}]}]}]}`)
	account := &core.AccountRef{ModelMapping: map[string]string{"signed-model": "other"}}
	if _, e := c.mapReferencedModels(raw, account, rt); e == nil {
		t.Fatal("signed model remapped")
	}
	account.ModelMapping = nil
	got, e := c.mapReferencedModels(raw, account, rt)
	if e != nil || gjson.GetBytes(got, "messages.0.content.0.tool_changes.0.tool.definition.model").String() != "signed-model" {
		t.Fatalf("%s %v", got, e)
	}
}

func TestReviewConvertedTargetCannotIntroduceInlineModel(t *testing.T) {
	c := &call{model: "primary"}
	c.ep.Protocol = "openai.chat"
	rt := &typeRoute{upstream: "anthropic.messages", conv: &fakeConv{from: "openai.chat", to: "anthropic.messages"}, modelReferences: []manifest.RequestModelReference{reviewInlineRule()}}
	input := []byte(`{"messages":[{"content":[{"type":"tool_addition","tool":{"definition":{"type":"advisor_20260301","model":"unadmitted"}}}]}]}`)
	if _, e := c.mapReferencedModels(input, &core.AccountRef{}, rt); e == nil {
		t.Fatal("converted target introduced unadmitted inline model")
	}
}
