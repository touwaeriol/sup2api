package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fakeGroupModels lists sorted models by group id, within the group's model
// policy as group.Service does.
type fakeGroupModels map[int64][]string

func (f fakeGroupModels) OfferedModels(_ context.Context, g core.GroupInfo) ([]string, error) {
	return slices.DeleteFunc(slices.Clone(f[g.ID]), func(m string) bool { return !g.AllowsModel(m) }), nil
}

// failingModels cannot read a group's models.
type failingModels struct{}

func (failingModels) OfferedModels(context.Context, core.GroupInfo) ([]string, error) {
	return nil, errors.New("database down")
}

// modelsEnv is apiEnv whose test group offers a Gemini, an OpenAI and an
// embedding model.
func modelsEnv(t *testing.T) (*env, *apiUpstream) {
	e, u, _, _ := apiEnv(t)
	e.gw.d.Models = fakeGroupModels{testGroup: {"gemini-2.5-pro", "gpt-4o", "text-embedding-004"}}
	return e, u
}

// get sends a GET with exactly these headers.
func (e *env) get(path string, headers map[string]string) result {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return result{status: resp.StatusCode, header: resp.Header, body: data}
}

func anthropicKey(key string) map[string]string {
	return map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
}

func googKey(key string) map[string]string { return map[string]string{"x-goog-api-key": key} }

func ids(r result, path string) string {
	var out []string
	for _, v := range r.json().Get(path).Array() {
		out = append(out, v.String())
	}
	return strings.Join(out, ",")
}

const listedModels = "gemini-2.5-pro,gpt-4o,text-embedding-004"

// GET /v1/models answers in the format the client's headers name, as new-api
// does; /v1beta/models is always Gemini's. No upstream is asked and nothing
// is recorded.
func TestModelListFormats(t *testing.T) {
	e, u := modelsEnv(t)
	r := e.get("/v1/models", bearer())
	if r.status != 200 || r.json().Get("object").String() != "list" || ids(r, "data.#.id") != listedModels ||
		r.json().Get("data.1").Raw != `{"id":"gpt-4o","object":"model","created":0,"owned_by":"sub2api"}` {
		t.Fatalf("openai: %d %s", r.status, r.body)
	}
	r = e.get("/v1/models", anthropicKey(testKey))
	if j := r.json(); r.status != 200 || ids(r, "data.#.id") != listedModels || j.Get("data.0.type").String() != "model" ||
		j.Get("has_more").Bool() || j.Get("first_id").String() != "gemini-2.5-pro" || j.Get("last_id").String() != "text-embedding-004" {
		t.Fatalf("anthropic: %d %s", r.status, r.body)
	}
	// x-api-key without anthropic-version is not Anthropic's client.
	if r = e.get("/v1/models", map[string]string{"x-api-key": testKey}); r.status != 200 || r.json().Get("object").String() != "list" {
		t.Fatalf("x-api-key alone: %d %s", r.status, r.body)
	}
	// anthropic-version alone (an Anthropic SDK on a bearer token) is
	// Anthropic's client too.
	bearerAnthropic := map[string]string{"authorization": "Bearer " + testKey, "anthropic-version": "2023-06-01"}
	if r := e.get("/v1/models", bearerAnthropic); r.status != 200 || r.json().Get("data.0.type").String() != "model" {
		t.Fatalf("bearer anthropic: %d %s", r.status, r.body)
	}
	if r.header.Get("Cache-Control") == "" {
		t.Fatal("a key's model list must not be cached")
	}
	if len(u.keys()) != 0 {
		t.Fatal("the model list reached an upstream")
	}
	e.noRecord()
}

// Gemini's key on /v1, and any key on /v1beta, get Gemini's list in one
// page whatever the paging parameters.
func TestModelListGeminiFormat(t *testing.T) {
	e, _ := modelsEnv(t)
	for _, c := range []struct {
		path    string
		headers map[string]string
	}{{"/v1/models", googKey(testKey)}, {"/v1/models?key=" + testKey, nil}, {"/v1beta/models", bearer()}, {"/v1beta/models?pageSize=1", googKey(testKey)}} {
		r := e.get(c.path, c.headers)
		if j := r.json(); r.status != 200 || ids(r, "models.#.name") != "models/gemini-2.5-pro,models/gpt-4o,models/text-embedding-004" ||
			j.Get("nextPageToken").Exists() || j.Get("models.0.displayName").String() != "gemini-2.5-pro" || j.Get("models.0.version").String() == "" {
			t.Fatalf("gemini %s: %d %s", c.path, r.status, r.body)
		}
	}
}

