package gateway

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"testing"
)

func TestRequiredPrimaryUsageGateDoesNotAffectPlainRequests(t *testing.T) {
	for _, kind := range []string{"plain", "null", "compaction", "context"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			for i := range e.gen.accountTypes {
				for j := range e.gen.accountTypes[i].Type.Platforms {
					e.gen.accountTypes[i].Type.Platforms[j].Usage = map[string]manifest.UsageRules{"anthropic.messages": {Semantics: "exclusive"}}
				}
			}
			b := body(testModel, false)
			switch kind {
			case "null":
				b["compaction"] = nil
			case "compaction":
				b["compaction"] = map[string]any{"type": "summarize"}
			case "context":
				b["context_management"] = map[string]any{"edits": []any{}}
			}
			res := e.messages(b)
			e.record()
			allowed := kind == "plain" || kind == "null"
			if (res.status == 200) != allowed || (!allowed && len(e.up.keys()) != 0) {
				t.Fatalf("kind=%s status=%d calls=%v", kind, res.status, e.up.keys())
			}
		})
	}
}

func TestRequiredPrimaryUsageReadsEndpointDeclarations(t *testing.T) {
	rule := manifest.AdditionalUsageRule{Name: "extra_operation", UsePrimaryModel: true, RequiredBy: []string{"operation.options"}}
	c := &call{pf: manifest.Platform{}, ep: manifest.Endpoint{Usage: &manifest.UsageRules{Additional: []manifest.AdditionalUsageRule{rule}}}, body: []byte(`{"operation":{"options":{}}}`)}
	if c.routeHasRequiredPrimaryUsage(&typeRoute{}) {
		t.Fatal("endpoint requirement ignored")
	}
	if c.routeHasRequiredPrimaryUsage(&typeRoute{usage: manifest.UsageRules{Additional: []manifest.AdditionalUsageRule{{Name: rule.Name}}}}) {
		t.Fatal("untrusted response-model source accepted")
	}
	if !c.routeHasRequiredPrimaryUsage(&typeRoute{usage: manifest.UsageRules{Additional: []manifest.AdditionalUsageRule{{Name: rule.Name, UsePrimaryModel: true}}}}) {
		t.Fatal("explicit primary contract rejected")
	}
}
