package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
)

// codexImageUpstream is a stream-only Responses upstream generating images
// with the image_generation tool the way the Codex backend does
// (DESIGN-2026-11-18-CODEX-IMAGES 1.2): the image and a closing message
// only in output_item.done, response.completed with an empty output, the
// tool's usage in tool_usage.image_gen and the image model echoed in tools.
type codexImageUpstream struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	// textOnly answers with a message and no image; noEcho leaves the
	// image model out of the response's tools, echo replaces it.
	textOnly, noEcho bool
	echo             string
}

// The tokens the upstream reports: the chat part and the image tool's.
const (
	itChatIn, itChatOut   = 2235, 69
	itImageIn, itImageOut = 56, 343
)

func newCodexImageUpstream(t *testing.T) *codexImageUpstream {
	u := &codexImageUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *codexImageUpstream) last() gjson.Result {
	u.mu.Lock()
	defer u.mu.Unlock()
	return gjson.ParseBytes(u.bodies[len(u.bodies)-1])
}

func (u *codexImageUpstream) calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

func (u *codexImageUpstream) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.bodies = append(u.bodies, raw)
	textOnly, noEcho, echo := u.textOnly, u.noEcho, u.echo
	u.mu.Unlock()
	if !gjson.GetBytes(raw, "stream").Bool() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"message":"Stream must be set to true","type":"invalid_request_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v map[string]any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v["type"], b)
	}
	resp := func(status string) map[string]any {
		tool := map[string]any{"type": "image_generation", "model": "gpt-image-2-codex", "size": "auto"}
		if echo != "" {
			tool["model"] = echo
		}
		if noEcho {
			delete(tool, "model")
		}
		return map[string]any{"id": "resp_1", "object": "response", "created_at": 1763452800, "status": status, "model": "gpt-6-luna",
			"output": []any{}, "tools": []any{tool}}
	}
	send(map[string]any{"type": "response.created", "response": resp("in_progress")})
	n := 0
	if !textOnly {
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"id": "ig_1", "type": "image_generation_call",
			"status": "completed", "background": "opaque", "output_format": "png", "quality": "low", "size": "1024x1536", "result": "aW1hZ2U="}})
		n++
	}
	send(map[string]any{"type": "response.output_item.done", "output_index": n, "item": soMessage("msg_1", map[bool]string{true: "no image for you"}[textOnly])})
	done := resp("completed")
	done["usage"] = map[string]any{"input_tokens": itChatIn, "output_tokens": itChatOut, "input_tokens_details": map[string]any{"cached_tokens": 0}}
	if !textOnly {
		done["tool_usage"] = map[string]any{"image_gen": map[string]any{"input_tokens": itImageIn, "output_tokens": itImageOut,
			"input_tokens_details":  map[string]any{"image_tokens": 0, "text_tokens": itImageIn},
			"output_tokens_details": map[string]any{"image_tokens": itImageOut, "text_tokens": 0}, "total_tokens": itImageIn + itImageOut}}
	}
	send(map[string]any{"type": "response.completed", "response": done})
}

// codexImagesEnv has account 10, of a stream-only type serving the openai
// responses endpoint that declares the image endpoints adapted with the
// chat model gpt-6-luna, as the codex plugin does; gpt-image-2 is priced,
// the chat model is not.
func codexImagesEnv(t *testing.T) (*env, *fakePlatform, *codexImageUpstream) {
	t.Helper()
	up := newCodexImageUpstream(t)
	e := newEnv(t, func(e *env) { e.conv = convert.Default() })
	plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return up.srv.URL + "/v1/responses" }}
	adapted := manifest.AdaptOptions{Model: "gpt-6-luna"}
	e.addAccountType("codexlike", "chatgpt", plat, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI,
		Endpoints: []string{"responses"}, StreamOnly: []string{"openai.responses"},
		Adapted: map[string]manifest.AdaptOptions{"chat_completions": {}, "images_generations": adapted, "images_edits": adapted}})
	e.accounts.addTyped(testGroup, 10, 100, "codex-10", "codexlike", "chatgpt")
	e.accounts.groups[testGroup] = []int64{10}
	e.pricer.rules["gpt-image-2"] = &core.PriceRule{ID: 21, Model: "gpt-image-2", Mode: "expression", Expression: "p*5 + img_o*30 + c*30", ExprHash: "img"}
	return e, plat, up
}

