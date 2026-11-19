package e2e

import (
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// codexImageChatModel is the chat model the codex account types name in
// their adapted image endpoints (plugins/codex manifest, CONTRACTS §121.3):
// the top-level model of the Responses request that drives the tool.
const codexImageChatModel = "gpt-6-luna"

// codexImageEchoModel is the image model the Codex backend's tools echo
// names (the mock's imageToolEchoModel): the record's upstream model, the
// upstream's answer as it is (owner, 2026-11-19).
const codexImageEchoModel = "gpt-image-2-codex"

// The Codex backend mock answers a request with the image_generation tool
// with the usual Responses usage (input 120 of which 50 cached, output 42)
// and tool_usage.image_gen (input 39, no image; output 515, all image). The
// record sums them (CONTRACTS §121.5): input 159, output 557, cache read
// 50, image output 515, at the client's model's price imageToolPrice, so
// the cost is AC41's imageToolCost:
//
//	p = 159-50-0 = 109, cr = 50, img = 0, img_o = 515, c = 557-515 = 42
//	109*5 + 50*1.25 + 0*8 + 515*30 + 42*10 = 16477.5 -> 0.0164775 USD
//	times the group rate 1.5 = 0.02471625 USD
//
// Without an image (the mock's MOCK-NO-IMAGE marker: a completed answer
// with only a message) there is no
// tool_usage: input 120, output 42, AC41's imageNoToolCost:
//
//	70*5 + 50*1.25 + 42*10 = 832.5 -> 0.0008325 USD, times 1.5 = 0.00124875 USD
//
// The client's usage is the image part alone (tool_usage.image_gen).

// AC 42: a codex account serves the OpenAI image endpoints through the
// core's conversion (CONTRACTS §121). Generations (JSON and stream) and
// edits (JSON) reach the Codex backend, through the account's proxy, as a
// streamed Responses request: top-level model gpt-6-luna, the forced
// image_generation tool naming the client's model (and action edit with
// the input image for edits). The client gets the Images API shape; the
// one usage record has protocol openai.images / openai.images_edit,
// upstream protocol openai.responses, the client's model, upstream model
// the backend's echo gpt-image-2-codex, the two parts' tokens summed, the
// client's model's price times the group rate and one debit. The upstream's
// answer is taken as it is: a completed answer without an image is a 200
// with an empty data array and no usage (the mock reports no
// tool_usage then; a stream ends without a completed or error event), a
// successful record without an error type, charged once, and the account
// not failed (active, schedulable, no cooldown).
// n: 2 is a 400 that does not reach the backend; an unpriced model is a
// 403 before any upstream call.
func TestAC42_CodexImages(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	model := fmt.Sprintf("gpt-image-e2e-%s-codex", e.RunID)
	e.ensureExpressionPrice(admin, model, imageToolPrice)
	rated := func(c *big.Rat) *big.Rat { return new(big.Rat).Mul(c, new(big.Rat).SetFloat64(imageRate)) }
	tn, k := e.imageTenant(admin, "codex-images", AccountSpec{PluginKey: "codex", Type: "codex_oauth",
		Extra: map[string]any{"proxy_url": e.MockInternalURL}})
	accountID := tn.Accounts[0].ID
	editImage := []any{map[string]any{"image_url": "data:image/png;base64," + "iVBORw0KGgo="}}
	body := func(prompt string, stream bool, edit bool) map[string]any {
		b := map[string]any{"model": model, "prompt": prompt + " " + e.Name("p"), "stream": stream}
		if edit {
			b["images"] = editImage
		}
		return b
	}

	// upstream checks the one Responses request the Codex backend got.
	upstream := func(t *testing.T, mark int64, what, action string) {
		t.Helper()
		r := oneMockCall(t, m, mark, k, what)
		j := gjson.Parse(r.Body)
		if r.Host != "chatgpt.com" || r.Path != "/backend-api/codex/responses" || !r.Proxied ||
			j.Get("model").String() != codexImageChatModel || !j.Get("stream").Bool() ||
			j.Get("tools.0.type").String() != "image_generation" || j.Get("tools.0.model").String() != model ||
			j.Get("tools.0.action").String() != action || j.Get("tool_choice.type").String() != "image_generation" {
			t.Fatalf("%s: upstream %s %s proxied=%v body %s", what, r.Host, r.Path, r.Proxied, r.Body)
		}
		if action == "edit" && j.Get("input.0.content.1.type").String() != "input_image" {
			t.Fatalf("%s: no input image upstream: %s", what, r.Body)
		}
	}
	// record checks the usage record and its debit.
	record := func(t *testing.T, g *GatewayResult, protocol string, image bool, want *big.Rat) {
		t.Helper()
		u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		input, output, imageOutput := int64(MockInputTokens), int64(MockOutputTokens), int64(0)
		if image {
			input, output, imageOutput = imageToolInput, imageToolOutput, imageToolImageOutput
		}
		if u.Get("account_id").Int() != accountID || u.Get("billing_status").String() != "billed" || !u.Get("success").Bool() || u.Get("status_code").Int() != 200 || u.Get("error_type").String() != "" ||
			u.Get("protocol").String() != protocol || u.Get("upstream_protocol").String() != "openai.responses" ||
			u.Get("model").String() != model || u.Get("upstream_model").String() != codexImageEchoModel ||
			u.Get("input_tokens").Int() != input || u.Get("output_tokens").Int() != output || u.Get("cache_read_tokens").Int() != MockCacheReadTokens ||
			u.Get("image_input_tokens").Int() != 0 || u.Get("image_output_tokens").Int() != imageOutput ||
			u.Get("billing_detail.additional").Exists() {
			t.Fatalf("usage: %s", u.Raw)
		}
		AssertMoney(t, "total_cost", u.Get("total_cost").String(), want)
		e.expectOneDebit(admin, tn.User.UserID, g.RequestID, want)
	}

	for _, c := range []struct {
		name, path, protocol, action, completed string
		stream                                  bool
	}{
		{name: "generations json", path: "/v1/images/generations", protocol: "openai.images", action: "generate"},
		{name: "generations stream", path: "/v1/images/generations", protocol: "openai.images", action: "generate", stream: true, completed: "image_generation.completed"},
		{name: "edits json", path: "/v1/images/edits", protocol: "openai.images_edit", action: "edit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := e.With(t)
			mark := m.Mark(t)
			g := e.OpenAI(tn.APIKey, c.path, body("a small red dot", c.stream, c.action == "edit"))
			if g.Status != 200 {
				t.Fatalf("HTTP %d %s", g.Status, g.Body)
			}
			var img gjson.Result
			if c.stream {
				types := g.EventTypes()
				if len(types) == 0 || types[len(types)-1] != c.completed {
					t.Fatalf("events %v", types)
				}
				img = g.Events[len(g.Events)-1].Data
			} else {
				img = g.JSON()
				if !img.Get("created").Exists() {
					t.Fatalf("no created: %s", g.Body)
				}
				img = gjson.Parse(fmt.Sprintf(`{"b64_json":%s,"usage":%s}`, img.Get("data.0.b64_json").Raw, img.Get("usage").Raw))
			}
			if img.Get("b64_json").String() != "bW9jaw==" || img.Get("usage.input_tokens").Int() != 39 || img.Get("usage.output_tokens").Int() != imageToolImageOutput {
				t.Fatalf("image answer: %s %v", g.Body, g.Events)
			}
			upstream(t, mark, c.name, c.action)
			record(t, g, c.protocol, true, rated(imageToolCost))
		})
	}

	t.Run("no image", func(t *testing.T) {
		for _, stream := range []bool{false, true} {
			e := e.With(t)
			mark := m.Mark(t)
			g := e.OpenAI(tn.APIKey, "/v1/images/generations", body("MOCK-NO-IMAGE", stream, false))
			what := fmt.Sprintf("no image stream=%v", stream)
			if g.Status != 200 {
				t.Fatalf("%s: HTTP %d %s", what, g.Status, g.Body)
			}
			if stream {
				for _, ev := range g.Events {
					if strings.HasSuffix(ev.Event, ".completed") || ev.Event == "error" {
						t.Fatalf("%s: events %v", what, g.EventTypes())
					}
				}
			} else if j := g.JSON(); !j.Get("data").IsArray() || len(j.Get("data").Array()) != 0 || !j.Get("created").Exists() || j.Get("usage").Exists() {
				t.Fatalf("%s: %s", what, g.Body)
			}
			upstream(t, mark, what, "generate")
			record(t, g, "openai.images", false, rated(imageNoToolCost))
			if a := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", accountID), nil); a.Get("status").String() != "active" || !a.Get("schedulable").Bool() || a.Get("cooldown_until").String() != "" {
				t.Fatalf("%s: account %s", what, a.Raw)
			}
		}
	})

	t.Run("n 2", func(t *testing.T) {
		e := e.With(t)
		mark := m.Mark(t)
		b := body("two images", false, false)
		b["n"] = 2
		g := e.OpenAI(tn.APIKey, "/v1/images/generations", b)
		if g.Status != http.StatusBadRequest || !strings.Contains(string(g.Body), "only one image per request") {
			t.Fatalf("n 2: HTTP %d %s", g.Status, g.Body)
		}
		if calls := mockCallsOf(t, m, mark, k); len(calls) != 0 {
			t.Fatalf("n 2 reached the backend: %+v", calls)
		}
	})

	t.Run("no price", func(t *testing.T) {
		e := e.With(t)
		mark := m.Mark(t)
		g := e.OpenAI(tn.APIKey, "/v1/images/generations", map[string]any{"model": model + "-unpriced", "prompt": "no price"})
		if g.Status != http.StatusForbidden || !strings.Contains(string(g.Body), "model_price_not_configured") {
			t.Fatalf("unpriced: HTTP %d %s", g.Status, g.Body)
		}
		if calls := mockCallsOf(t, m, mark, k); len(calls) != 0 {
			t.Fatalf("unpriced reached the backend: %+v", calls)
		}
	})
}
