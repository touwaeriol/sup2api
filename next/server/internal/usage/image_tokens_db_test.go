package usage

import (
	"context"
	"strconv"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// An image generation's record (CONTRACTS §118): the two image columns are
// written, the settlement prices them at img / img_o and the rest of the
// input and output at p / c, the usage.recorded event carries them, and the
// list and detail of /usage and /me/usage return them.
func TestImageTokensSettledAndShown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const src = `p*5 + cr*1.25 + img*8 + img_o*32 + c*10`
	prog, err := expr.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.record("req-image", true)
	rec.Platform, rec.Protocol, rec.UpstreamProtocol, rec.Endpoint = "openai", "openai.images", "openai.images", "/v1/images/generations"
	rec.Model, rec.UsageSemantics, rec.PriceHeaders = "gpt-image-1.5", "inclusive", nil
	rec.Tokens = core.UsageTokens{Input: 1000, Output: 4200, CacheRead: 100, ImageInput: 600, ImageOutput: 4000}
	rec.Price = &core.PriceRule{Model: "gpt-image-1.5", Mode: "expression", Expression: src, ExprVersion: 1, ExprHash: prog.Hash()}
	f.svc.process(ctx, []*core.UsageRecord{rec})

	// 300*5 + 100*1.25 + 600*8 + 4000*32 + 200*10 = 136425 micro-USD.
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || image_input_tokens || ' ' || image_output_tokens
		FROM usage_logs WHERE request_id = 'req-image'`); s != "billed 0.13642500 600 4000" {
		t.Fatalf("row: %s", s)
	}
	if s := f.scalar(`SELECT (payload->>'image_input_tokens') || ' ' || (payload->>'image_output_tokens') FROM events
		WHERE type = 'usage.recorded' AND payload->>'request_id' = 'req-image'`); s != "600 4000" {
		t.Fatalf("event: %s", s)
	}
	for _, path := range []string{"/usage", "/me/usage"} {
		item := f.get(f.user, path, 200)["data"].([]any)[0].(map[string]any)
		if item["image_input_tokens"] != float64(600) || item["image_output_tokens"] != float64(4000) || item["input_tokens"] != float64(1000) {
			t.Fatalf("%s: %v", path, item)
		}
		detail := f.get(f.user, path+"/"+strconv.FormatInt(int64(item["id"].(float64)), 10), 200)["data"].(map[string]any)
		if detail["image_input_tokens"] != float64(600) || detail["image_output_tokens"] != float64(4000) {
			t.Fatalf("%s detail: %v", path, detail)
		}
	}

	// Another request's row holds 0 in both, and its billing detail keeps
	// its bytes: no image key in the stored tokens.
	plain := f.record("req-plain", true)
	plain.Additional = []core.PricedUsage{{Kind: "advisor", Model: "extra", Price: f.price, UsageSemantics: "exclusive",
		Tokens: core.UsageTokens{Input: 10}, RateMultiplier: plain.RateMultiplier}}
	f.svc.process(ctx, []*core.UsageRecord{plain})
	if s := f.scalar(`SELECT image_input_tokens || ' ' || image_output_tokens || ' ' || (billing_detail::text LIKE '%Image%')
		FROM usage_logs WHERE request_id = 'req-plain'`); s != "0 0 false" {
		t.Fatalf("plain row: %s", s)
	}
}
