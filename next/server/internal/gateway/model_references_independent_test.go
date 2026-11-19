package gateway

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"testing"
)

func TestReviewAdditionalRouteAndPriceBoundaries(t *testing.T) {
	t.Run("unpriced primary is refused despite a priced extra", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		delete(e.pricer.rules, testModel)
		res := e.messages(advisorBody(false))
		rec := e.record()
		if res.status != 403 || res.json().Get("error.code").String() != core.ErrPriceNotConfigured.Code ||
			rec.ErrorType != errTypePriceNotConfigured || len(e.up.keys()) != 0 {
			t.Fatalf("unpriced primary served: %d %s %+v", res.status, res.body, rec)
		}
	})
	t.Run("account override missing extra metering excluded", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		for i := range e.gen.accountTypes {
			for j := range e.gen.accountTypes[i].Type.Platforms {
				e.gen.accountTypes[i].Type.Platforms[j].Usage = map[string]manifest.UsageRules{"anthropic.messages": {Semantics: "exclusive"}}
			}
		}
		res := e.messages(advisorBody(false))
		e.record()
		if res.status == 200 || len(e.up.keys()) != 0 {
			t.Fatalf("unmetered account accepted: %d", res.status)
		}
	})
	c := &call{ep: manifest.Endpoint{Protocol: "anthropic.messages"}, modelRefs: []modelReference{{kind: "advisor", path: "tools.0.model", model: "a"}, {kind: "advisor", path: "tools.1.model", model: "b"}}}
	account := &core.AccountRef{ModelMapping: map[string]string{"a": "same", "b": "same"}}
	if _, err := c.mapReferencedModels([]byte(`{"tools":[{"model":"a"},{"model":"b"}]}`), account, &typeRoute{upstream: "anthropic.messages"}); err == nil {
		t.Fatal("different prices collapsed to one observed identity")
	}
	if _, err := c.mapReferencedModels([]byte(`{}`), account, &typeRoute{upstream: "openai.chat"}); err == nil {
		t.Fatal("conversion silently dropped nested invocation")
	}
}