func imageRequest(edit, stream bool) (string, map[string]any) {
	b := map[string]any{"model": "gpt-image-2", "prompt": "a small white dot", "size": "1024x1536", "stream": stream}
	if !edit {
		return "/v1/images/generations", b
	}
	b["images"] = []any{map[string]any{"image_url": "data:image/png;base64,iVBORw0KGgo="}}
	return "/v1/images/edits", b
}

// A stream-only type declaring the image endpoints adapted serves image
// generations and edits, JSON and streamed, through the image tool
// (CONTRACTS §121): the upstream gets the chat model at the top and the
// client's model in the tool; the client gets the Images API's shape; the
// record is the image request, priced by the client's model, with the chat
// and image tokens added (the Responses endpoint's own rules), and the chat
// model the upstream ran as the upstream model.
func TestCodexImages(t *testing.T) {
	for _, edit := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			e, plat, up := codexImagesEnv(t)
			path, body := imageRequest(edit, stream)
			r := e.post(path, body, bearer())
			if r.status != 200 {
				t.Fatalf("%s stream=%v: %d %s", path, stream, r.status, r.body)
			}
			protocol, event := "openai.images", "image_generation.completed"
			if edit {
				protocol, event = "openai.images_edit", "image_edit.completed"
			}
			if stream {
				if !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(r.body), "event: "+event) ||
					!strings.Contains(string(r.body), `"b64_json":"aW1hZ2U="`) || strings.Contains(string(r.body), "response.") {
					t.Fatalf("%s stream: %s", path, r.body)
				}
			} else {
				j := r.json()
				if j.Get("data.0.b64_json").String() != "aW1hZ2U=" || j.Get("size").String() != "1024x1536" ||
					j.Get("usage.output_tokens").Int() != itImageOut || j.Get("output").Exists() {
					t.Fatalf("%s: %s", path, r.body)
				}
			}
			ub := up.last()
			if ub.Get("model").String() != "gpt-6-luna" || ub.Get("tools.0.type").String() != "image_generation" ||
				ub.Get("tools.0.model").String() != "gpt-image-2" || ub.Get("tool_choice.type").String() != "image_generation" ||
				ub.Get("stream").Type != gjson.True || !strings.Contains(ub.Get("instructions").String(), "1024x1536") {
				t.Fatalf("%s upstream body %s", path, ub.Raw)
			}
			if b := plat.builds[len(plat.builds)-1]; b.GetMeta().GetProtocol() != "openai.responses" || b.GetMeta().GetClientProtocol() != protocol {
				t.Fatalf("%s meta %+v", path, b.GetMeta())
			}
			rec := e.record()
			if !rec.Success || rec.Protocol != protocol || rec.UpstreamProtocol != "openai.responses" || rec.Model != "gpt-image-2" ||
				rec.UpstreamModel != "gpt-image-2-codex" || rec.Price == nil || rec.Price.ID != 21 || !rec.Billable || rec.Stream != stream ||
				rec.Tokens != (core.UsageTokens{Input: itChatIn + itImageIn, Output: itChatOut + itImageOut, ImageOutput: itImageOut}) ||
				rec.Conversion == nil || strings.Join(rec.Conversion.Hops, ",") != protocol+",openai.responses" {
				t.Fatalf("%s stream=%v record %+v conversion %+v", path, stream, rec, rec.Conversion)
			}
		}
	}
}

// The account's model mapping goes into the tool, where the client's model
// is; the chat model at the top stays. The upstream model recorded is the
// one the response echoes, over the mapping; without an echo it is what the
// gateway had: the mapped name, or none when the model is not mapped.
func TestCodexImagesModelMapping(t *testing.T) {
	for _, tc := range []struct {
		mapped, noEcho bool
		echo, want     string
	}{
		{true, false, "", "gpt-image-2-codex"},
		{true, false, "gpt-image-2", "gpt-image-2"}, // the echo is the client's model: still the echo
		{true, true, "", "gpt-image-2-alias"},
		{false, true, "", ""},
	} {
		e, _, up := codexImagesEnv(t)
		up.noEcho, up.echo = tc.noEcho, tc.echo
		if tc.mapped {
			e.accounts.accounts[10].ModelMapping = map[string]string{"gpt-image-2": "gpt-image-2-alias"}
		}
		path, body := imageRequest(false, false)
		if r := e.post(path, body, bearer()); r.status != 200 {
			t.Fatalf("%+v: %d %s", tc, r.status, r.body)
		}
		if ub := up.last(); ub.Get("model").String() != "gpt-6-luna" || tc.mapped && ub.Get("tools.0.model").String() != "gpt-image-2-alias" {
			t.Fatalf("%+v: upstream body %s", tc, ub.Raw)
		}
		if rec := e.record(); rec.Model != "gpt-image-2" || rec.UpstreamModel != tc.want || rec.Price.ID != 21 {
			t.Fatalf("%+v: record %+v", tc, rec)
		}
	}
}

