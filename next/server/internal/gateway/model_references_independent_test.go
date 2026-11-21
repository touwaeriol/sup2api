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
	t.Run("account that cannot meter the extra still serves at the requested model", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		meterNothing(t, e, "anthropic", "anthropic.messages")
		res := e.messages(advisorBody(false))
		rec := e.record()
		if res.status != 200 || len(e.up.keys()) != 1 || !rec.Billable || len(rec.Additional) != 0 || rec.Price == nil || rec.Price.ID != 9 {
			t.Fatalf("unmetered account: %d %+v", res.status, rec)
		}
	})
	// Two references the mapping makes one upstream model cannot be told
	// apart in the response: neither is priced by that identity, however
	// many times they recur, and the attempt still goes upstream.
	c := &call{ep: manifest.Endpoint{Protocol: "anthropic.messages"}, modelRefs: []modelReference{
		{kind: "advisor", path: "tools.0.model", model: "a"}, {kind: "advisor", path: "tools.1.model", model: "b"}, {kind: "advisor", path: "tools.2.model", model: "a"}}}
	account := &core.AccountRef{ModelMapping: map[string]string{"a": "same", "b": "same"}}
	out, err := c.mapReferencedModels([]byte(`{"tools":[{"model":"a"},{"model":"b"},{"model":"a"}]}`), account, &typeRoute{upstream: "anthropic.messages"})
	if _, priced := c.upstreamRefs["advisor\x00same"]; err != nil || priced || string(out) != `{"tools":[{"model":"same"},{"model":"same"},{"model":"same"}]}` {
		t.Fatalf("ambiguous references: %s %v priced=%v", out, err, priced)
	}
	// A converted request does not carry them: nothing is mapped or priced.
	if out, err := c.mapReferencedModels([]byte(`{}`), account, &typeRoute{upstream: "openai.chat"}); err != nil || string(out) != `{}` || c.upstreamRefs != nil {
		t.Fatalf("converted request: %s %v %v", out, err, c.upstreamRefs)
	}
}