// One model: Gemini names it with or without "models/".
func TestModelListOneModel(t *testing.T) {
	e, _ := modelsEnv(t)
	gemini := "countTokens,generateContent,streamGenerateContent"
	for _, path := range []string{"/v1beta/models/gemini-2.5-pro", "/v1beta/models/models/gemini-2.5-pro", "/v1/models/models/gemini-2.5-pro?key=" + testKey} {
		r := e.get(path, googKey(testKey))
		if j := r.json(); r.status != 200 || j.Get("name").String() != "models/gemini-2.5-pro" || ids(r, "supportedGenerationMethods") != gemini {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
	}
	r := e.get("/v1beta/models/text-embedding-004", googKey(testKey))
	for _, m := range r.json().Get("supportedGenerationMethods").Array() {
		if !strings.Contains(strings.ToLower(m.String()), "embed") {
			t.Fatalf("embedding model lists %s: %s", m, r.body)
		}
	}
	if r = e.get("/v1/models/gpt-4o", bearer()); r.status != 200 || r.json().Get("id").String() != "gpt-4o" || r.json().Get("object").String() != "model" {
		t.Fatalf("openai model: %d %s", r.status, r.body)
	}
	if r = e.get("/v1/models/gpt-4o", anthropicKey(testKey)); r.status != 200 || r.json().Get("type").String() != "model" || r.json().Get("display_name").String() != "gpt-4o" {
		t.Fatalf("anthropic model: %d %s", r.status, r.body)
	}
}

// A model the group does not offer is a 404 in the client's format; only
// Gemini's names drop "models/".
func TestModelListNotFound(t *testing.T) {
	e, _ := modelsEnv(t)
	for _, c := range []struct {
		path    string
		headers map[string]string
		check   string // a path of the error body
		want    string
	}{
		{"/v1beta/models/unknown", googKey(testKey), "error.status", "NOT_FOUND"},
		{"/v1/models/unknown", bearer(), "error.code", "model_not_found"},
		{"/v1/models/models/gpt-4o", bearer(), "error.type", "not_found_error"},
		{"/v1/models/unknown", anthropicKey(testKey), "error.type", "not_found_error"},
	} {
		if r := e.get(c.path, c.headers); r.status != 404 || r.json().Get(c.check).String() != c.want {
			t.Fatalf("%s: %d %s", c.path, r.status, r.body)
		}
	}
}

// A missing or unknown key is a 401 in the client's format.
func TestModelListAuth(t *testing.T) {
	e, _ := modelsEnv(t)
	for _, c := range []struct {
		path    string
		headers map[string]string
		check   string
		want    string
	}{
		{"/v1/models", nil, "error.type", "authentication_error"},
		{"/v1/models/gpt-4o", anthropicKey("sk-s2a-unknown"), "type", "error"},
		{"/v1/models", googKey("sk-s2a-unknown"), "error.status", "UNAUTHENTICATED"},
		{"/v1beta/models/gemini-2.5-pro", nil, "error.status", "UNAUTHENTICATED"},
		{"/v1beta/models?key=sk-s2a-unknown", nil, "error.status", "UNAUTHENTICATED"},
	} {
		if r := e.get(c.path, c.headers); r.status != 401 || r.json().Get(c.check).String() != c.want {
			t.Fatalf("%s %v: %d %s", c.path, c.headers, r.status, r.body)
		}
	}
}

// The model list takes GETs of its own paths only: the Gemini endpoints
// below /models keep serving POSTs, and other methods are not answered.
func TestModelListLeavesEndpoints(t *testing.T) {
	e, u := modelsEnv(t)
	gen := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}}
	for _, path := range []string{"/v1beta/models/gemini-2.5-pro:generateContent", "/v1/models/gemini-2.5-pro:generateContent"} {
		r := e.post(path, gen, googKey(testKey))
		if r.status != 200 || r.json().Get("candidates.0.content.parts.0.text").String() != "llo" {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		if rec := e.record(); !rec.Success || rec.Protocol != "gemini.generate" {
			t.Fatalf("%s record %+v", path, rec)
		}
	}
	r := e.post("/v1beta/models/gemini-2.5-pro:countTokens", gen, googKey(testKey))
	if r.status != 200 || r.json().Get("totalTokens").Int() != 31 {
		t.Fatalf("count tokens: %d %s", r.status, r.body)
	}
	e.record()
	calls := len(u.keys())
	if r = e.post("/v1/models", nil, bearer()); r.status != 404 || r.json().Get("error").String() != "core 404" {
		t.Fatalf("POST /v1/models: %d %s", r.status, r.body)
	}
	if r = e.get("/v1/models/", bearer()); r.status != 404 || r.json().Get("error").String() != "core 404" {
		t.Fatalf("GET /v1/models/: %d %s", r.status, r.body)
	}
	if len(u.keys()) != calls {
		t.Fatal("a request the model list declined reached an upstream")
	}
	e.noRecord()
}

