package gateway

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// imageAnswer is the OpenAI answer of an image generation (/v1/images/
// generations) or edit (/v1/images/edits): a JSON body whose usage has both
// breakdowns, or a stream of a partial image and the completed event, whose
// usage has no output breakdown (the API reference).
func imageAnswer(w http.ResponseWriter, path string, stream bool) {
	const usage = `"usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},"output_tokens":4200,` +
		`"output_tokens_details":{"image_tokens":4000,"text_tokens":200},"total_tokens":4250}`
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aGk="}],` + usage + `}`))
		return
	}
	event := "image_generation"
	if strings.HasSuffix(path, "/edits") {
		event = "image_edit"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	sseData(w, event+".partial_image", `{"type":"`+event+`.partial_image","b64_json":"aA==","partial_image_index":0}`)
	sseData(w, event+".completed", `{"type":"`+event+`.completed","b64_json":"aGk=",`+
		`"usage":{"input_tokens":50,"input_tokens_details":{"image_tokens":40,"text_tokens":10},"output_tokens":4000,"total_tokens":4050}}`)
}

// The two image endpoints of the built-in openai platform (CONTRACTS §118):
// an OpenAI API key type serves both, JSON and stream, and the record
// carries the image share of the tokens. A type that lists only the
// Responses endpoints (codex) is never a candidate.
func TestOpenAIImages(t *testing.T) {
	e, u, op, _ := apiEnv(t)
	e.addAccountType("codex", "codex_oauth", apiPlatform(u),
		manifest.AccountPlatform{Platform: "openai", Endpoints: []string{"responses", "responses_websocket"}})
	e.accounts.addTyped(testGroup, 31, 200, "codex-31", "codex", "codex_oauth")
	e.pricer.rules["gpt-image-1.5"] = &core.PriceRule{ID: 12, Model: "gpt-image-1.5", Mode: "expression"}

	for _, c := range []struct {
		path, protocol string
		body           map[string]any
	}{
		{"/v1/images/generations", "openai.images", map[string]any{"model": "gpt-image-1.5", "prompt": "a red fox", "size": "1024x1024"}},
		{"/v1/images/edits", "openai.images_edit", map[string]any{"model": "gpt-image-1.5", "prompt": "add a hat",
			"images": []any{map[string]any{"image_url": "data:image/png;base64,iVBORw0KGgo="}}}},
	} {
		r := e.post(c.path, c.body, bearer())
		if r.status != 200 || r.json().Get("data.0.b64_json").String() != "aGk=" {
			t.Fatalf("%s: %d %s", c.path, r.status, r.body)
		}
		if got := u.last(); got.key != "oai-11" || got.path != c.path || gjson.GetBytes(got.body, "prompt").String() != c.body["prompt"] {
			t.Fatalf("%s upstream call %+v", c.path, got)
		}
		if b := op.builds[len(op.builds)-1]; b.GetMeta().GetProtocol() != c.protocol || b.GetMeta().GetModel() != "gpt-image-1.5" {
			t.Fatalf("%s build meta %+v", c.path, b.GetMeta())
		}
		if hasKey(e.accounts.lastTypes, "codex", "codex_oauth") || !hasKey(e.accounts.lastTypes, "openai", "apikey") {
			t.Fatalf("%s candidate types %v", c.path, e.accounts.lastTypes)
		}
		rec := e.record()
		if !rec.Success || rec.Protocol != c.protocol || rec.Endpoint != c.path || rec.UsageSemantics != "inclusive" || !rec.Billable ||
			rec.Tokens != (core.UsageTokens{Input: 50, Output: 4200, ImageInput: 40, ImageOutput: 4000}) || len(rec.Metrics) != 0 {
			t.Fatalf("%s record %+v", c.path, rec)
		}

		c.body["stream"] = true
		r = e.post(c.path, c.body, bearer())
		if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(r.body), ".completed") {
			t.Fatalf("%s stream: %d %s", c.path, r.status, r.body)
		}
		rec = e.record()
		if !rec.Success || !rec.Stream || rec.Tokens != (core.UsageTokens{Input: 50, Output: 4000, ImageInput: 40, ImageOutput: 4000}) {
			t.Fatalf("%s stream record %+v", c.path, rec)
		}
	}
}

// Only the JSON form of an image edit is served: a multipart body is refused
// as the first step of the gateway, 400 "request body is not valid JSON",
// before any account is picked, priced or sent to. Its record is the free
// invalid_request row of any body that is not JSON.
func TestOpenAIImageEditMultipartRefused(t *testing.T) {
	e, u, op, _ := apiEnv(t)
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("model", "gpt-image-1.5")
	_ = mw.WriteField("prompt", "add a hat")
	part, _ := mw.CreateFormFile("image[]", "fox.png")
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/images/edits", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	j := gjson.ParseBytes(body)
	if resp.StatusCode != 400 || j.Get("error.message").String() != "request body is not valid JSON" ||
		j.Get("error.code").String() != core.ErrInvalidArgument.Code || j.Get("error.type").String() != "invalid_request_error" {
		t.Fatalf("multipart edit: %d %s", resp.StatusCode, body)
	}
	if len(u.keys()) != 0 || len(op.builds) != 0 {
		t.Fatalf("a multipart edit reached the upstream: %v, %d builds", u.keys(), len(op.builds))
	}
	rec := e.record()
	if rec.Success || rec.ErrorType != errTypeInvalidRequest || rec.Protocol != "openai.images_edit" || rec.AccountID != nil ||
		rec.Price != nil || rec.Billable || rec.Tokens != (core.UsageTokens{}) {
		t.Fatalf("multipart edit record %+v", rec)
	}
}
