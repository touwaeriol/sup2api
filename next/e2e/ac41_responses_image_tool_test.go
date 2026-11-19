package e2e

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// imageToolPrice is the expression price of the request model of AC41
// (USD per million tokens): image output priced apart from text output.
const imageToolPrice = `p*5 + cr*1.25 + img*8 + img_o*30 + c*10`

// The mock's Responses usage (deploy/mock-upstream): input 120 of which 50
// cached, output 42; with the image_generation tool also
// tool_usage.image_gen: input 39 (no image), output 515 (all image). The
// record is the sum (CONTRACTS §118.10): input 120+39 = 159, output
// 42+515 = 557, cache read 50, image input 0, image output 515.
//
// At imageToolPrice, inclusive semantics (billing/expr Normalize):
// img = 0, img_o = 515, c = 557-515 = 42, cr = 50, p = 159-50-0 = 109:
//
//	109*5 + 50*1.25 + 0*8 + 515*30 + 42*10
//	  = 545 + 62.5 + 15450 + 420 = 16477.5 -> 0.0164775 USD
//
// Without the tool: p = 120-50 = 70, cr = 50, c = 42:
//
//	70*5 + 50*1.25 + 42*10 = 350 + 62.5 + 420 = 832.5 -> 0.0008325 USD
//
// Both times the group rate imageRate 1.5: 0.02471625 and 0.00124875 USD.
var (
	imageToolCost   = big.NewRat(164775, 10_000_000)
	imageNoToolCost = big.NewRat(8325, 10_000_000)
)

const (
	imageToolInput       = MockInputTokens + 39
	imageToolOutput      = MockOutputTokens + 515
	imageToolImageOutput = 515
)

