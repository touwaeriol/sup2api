package e2e

import (
	"bytes"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// imagePrice prices the image tokens apart (CONTRACTS §118.6), USD per
// million tokens.
const imagePrice = `p*5 + cr*1.25 + img*8 + img_o*32 + c*10`

// imageRate is the group rate of the image tenants: the cost is multiplied.
const imageRate = 1.5

// The mock's image usage (deploy/mock-upstream sites.go) and its cost at
// imagePrice, from the core's Normalize (billing/expr, inclusive
// semantics): img = image input, img_o = image output; p = input - cache
// read - img, c = output - img_o (never below 0); cr = 0.
//
//	JSON:   input 50 (image 40), output 4200 (image 4000)
//	        p = 50-0-40 = 10, c = 4200-4000 = 200
//	        10*5 + 0*1.25 + 40*8 + 4000*32 + 200*10 = 130370 -> 0.130370 USD
//	stream: completed event: input 50 (image 40), output 4000, the output
//	        all image (sdk/platforms/openai.json maps output_tokens to it)
//	        p = 10, c = 4000-4000 = 0
//	        10*5 + 40*8 + 4000*32 = 128370 -> 0.128370 USD
//
// billing/image_tokens_test.go and api_test.go check the same expression
// on other token counts (300/600/4000/200 there: 0.1363).
var (
	imageJSONCost   = big.NewRat(130370, 1_000_000)
	imageStreamCost = big.NewRat(128370, 1_000_000)
)

// AC 40: OpenAI image generation (CONTRACTS §118). An OpenAI API key, a
// New API and a Sub2API account each serve POST /v1/images/generations
// (JSON and stream) and /v1/images/edits (JSON): the usage record has the
// image input and output tokens, and the expression price bills them apart,
// times the group rate. A multipart edits request is a 400 in the gateway
// with a free invalid_request record and no account; volcengine and Azure
// deployment accounts do not serve the endpoint; a model without a price is refused
// with 403 model_price_not_configured before any upstream call (CONTRACTS
// §17.3: no price is always an error).
func TestAC40_ImageGeneration(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	model := fmt.Sprintf("gpt-image-e2e-%s", e.RunID)
	e.CreatePrice(admin, map[string]any{"model": model, "mode": "expression", "expression": imagePrice, "note": "e2e image price"})
	body := func(prompt string, stream bool) map[string]any {
		return map[string]any{"model": model, "prompt": prompt, "size": "1024x1024", "stream": stream}
	}

	for _, name := range []string{OpenAIPlugin, NewAPIPlugin, Sub2APIPlugin} {
		t.Run(name, func(t *testing.T) {
			e := e.With(t)
			tn, k := e.imageTenant(admin, name, AccountSpec{PluginKey: name, Type: APIKeyType})
			accountID := tn.Accounts[0].ID
			for _, c := range []struct {
				path         string
				stream       bool
				input        int64
				output       int64
				cost         *big.Rat
				lastEventPre string
			}{
				{path: "/v1/images/generations", input: 50, output: 4200, cost: imageJSONCost},
				{path: "/v1/images/generations", stream: true, input: 50, output: 4000, cost: imageStreamCost, lastEventPre: "image_generation"},
				{path: "/v1/images/edits", input: 50, output: 4200, cost: imageJSONCost},
			} {
				what := fmt.Sprintf("%s %s stream=%v", name, c.path, c.stream)
				mark := m.Mark(t)
				g := e.OpenAI(tn.APIKey, c.path, body("img-"+e.Name(name), c.stream))
				if g.Status != 200 {
					t.Fatalf("%s: HTTP %d %s", what, g.Status, g.Body)
				}
				if c.stream {
					if types := g.EventTypes(); len(types) == 0 || types[len(types)-1] != c.lastEventPre+".completed" {
						t.Fatalf("%s: events %v", what, types)
					}
				} else if g.JSON().Get("data.0.b64_json").String() == "" {
					t.Fatalf("%s: no image: %s", what, g.Body)
				}
				r := oneMockCall(t, m, mark, k, what)
				expectPath(t, r, "", c.path)
				if r.Header("authorization") != "Bearer "+k {
					t.Fatalf("%s: headers %s", what, r.Headers.Raw)
				}
				u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
				if u.Get("account_id").Int() != accountID || u.Get("billing_status").String() != "billed" || u.Get("stream").Bool() != c.stream ||
					u.Get("input_tokens").Int() != c.input || u.Get("output_tokens").Int() != c.output ||
					u.Get("image_input_tokens").Int() != 40 || u.Get("image_output_tokens").Int() != 4000 {
					t.Fatalf("%s: usage %s", what, u.Raw)
				}
				want := new(big.Rat).Mul(c.cost, new(big.Rat).SetFloat64(imageRate))
				AssertMoney(t, what+" total_cost", u.Get("total_cost").String(), want)
				e.expectOneDebit(admin, tn.User.UserID, g.RequestID, want)
			}
		})
	}

	t.Run("multipart edits", func(t *testing.T) {
		e := e.With(t)
		tn, k := e.imageTenant(admin, "multipart", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType})
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		_ = mw.WriteField("model", model)
		_ = mw.WriteField("prompt", "edit "+e.RunID)
		fw, _ := mw.CreateFormFile("image", "in.png")
		_, _ = fw.Write([]byte("\x89PNG mock"))
		_ = mw.Close()
		mark := m.Mark(t)
		g := e.Gateway(t, e.BaseURL, "/v1/images/edits", "", form.Bytes(),
			map[string]string{"Authorization": "Bearer " + tn.APIKey, "Content-Type": mw.FormDataContentType()})
		if g.Status != http.StatusBadRequest || g.JSON().Get("error.message").String() != "request body is not valid JSON" {
			t.Fatalf("multipart edits: HTTP %d %s", g.Status, g.Body)
		}
		u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		if u.Get("billing_status").String() != "free" || u.Get("error_type").String() != "invalid_request" || u.Get("account_id").Int() != 0 {
			t.Fatalf("multipart edits usage: %s", u.Raw)
		}
		AssertMoney(t, "multipart edits total_cost", u.Get("total_cost").String(), new(big.Rat))
		if calls := mockCallsOf(t, m, mark, k); len(calls) != 0 {
			t.Fatalf("multipart edits reached the mock: %+v", calls)
		}
	})

	// Types with an endpoint subset on openai that neither lists the image
	// endpoints nor adapts them (CONTRACTS §121.1) do not serve them: Ark,
	// and an Azure deployment (chat, embeddings, responses adapted). Codex
	// accounts serve them through the core's conversion (AC42).
	for name, spec := range map[string]AccountSpec{
		"volcengine":       {PluginKey: "volcengine", Type: "relay"},
		"azure_deployment": {PluginKey: AzurePlugin, Type: AzureDeploymentType, Settings: map[string]any{"api_version": azureAPIVersion}},
	} {
		t.Run(name+" not served", func(t *testing.T) {
			e := e.With(t)
			tn, k := e.imageTenant(admin, name, spec)
			mark := m.Mark(t)
			expectNotFound(t, e.OpenAI(tn.APIKey, "/v1/images/generations", body("not served", false)), "openai", endpointNotServedMessage)
			if calls := mockCallsOf(t, m, mark, k); len(calls) != 0 {
				t.Fatalf("%s: reached the mock: %+v", name, calls)
			}
		})
	}

	t.Run("no price", func(t *testing.T) {
		e := e.With(t)
		tn, k := e.imageTenant(admin, "unpriced", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType})
		mark := m.Mark(t)
		g := e.OpenAI(tn.APIKey, "/v1/images/generations", map[string]any{"model": model + "-unpriced", "prompt": "no price"})
		if g.Status != http.StatusForbidden || !strings.Contains(string(g.Body), "model_price_not_configured") {
			t.Fatalf("unpriced image model: HTTP %d %s", g.Status, g.Body)
		}
		if calls := mockCallsOf(t, m, mark, k); len(calls) != 0 {
			t.Fatalf("unpriced image model reached the mock: %+v", calls)
		}
	})
}

// imageTenant is a tenant (group rate imageRate, credit "20") with one
// account of spec on a new upstream key at the mock, its plugin enabled;
// it returns the tenant and the key.
func (e *Env) imageTenant(admin *Session, name string, spec AccountSpec) (*Tenant, string) {
	e.T.Helper()
	e.EnsurePlugin(admin, spec.PluginKey, "")
	k := fmt.Sprintf("sk-img-%s-%d", e.RunID, nextSeq())
	switch spec.PluginKey {
	case "codex":
		spec.Credentials = codexCreds(k)
	default:
		spec.APIKey = k
	}
	tn := e.newTenantShell(admin, e.Name("grp-img-"+name), imageRate, "20")
	tn.addAccount(e, admin, spec, k)
	return tn, k
}