// The Gemini methods are those of the generation's endpoints on the path's
// API version; embedding methods go to embedding models only.
func TestGeminiMethodsFromEndpoints(t *testing.T) {
	gen := newFakeGen()
	gen.addPlatform(core.PluginInfo{Key: "emb"}, manifest.Platform{ID: "emb", Endpoints: []manifest.Endpoint{
		{Method: "POST", Path: "/v1beta/models/:model:asyncBatchEmbedContent", Protocol: "emb.batch"},
		{Method: "GET", Path: "/v1beta/models/:model:watch", Protocol: "emb.watch"},
		{Method: "POST", Path: "/v1beta/tunedModels/:model:tune", Protocol: "emb.tune"},
	}})
	table := buildRouteTable(gen)
	beta := table.geminiMethods("v1beta")
	if !slices.Contains(beta.embed, "asyncBatchEmbedContent") || strings.Join(beta.generate, ",") != "countTokens,generateContent,streamGenerateContent" {
		t.Fatalf("v1beta %+v", beta)
	}
	for _, m := range beta.embed {
		if !strings.Contains(strings.ToLower(m), "embed") {
			t.Fatalf("embed method %s", m)
		}
	}
	if v1 := table.geminiMethods("v1"); slices.Contains(v1.embed, "asyncBatchEmbedContent") || !slices.Equal(v1.generate, beta.generate) {
		t.Fatalf("v1 %+v", v1)
	}
	if got := beta.of("Text-Embedding-004"); !slices.Equal(got, beta.embed) {
		t.Fatalf("embedding model methods %v", got)
	}
	if m := (*routeTable)(nil).geminiMethods("v1"); m.embed == nil || m.generate == nil {
		t.Fatal("methods must render as [] without a table")
	}
}

// The list is of the key's group, within the group's model policy; a model
// id may hold slashes, sent as they are or escaped.
func TestModelListKeyGroup(t *testing.T) {
	e, _ := modelsEnv(t)
	e.gw.d.Models = fakeGroupModels{testGroup: {"gemini-2.5-pro", "gpt-4o", "org/model-x"}}
	e.auth.change(func(a *fakeAuth) {
		a.keys[testKey].Group.ModelFilterMode, a.keys[testKey].Group.ModelAllowlist = "blacklist", []string{"gpt-*"}
	})
	if r := e.get("/v1/models", bearer()); r.status != 200 || ids(r, "data.#.id") != "gemini-2.5-pro,org/model-x" {
		t.Fatalf("policy: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/v1/models/org/model-x", "/v1/models/org%2Fmodel-x", "/v1beta/models/models/org%2Fmodel-x"} {
		r := e.get(path, bearer())
		if id := r.json().Get("id").String() + r.json().Get("name").String(); r.status != 200 || !strings.HasSuffix(id, "org/model-x") {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
	}
	if r := e.get("/v1/models/gpt-4o", bearer()); r.status != 404 {
		t.Fatalf("a model outside the policy: %d %s", r.status, r.body)
	}
}

// A key the API key service refuses (disabled group, IP not allowed, ...)
// gets its error in the client's format; the service is told the client's
// IP as for any gateway request.
func TestModelListKeyRefused(t *testing.T) {
	e, _ := modelsEnv(t)
	auth := &clientIPAuth{}
	e.gw.d.Auth = auth
	for _, c := range []struct {
		path    string
		headers map[string]string
		check   string
		want    string
	}{
		{"/v1/models", bearer(), "error.code", "ip_not_allowed"},
		{"/v1/models", anthropicKey(testKey), "error.type", "permission_error"},
		{"/v1beta/models", googKey(testKey), "error.status", "PERMISSION_DENIED"},
	} {
		if r := e.get(c.path, c.headers); r.status != 403 || r.json().Get(c.check).String() != c.want {
			t.Fatalf("%s %v: %d %s", c.path, c.headers, r.status, r.body)
		}
	}
	if auth.ip != "127.0.0.1" {
		t.Fatalf("client IP %q", auth.ip)
	}
	e.noRecord()
}

// A list that cannot be read is a 500 in the client's format; without a
// lister the paths stay the core's 404.
func TestModelListUnavailable(t *testing.T) {
	e, _ := modelsEnv(t)
	e.gw.d.Models = failingModels{}
	if r := e.get("/v1beta/models", googKey(testKey)); r.status != 500 || r.json().Get("error.status").String() != "INTERNAL" ||
		strings.Contains(string(r.body), "database down") {
		t.Fatalf("lister error: %d %s", r.status, r.body)
	}
	e.gw.d.Models = nil
	if r := e.get("/v1/models", bearer()); r.status != 404 || r.json().Get("error").String() != "core 404" {
		t.Fatalf("no lister: %d %s", r.status, r.body)
	}
}