// AC 41: a Responses request using the image_generation tool (CONTRACTS
// §17.2, §118.10) is one request at the client's model: one usage record
// whose tokens are the conversation's plus the tool's (the image tokens in
// image input / output), one debit, priced at the requested model's
// expression price times the group rate; billing_detail has no item for an
// image model (no additional or replacement item, no primary_* figures),
// and neither the answer's tools echo (gpt-image-2-codex) nor a model the
// request's tool names needs a price. Over an OpenAI API key (JSON,
// stream, a WebSocket turn) and a codex account (a client JSON request the
// gateway streams and gathers back, the image in it). A request without
// the tool is the conversation alone.
func TestAC41_ResponsesImageTool(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	model := fmt.Sprintf("gpt-e2e-%s-imagetool", e.RunID)
	e.ensureExpressionPrice(admin, model, imageToolPrice)
	price, ok := Find(admin.ListAll(t, "/prices", "q", model), "model", model)
	if !ok {
		t.Fatalf("no price for %s", model)
	}
	priceID := price.Get("id").Int()
	rated := func(c *big.Rat) *big.Rat { return new(big.Rat).Mul(c, new(big.Rat).SetFloat64(imageRate)) }
	withTool, noTool := rated(imageToolCost), rated(imageNoToolCost)
	tool := func(m string) map[string]any {
		t := map[string]any{"type": "image_generation"}
		if m != "" {
			t["model"] = m
		}
		return t
	}
	// A tool model that has no price: it plays no part in billing.
	toolModel := fmt.Sprintf("gpt-image-e2e-%s-toolmodel", e.RunID)

	openai, _ := e.imageTenant(admin, "tool-openai", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType})
	codex, _ := e.imageTenant(admin, "tool-codex", AccountSpec{PluginKey: "codex", Type: "codex_oauth"})

	for _, c := range []struct {
		name   string
		tn     *Tenant
		stream bool
		tool   map[string]any // nil = no tool
	}{
		{name: "openai json", tn: openai, tool: tool("")},
		{name: "openai stream", tn: openai, stream: true, tool: tool("")},
		{name: "openai json, the tool names an unpriced model", tn: openai, tool: tool(toolModel)},
		{name: "codex json gathered from the stream", tn: codex, tool: tool("")},
		{name: "openai json without the tool", tn: openai},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := e.With(t)
			body := map[string]any{"model": model, "input": "draw a cat " + e.Name("t"), "stream": c.stream}
			if c.tool != nil {
				body["tools"] = []any{c.tool}
			}
			g := e.OpenAI(c.tn.APIKey, "/v1/responses", body)
			if g.Status != 200 {
				t.Fatalf("HTTP %d %s", g.Status, g.Body)
			}
			if c.tool != nil && !c.stream && !g.JSON().Get(`output.#(type=="image_generation_call").result`).Exists() {
				t.Fatalf("no image in the answer: %s", g.Body)
			}
			u := e.UsageByRequest(admin, c.tn.User.UserID, g.RequestID)
			want := noTool
			if c.tool != nil {
				want = withTool
			}
			expectImageToolUsage(t, u, c.tn.Accounts[0].ID, model, priceID, c.tool != nil, want)
			n := 0
			for _, r := range admin.ListAll(t, "/usage", "user_id", fmt.Sprint(c.tn.User.UserID)) {
				if r.Get("request_id").String() == g.RequestID {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("usage records of %s = %d, want 1", g.RequestID, n)
			}
			e.expectOneDebit(admin, c.tn.User.UserID, g.RequestID, want)
		})
	}

	t.Run("openai websocket turn", func(t *testing.T) {
		e := e.With(t)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		ws, _ := e.imageTenant(admin, "tool-ws", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType})
		conn := gatewayWS(ctx, t, e.BaseURL, ws.APIKey)
		defer conn.CloseNow()
		events := wsTurn(ctx, t, conn, map[string]any{"type": "response.create", "model": model, "input": "draw " + e.RunID,
			"tools": []any{tool("")}})
		if last := events[len(events)-1]; last.Get("type").String() != "response.completed" || last.Get("response.tool_usage.image_gen.output_tokens").Int() != imageToolImageOutput {
			t.Fatalf("websocket turn: %v", events)
		}
		conn.Close(websocket.StatusNormalClosure, "")
		var recs []gjson.Result
		Eventually(t, 30*time.Second, time.Second, "the websocket turn's usage record", func() bool {
			recs = recs[:0]
			for _, r := range admin.ListAll(t, "/usage", "user_id", fmt.Sprint(ws.User.UserID)) {
				if r.Get("billing_status").String() == "billed" {
					recs = append(recs, r)
				}
			}
			return len(recs) > 0
		})
		if len(recs) != 1 {
			t.Fatalf("websocket turn: %d billed records, want 1", len(recs))
		}
		rec := admin.OK(t, http.MethodGet, fmt.Sprintf("/usage/%d", recs[0].Get("id").Int()), nil)
		expectImageToolUsage(t, rec, ws.Accounts[0].ID, model, priceID, true, withTool)
	})
}

// expectImageToolUsage checks a billed Responses record of model at price
// priceID: its tokens (the conversation's, plus the image tool's when
// tool), no billing item apart from the request's own usage, and the cost
// want.
func expectImageToolUsage(t testing.TB, u gjson.Result, accountID int64, model string, priceID int64, tool bool, want *big.Rat) {
	t.Helper()
	input, output, imageOutput := int64(MockInputTokens), int64(MockOutputTokens), int64(0)
	if tool {
		input, output, imageOutput = imageToolInput, imageToolOutput, imageToolImageOutput
	}
	if u.Get("account_id").Int() != accountID || u.Get("billing_status").String() != "billed" ||
		u.Get("model").String() != model || u.Get("price_id").Int() != priceID ||
		u.Get("input_tokens").Int() != input || u.Get("output_tokens").Int() != output || u.Get("cache_read_tokens").Int() != MockCacheReadTokens ||
		u.Get("image_input_tokens").Int() != 0 || u.Get("image_output_tokens").Int() != imageOutput {
		t.Fatalf("usage: %s", u.Raw)
	}
	d := u.Get("billing_detail")
	for _, k := range []string{"additional", "replacement", "primary_cost", "primary_list_cost", "primary_total_cost"} {
		if d.Get(k).Exists() {
			t.Fatalf("billing_detail has %s: %s", k, d.Raw)
		}
	}
	AssertMoney(t, "total_cost", u.Get("total_cost").String(), want)
}
