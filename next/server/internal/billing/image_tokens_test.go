package billing

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The money of an image generation (CONTRACTS §118) under the two ways an
// administrator can price it. The reference price of gpt-image-1.5 bills
// the image tokens at their own rates and the rest of the input and output
// at the text rates; a price with text rates only (what a models.dev sync
// imports) bills every token, the image ones included, at the text rates -
// less, never nothing, and no token twice.
func TestQuoteImageTokens(t *testing.T) {
	// 1000 input tokens of which 600 image and 100 cached text, 4200 output
	// tokens of which 4000 image (OpenAI reports both inclusively).
	tokens := core.UsageTokens{Input: 1000, Output: 4200, CacheRead: 100, ImageInput: 600, ImageOutput: 4000}
	for _, tc := range []struct {
		name, expression, want string
	}{
		// p = 1000-100-600 = 300, c = 4200-4000 = 200:
		// 300*5 + 100*1.25 + 600*8 + 4000*32 + 200*10 = 136425 micro-USD.
		{"image prices", `p*5 + cr*1.25 + img*8 + img_o*32 + c*10`, "0.136425"},
		// p = 1000-100 = 900, c = 4200: 900*5 + 100*1.25 + 4200*10 = 46625.
		{"text prices only", `p*5 + cr*1.25 + c*10`, "0.046625"},
		// A price without a text output rate writes c at the image output
		// rate: an upstream that reports no output breakdown is then right too.
		{"no output breakdown", `p*5 + cr*1.25 + img*8 + img_o*30 + c*30`, "0.132425"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&Service{}).Quote(context.Background(), core.QuoteInputs{
				Expression: tc.expression, UsageSemantics: "inclusive", Tokens: tokens, RateMultiplier: decimal.NewFromInt(1),
			})
			if err != nil || !got.Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("cost = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
	// A stream's completed event has no output breakdown: the whole output
	// is the image output (sdk/platforms/openai.json), billed once at img_o.
	got, err := (&Service{}).Quote(context.Background(), core.QuoteInputs{
		Expression: `p*5 + img*8 + img_o*30 + c*30`, UsageSemantics: "inclusive", RateMultiplier: decimal.NewFromInt(1),
		Tokens: core.UsageTokens{Input: 50, Output: 4000, ImageOutput: 4000},
	})
	if err != nil || !got.Equal(decimal.RequireFromString("0.12025")) {
		t.Fatalf("stream cost = %s, %v", got, err)
	}
}