// The echoed image model is only recorded: a price it has is never used,
// for the pre-charge or the charge (CONTRACTS §17.2).
func TestCodexImagesEchoNotPriced(t *testing.T) {
	e, _, _ := codexImagesEnv(t)
	e.pricer.rules["gpt-image-2-codex"] = &core.PriceRule{ID: 99, Model: "gpt-image-2-codex", Mode: "visual", Expression: "p*1000", ExprHash: "echo"}
	e.pricer.rules["gpt-6-luna"] = &core.PriceRule{ID: 98, Model: "gpt-6-luna", Mode: "visual", Expression: "p*1000", ExprHash: "chat"}
	path, body := imageRequest(false, true)
	if r := e.post(path, body, bearer()); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	rec := e.record()
	if rec.UpstreamModel != "gpt-image-2-codex" || rec.Price == nil || rec.Price.ID != 21 ||
		len(e.balance.precharged) != 1 || e.balance.precharged[0] == nil || e.balance.precharged[0].ID != 21 {
		t.Fatalf("record %+v price %+v pre-charged %+v", rec, rec.Price, e.balance.precharged)
	}
}

// An upstream that answered with text and no image succeeded: the client
// gets the Images API's answer without images (an empty data, a stream
// without completed events), the record is a success and the usage billed
// (the owner: what the upstream returns is what it is).
func TestCodexImagesNoImage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		e, _, up := codexImagesEnv(t)
		up.textOnly = true
		path, body := imageRequest(false, stream)
		r := e.post(path, body, bearer())
		rec := e.record()
		if stream {
			if r.status != 200 || strings.Contains(string(r.body), "completed") || strings.Contains(string(r.body), "error") {
				t.Fatalf("stream: %d %s", r.status, r.body)
			}
		} else if d := r.json().Get("data"); r.status != 200 || !d.IsArray() || len(d.Array()) != 0 || r.json().Get("error").Exists() {
			t.Fatalf("json: %d %s", r.status, r.body)
		}
		if !rec.Success || rec.StatusCode != 200 || rec.ErrorType != "" || up.calls() != 1 || !rec.Billable ||
			rec.Tokens != (core.UsageTokens{Input: itChatIn, Output: itChatOut}) {
			t.Fatalf("stream=%v record %+v after %d calls", stream, rec, up.calls())
		}
	}
}

// An adapted image endpoint without the chat model cannot be converted:
// the type does not serve the request (a configuration error, logged), and
// nothing goes upstream.
func TestCodexImagesWithoutAdaptModel(t *testing.T) {
	e, _, up := codexImagesEnv(t)
	b := &e.gen.accountTypes[len(e.gen.accountTypes)-1]
	b.Type.Platforms[0].Adapted = map[string]manifest.AdaptOptions{"images_generations": {}}
	path, body := imageRequest(false, false)
	if r := e.post(path, body, bearer()); r.status != 400 || r.json().Get("error.code").String() != "unsupported_conversion" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if up.calls() != 0 {
		t.Fatal("the request went upstream")
	}
	e.record()
}

// modelPaths is a converter locating the response model.
type modelPaths struct{}

func (modelPaths) ResponseModelPaths() (json, sse string) {
	return "tools.0.model", "response.tools.0.model"
}

// withModelPaths reads the model at the converter's paths on a copy, also
// from rules whose maps are absent, and leaves the rules it was given alone.
func TestWithModelPaths(t *testing.T) {
	rules := manifest.UsageRules{Semantics: "inclusive", JSON: &manifest.UsageMap{},
		SSE: []manifest.SSEUsageMap{{Event: "response.completed"}, {Event: "x", Map: map[string]string{"model": "response.model", "input_tokens": "a"}}}}
	got := withModelPaths(rules, modelPaths{})
	if got.JSON.Map["model"] != "tools.0.model" || got.SSE[0].Map["model"] != "response.tools.0.model" ||
		got.SSE[1].Map["model"] != "response.tools.0.model" || got.SSE[1].Map["input_tokens"] != "a" {
		t.Fatalf("rules %+v", got)
	}
	if rules.JSON.Map != nil || rules.SSE[0].Map != nil || rules.SSE[1].Map["model"] != "response.model" {
		t.Fatalf("the given rules changed: %+v", rules)
	}
}
