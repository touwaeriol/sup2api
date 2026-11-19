package apikey

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"unicode"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// The key is any non-empty string, saved trimmed: no prefix, character set
// or length rule (a key with control characters is saved too; it fails at
// send time, net/http and the core refuse such a header value).
func TestValidateKey(t *testing.T) {
	s := Spec{AccountType: "apikey", DefaultBaseURL: "https://api.example.com"}
	for _, key := range []string{"k", "a b\tc", "ключ", "sk-" + strings.Repeat("k", 4093), "abc\r\ndef"} {
		creds, _ := json.Marshal(map[string]string{"api_key": " " + key + " "})
		want, _ := json.Marshal(map[string]string{"api_key": key})
		r := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: string(creds)})
		if len(r.GetErrors()) != 0 || r.GetNormalizedCredentialsJson() != string(want) {
			t.Errorf("%q: errors %v, saved %s", key, r.GetErrors(), r.GetNormalizedCredentialsJson())
		}
	}
	for creds, code := range map[string]string{`{}`: "required", `{"api_key":null}`: "required", `{"api_key":" \t "}`: "required", `{"api_key":42}`: "type"} {
		r := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: creds})
		if e := r.GetErrors(); len(e) != 1 || e[0].GetField() != "api_key" || e[0].GetCode() != code {
			t.Errorf("%s: errors %v, want one %s", creds, e, code)
		}
	}
}

// Normalization stays in the object each field came from: base_url in
// settings remains a setting (the core's guardedSettings check and the
// console form read it there), and the other keys Fields names pass through
// as they are. The stage 1 refactor once merged everything into the
// credentials and returned "{}" settings.
func TestValidateNormalizesInPlace(t *testing.T) {
	s := Spec{AccountType: "apikey", DefaultBaseURL: "https://api.example.com", Versions: []string{"/v1"},
		Fields: fieldcheck.Fields{"note": fieldcheck.Any(), "region": fieldcheck.Any()}}
	r := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey",
		CredentialsJson: `{"api_key":"  sk-example-123  ","note":"keep"}`,
		SettingsJson:    `{"base_url":"https://proxy.example.com/v1/","region":"eu"}`})
	if len(r.GetErrors()) != 0 {
		t.Fatal(r.GetErrors())
	}
	if r.GetNormalizedCredentialsJson() != `{"api_key":"sk-example-123","note":"keep"}` ||
		r.GetNormalizedSettingsJson() != `{"base_url":"https://proxy.example.com/v1","region":"eu"}` {
		t.Fatalf("creds=%s settings=%s", r.GetNormalizedCredentialsJson(), r.GetNormalizedSettingsJson())
	}
	// Nothing in settings: "" keeps whatever the core has.
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":"sk-example-123"}`})
	if r.GetNormalizedSettingsJson() != "" || r.GetNormalizedCredentialsJson() != `{"api_key":"sk-example-123"}` {
		t.Fatalf("creds=%s settings=%q", r.GetNormalizedCredentialsJson(), r.GetNormalizedSettingsJson())
	}
}

// NormalizeBaseURL keeps what the user wrote: the path in its escaped form,
// every suffix; only surrounding whitespace, trailing "/" and the scheme's
// case go.
func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		" https://proxy.example.com/v1beta/ ":      "https://proxy.example.com/v1beta",
		"https://proxy.example.com/x/v1/v1beta":    "https://proxy.example.com/x/v1/v1beta",
		"https://proxy.example.com/v1//":           "https://proxy.example.com/v1",
		"https://proxy.example.com/":               "https://proxy.example.com",
		"https://proxy.example.com":                "https://proxy.example.com",
		"HTTPS://Proxy.Example.com/V1":             "https://Proxy.Example.com/V1",
		"https://x.example.com/api/projects/a%2Fb": "https://x.example.com/api/projects/a%2Fb",
		"https://x.example.com/a%2F":               "https://x.example.com/a%2F",
		"https://x.example.com/a%7Eb":              "https://x.example.com/a%7Eb",
		"https://x.example.com/~user":              "https://x.example.com/~user",
		"https://x.example.com/ключ/v1":            "https://x.example.com/%D0%BA%D0%BB%D1%8E%D1%87/v1",
		"https://x.example.com/a%20b":              "https://x.example.com/a%20b",
		"https://x%C2%A0y.example":                 "https://x%C2%A0y.example",
		"http://127.0.0.1:8080/":                   "http://127.0.0.1:8080",
		"https://[::1]:8080/p":                     "https://[::1]:8080/p",
	} {
		if got, err := NormalizeBaseURL(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://x.example", "https://u:p@x.example", "https://x.example/?", "https://x.example/?a=1", "https://x.example/#f", "/relative",
		"https://x.example/#", "https://x.example/a b", "https://x.example/a\tb", "https://x.example/a\nb", "https://x.example/a\u00a0b", "https://x.example/a\u3000b", "https://x .example",
		"https://x.example/a%zz"} {
		if _, err := NormalizeBaseURL(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// TestNormalizeBaseURLIsStable: requests normalize the stored base_url
// again (Spec.FromAccount). A stored value is an output of NormalizeBaseURL:
// raw non-ASCII bytes are percent-encoded and whitespace, "?" and "#" were
// refused, so it holds none of them, its path is valid escaped form, and it
// comes out unchanged and without an error. (The former form schema is no
// reason: RE2's \s is ASCII only, a no-break space passed it.)
func TestNormalizeBaseURLIsStable(t *testing.T) {
	for _, in := range []string{
		" https://proxy.example.com/v1beta/ ", "HTTPS://Proxy.Example.com/x/v1", "https://proxy.example.com:8443/a/b/",
		"http://127.0.0.1:8080", "https://x.example.com/a%20b", "https://x.example.com/a%2Fb/c", "https://[::1]:8080/p",
		"https://x.example.com/ключ", "https://x.example.com/~user/v1/", "https://x.example.com/a%7Eb", "https://x.example.com/a%2F/",
		"https://x.example.com/a%C2%A0b", "https://x%C2%A0y.example", "https://x.example.com/a%E3%80%80b",
		"https://x.example.com/a%C2%85b", "https://x.example.com/a%3Fb", "https://x.example.com/a%23b", "https://x.example.com//v1//",
	} {
		stored, err := NormalizeBaseURL(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if again, err := NormalizeBaseURL(stored); err != nil || again != stored {
			t.Errorf("%q: stored %q normalizes to %q %v", in, stored, again, err)
		}
	}
}

// fromAccount is the base URL Spec{Versions: versions}.FromAccount gives
// for a stored base_url.
func fromAccount(t *testing.T, versions []string, stored string) string {
	t.Helper()
	settings, _ := json.Marshal(map[string]string{"base_url": stored})
	cfg, err := Spec{AccountType: "apikey", Versions: versions}.FromAccount(&pluginv1.Account{CredentialsJson: `{"api_key":"k"}`, SettingsJson: string(settings)})
	if err != nil {
		t.Fatalf("%q: %v", stored, err)
	}
	return cfg.BaseURL
}

// FromAccount takes the longest declared version the stored base URL ends
// in, at most one, on whole segments of the escaped path and
// case-sensitively; the rest is the root every route is appended to.
func TestFromAccountRoot(t *testing.T) {
	gemini, azure := []string{"/v1beta", "/v1"}, []string{"/openai/v1", "/openai"}
	for _, c := range []struct {
		versions     []string
		stored, root string
	}{
		{gemini, "https://x.example", "https://x.example"},
		{gemini, "https://x.example/v1", "https://x.example"},
		{gemini, "https://x.example/v1beta/", "https://x.example"},
		{gemini, "https://x.example/v1/v1beta", "https://x.example/v1"},
		{gemini, "https://x.example/v1/v1", "https://x.example/v1"},
		{gemini, "https://x.example/proxy/v1", "https://x.example/proxy"},
		{gemini, "https://x.example/xv1", "https://x.example/xv1"},
		{gemini, "https://x.example/V1", "https://x.example/V1"},
		{gemini, "https://x.example/a%2Fv1", "https://x.example/a%2Fv1"},
		{gemini, "https://x.example/%76%31", "https://x.example/%76%31"},
		{gemini, "https://x.example//v1", "https://x.example"},
		{azure, "https://x.example/openai/v1", "https://x.example"},
		{azure, "https://x.example/openai", "https://x.example"},
		{azure, "https://x.example/api/projects/a%2Fb", "https://x.example/api/projects/a%2Fb"},
		{nil, "https://x.example/v1", "https://x.example/v1"},
		// The host is never taken for a version segment.
		{[]string{"/v1"}, "http://v1", "http://v1"},
		{gemini, "http://v1beta", "http://v1beta"},
		{[]string{"/api/v3"}, "http://api/v3", "http://api/v3"},
		{azure, "https://openai/v1", "https://openai/v1"},
		{azure, "https://openai", "https://openai"},
	} {
		if got := fromAccount(t, c.versions, c.stored); got != c.root {
			t.Errorf("%v %q: root %q, want %q", c.versions, c.stored, got, c.root)
		}
	}
	// The default base URL goes through the same rule; none ends in a version.
	cfg, err := Spec{AccountType: "apikey", DefaultBaseURL: "https://api.example.com", Versions: gemini}.FromAccount(&pluginv1.Account{CredentialsJson: `{"api_key":" k "}`})
	if err != nil || cfg.BaseURL != "https://api.example.com" || cfg.APIKey != "k" {
		t.Fatalf("default = %+v %v", cfg, err)
	}
}

// oldNormalizeBaseURL is the base URL rule before 2026-11-20, kept here only
// as the reference of TestStoredBaseURLsKeepTheirRequests: it stripped the
// first of stripSuffixes the path ended in and cleared RawPath (decoding
// "%2F"). It ran on save, and again on every request. Delete it with that
// test once core 0.1.304 is released.
func oldNormalizeBaseURL(baseStr string, stripSuffixes []string) (string, error) {
	baseStr = strings.TrimSpace(baseStr)
	if baseStr == "" || strings.ContainsFunc(baseStr, unicode.IsSpace) || strings.ContainsAny(baseStr, "?#") {
		return "", fmt.Errorf("refused")
	}
	u, err := url.Parse(baseStr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", fmt.Errorf("refused")
	}
	p := strings.TrimRight(u.Path, "/")
	for _, suffix := range stripSuffixes {
		if strings.HasSuffix(p, suffix) {
			p = strings.TrimSuffix(p, suffix)
			break
		}
	}
	u.Path = strings.TrimRight(p, "/")
	u.RawPath = ""
	return u.String(), nil
}

// TestStoredBaseURLsKeepTheirRequests: every base URL saved under the old
// rule gives the same request root under the new one, for every account
// type's versions (design 2026-11-20 §6.5). The old root was old(S) on the
// stored S = old(input); the new one is the root FromAccount takes from S.
// Every route is appended to that root, so equal roots are equal requests.
func TestStoredBaseURLsKeepTheirRequests(t *testing.T) {
	versions := [][]string{
		nil,                       // volcengine relay
		{"/v1"},                   // openai, anthropic, relay, typesafe
		{"/v1beta", "/v1"},        // gemini, newapi, sub2api
		{"/openai/v1", "/openai"}, // azure_v1, azure_deployment
		{"/api/v3"},               // volcengine apikey
	}
	paths := []string{
		"", "/", "/v1", "/v1/", "/v1/v1", "/v1/v1beta", "/v1beta", "/v1beta/v1", "/v1beta/v1beta", "/V1", "/xv1",
		"/proxy", "/proxy/v1", "/proxy/v1beta/", "/p/a%2Fb", "/a%2Fv1", "/a%2Fv1/v1", "/a%7Eb/v1", "/%76%31", "/ключ/v1",
		"/openai", "/openai/v1", "/openai/v1/openai/v1", "/openai/openai", "/api/projects/openai", "/api/projects/p/openai/v1",
		"/api/v3", "/api/v3/", "/api/v3/api/v3", "//v1", "//v1//v1", "/v1//v1", "/a%20b/v1", "/v3",
	}
	hosts := []string{
		"https://x.example", "HTTP://127.0.0.1:8080", "https://[::1]:8443", "https://x%C2%A0y.example",
		"http://v1", "http://v1beta", "https://openai", "http://api",
	}
	for _, host := range hosts {
		for _, path := range paths {
			for _, l := range versions {
				stored, err := oldNormalizeBaseURL(host+path, l)
				if err != nil {
					t.Fatalf("%q: %v", host+path, err)
				}
				before, _ := oldNormalizeBaseURL(stored, l)
				if after := fromAccount(t, l, stored); before != after {
					t.Errorf("%v: input %q stored %q: %q before, %q now", l, host+path, stored, before, after)
				}
			}
		}
	}
}

// TestModelListRequest: the four model-list callers (anthropic, openai,
// gemini, relay) build the same request; the path and the id location are the
// only differences, and content-type (a body header) is always dropped. The
// caller's header map is copied, so reusing it after the call is safe.
func TestModelListRequest(t *testing.T) {
	cfg := &Config{APIKey: "k", BaseURL: "https://api.example.com"}
	h := map[string]string{"x-api-key": "k", "content-type": "application/json", "anthropic-version": "2023-06-01"}
	r := ModelListRequest(cfg, ModelList{Path: "/v1/models?limit=1000", IdsPath: "data.#.id"}, h)
	if r.GetMethod() != "GET" || r.GetUrl() != "https://api.example.com/v1/models?limit=1000" ||
		r.GetIdsPath() != "data.#.id" || r.GetStripPrefix() != "" {
		t.Fatalf("request = %v", r)
	}
	if _, ok := r.GetHeaders()["content-type"]; ok {
		t.Fatal("content-type must be dropped from a GET")
	}
	if r.GetHeaders()["x-api-key"] != "k" || r.GetHeaders()["anthropic-version"] != "2023-06-01" {
		t.Fatalf("headers = %v", r.GetHeaders())
	}
	if len(h) != 3 || h["content-type"] != "application/json" {
		t.Fatalf("the caller's header map was modified: %v", h)
	}
	// A differently-cased content type is dropped too.
	r = ModelListRequest(cfg, ModelList{Path: "/v1beta/models", IdsPath: "models.#.name", StripPrefix: "models/"},
		map[string]string{"Content-Type": "application/json", "authorization": "Bearer k"})
	if got := r.GetStripPrefix(); got != "models/" {
		t.Fatalf("strip prefix = %q", got)
	}
	if len(r.GetHeaders()) != 1 || r.GetHeaders()["authorization"] != "Bearer k" {
		t.Fatalf("headers = %v", r.GetHeaders())
	}
}

// TestTextsKeepShippedWording: the plugin-specific messages a plugin shipped
// before Spec.Validate existed still come out verbatim, so the console keeps
// showing what it always did.
func TestTextsKeepShippedWording(t *testing.T) {
	unset := Spec{AccountType: "apikey", DefaultBaseURL: "x"}
	if r := unset.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "other"}); r.GetErrors()[0].GetMessage() != "unsupported account type / 不支持的账号类型" {
		t.Fatalf("default message = %q", r.GetErrors()[0].GetMessage())
	}

	s := Spec{AccountType: "relay_key", RequireBaseURL: true, Texts: Texts{
		UnsupportedAccountType: "unsupported account type {type} / 不支持的账号类型",
		KeyNotString:           "API key must be a string / api_key 必须是字符串",
		KeyNotStringCode:       "pattern",
		BaseURLRequired:        "Base URL of the relay is required / 请填写中转站的 Base URL",
	}}
	r := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey"})
	if r.GetErrors()[0].GetMessage() != `unsupported account type "apikey" / 不支持的账号类型` {
		t.Fatalf("account type message = %q", r.GetErrors()[0].GetMessage())
	}
	if r.GetErrors()[0].GetCode() != "unsupported" || r.GetErrors()[0].GetField() != "account_type" {
		t.Fatalf("account type error = %v", r.GetErrors()[0])
	}

	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "relay_key", CredentialsJson: `{"api_key":42}`})
	if e := r.GetErrors()[0]; e.GetCode() != "pattern" || e.GetMessage() != "API key must be a string / api_key 必须是字符串" {
		t.Fatalf("non-string key = %v", e)
	}

	// api_key is checked before base_url, so an empty form lists them in that
	// order (relay used to list base_url first).
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "relay_key", CredentialsJson: `{}`})
	if len(r.GetErrors()) != 2 || r.GetErrors()[0].GetField() != "api_key" || r.GetErrors()[1].GetField() != "base_url" {
		t.Fatalf("empty form errors = %v", r.GetErrors())
	}
	if got := r.GetErrors()[1].GetMessage(); got != "Base URL of the relay is required / 请填写中转站的 Base URL" {
		t.Fatalf("required message = %q", got)
	}
}

// TestTextsAreNotFormatStrings: a Texts message is plain text, so a bare
// percent sign ("100%") reaches the console unchanged and an unknown
// placeholder stays as written. Before, the message went through Sprintf and
// "100%" came out as "%!d(MISSING)" or swallowed the argument.
func TestTextsAreNotFormatStrings(t *testing.T) {
	s := Spec{AccountType: "apikey", DefaultBaseURL: "https://api.example.com", Texts: Texts{
		UnsupportedAccountType: "unsupported account type {type} (100% sure) / 不支持的账号类型",
		KeyNotString:           "key must be text, 100% / key 必须是字符串",
		KeyNotStringCode:       "type",
		BaseURLFormat:          "base_url {reason}: must be 100% absolute / base_url 格式错误",
	}}
	r := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "other"})
	if got := r.GetErrors()[0].GetMessage(); got != `unsupported account type "other" (100% sure) / 不支持的账号类型` {
		t.Fatalf("account type message = %q", got)
	}
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":42}`})
	if got := r.GetErrors()[0].GetMessage(); got != "key must be text, 100% / key 必须是字符串" {
		t.Fatalf("key message = %q", got)
	}
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":42}`})
	if e := r.GetErrors()[0]; e.GetCode() != "type" || e.GetMessage() != "key must be text, 100% / key 必须是字符串" {
		t.Fatalf("non-string key = %v", e)
	}
	// The code is configured separately from the message: leaving it empty
	// keeps the historical "pattern".
	s.Texts.KeyNotStringCode = ""
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":42}`})
	if e := r.GetErrors()[0]; e.GetCode() != "pattern" || e.GetMessage() != "key must be text, 100% / key 必须是字符串" {
		t.Fatalf("non-string key = %v", e)
	}
	r = s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":"sk-12345678"}`, SettingsJson: `{"base_url":"ftp://x"}`})
	if e := r.GetErrors()[0]; e.GetField() != "base_url" || e.GetCode() != "format" ||
		e.GetMessage() != "base_url must be an absolute http(s) URL: must be 100% absolute / base_url 格式错误" {
		t.Fatalf("format error = %v", e)
	}
}

// Fields checks the keys it names and refuses the ones the form does not
// send (in either object); nil is the same as empty. A rule on api_key or
// base_url runs only where the Spec's own check passed.
func TestSpecFields(t *testing.T) {
	req := &pluginv1.ValidateCredentialsRequest{AccountType: "apikey",
		CredentialsJson: `{"api_key":"sk-12345678","organization":"o","extra":1}`, SettingsJson: `{"base_url":"https://u.example","proxy":"p"}`}
	errs := func(s Spec, creds, settings string) map[string]string {
		resp := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: creds, SettingsJson: settings})
		got := map[string]string{}
		for _, e := range resp.GetErrors() {
			got[e.GetField()] = e.GetCode()
		}
		return got
	}
	org := Spec{AccountType: "apikey", Fields: fieldcheck.Fields{"organization": fieldcheck.String().MaxLen(3)}}
	if got := errs(org, req.CredentialsJson, req.SettingsJson); !reflect.DeepEqual(got, map[string]string{"extra": "unknown", "proxy": "unknown"}) {
		t.Fatalf("unknown keys = %v", got)
	}
	if got := errs(org, `{"api_key":"sk-12345678","organization":"long"}`, ""); !reflect.DeepEqual(got, map[string]string{"organization": "maxLength"}) {
		t.Fatalf("rule = %v", got)
	}
	if got := errs(org, `{"api_key":"sk-12345678"}`, `{"organization":7}`); !reflect.DeepEqual(got, map[string]string{"organization": "type"}) {
		t.Fatalf("rule on a setting = %v", got)
	}
	for _, empty := range []Spec{{AccountType: "apikey"}, {AccountType: "apikey", Fields: fieldcheck.Fields{}}} {
		if got := errs(empty, req.CredentialsJson, req.SettingsJson); !reflect.DeepEqual(got, map[string]string{"organization": "unknown", "extra": "unknown", "proxy": "unknown"}) {
			t.Fatalf("nil or empty Fields (%v) allow api_key and base_url only: %v", empty.Fields == nil, got)
		}
	}
	empty := Spec{AccountType: "apikey"}
	resp := empty.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey",
		CredentialsJson: `{"api_key":"sk-12345678"}`, SettingsJson: `{"base_url":"https://u.example/"}`})
	if len(resp.GetErrors()) != 0 || resp.GetNormalizedSettingsJson() != `{"base_url":"https://u.example"}` {
		t.Fatalf("valid = %v", resp)
	}
	endpoint := Spec{AccountType: "apikey", Fields: fieldcheck.Fields{"base_url": fieldcheck.String().Pattern(`^https://[a-z.]+$`)}}
	if got := errs(endpoint, `{"api_key":"sk-12345678","base_url":"https://u.example/p"}`, ""); !reflect.DeepEqual(got, map[string]string{"base_url": "pattern"}) {
		t.Fatalf("base_url rule = %v", got)
	}
	// The rules see what Validate saves: the key trimmed, the base URL
	// normalized with its version kept, the one Spec read (settings before
	// credentials) in both objects.
	key := Spec{AccountType: "apikey", Fields: fieldcheck.Fields{"api_key": fieldcheck.String().MaxLen(512).Pattern(`^[!-~]*$`)}}
	if got := errs(key, `{"api_key":" sk-`+strings.Repeat("k", 509)+` "}`, ""); len(got) != 0 {
		t.Fatalf("a 512-character key with spaces around = %v", got)
	}
	root := Spec{AccountType: "apikey", Versions: []string{"/v1"}, Fields: fieldcheck.Fields{"base_url": fieldcheck.String().Pattern(`^https://good\.example$`)}}
	if got := errs(root, `{"api_key":"sk-12345678","base_url":" HTTPS://good.example/ "}`, ""); len(got) != 0 {
		t.Fatalf("a base URL normalized onto the rule = %v", got)
	}
	if got := errs(root, `{"api_key":"sk-12345678","base_url":"https://good.example/v1"}`, ""); !reflect.DeepEqual(got, map[string]string{"base_url": "pattern"}) {
		t.Fatalf("the rule sees the version kept = %v", got)
	}
	if got := errs(root, `{"api_key":"sk-12345678","base_url":"https://bad.example"}`, `{"base_url":"https://good.example"}`); len(got) != 0 {
		t.Fatalf("the rule checks the base URL Spec uses (settings first) = %v", got)
	}
	if got := errs(root, `{"api_key":"sk-12345678","base_url":"https://good.example"}`, `{"base_url":"https://bad.example"}`); !reflect.DeepEqual(got, map[string]string{"base_url": "pattern"}) {
		t.Fatalf("the settings' base URL is the one saved = %v", got)
	}
	// One error per field: the Spec's own, not the rule's too.
	if resp := endpoint.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey", CredentialsJson: `{"api_key":"sk-12345678","base_url":"ftp://u"}`}); len(resp.GetErrors()) != 1 || resp.GetErrors()[0].GetCode() != "format" {
		t.Fatalf("refused base_url = %v", resp.GetErrors())
	}
}

// NoBaseURL: a type without base_url refuses one in either object as an
// unknown key, whatever its value and even when Fields names it, and saves
// the others as they are; FromAccount reads no base_url.
func TestSpecNoBaseURL(t *testing.T) {
	s := Spec{AccountType: "apikey", NoBaseURL: true, Fields: fieldcheck.Fields{"project_id": fieldcheck.Any(), "base_url": fieldcheck.Any()}}
	for _, req := range []*pluginv1.ValidateCredentialsRequest{
		{AccountType: "apikey", CredentialsJson: `{"api_key":"sk-12345678","base_url":"https://u.example"}`},
		{AccountType: "apikey", CredentialsJson: `{"api_key":"sk-12345678"}`, SettingsJson: `{"base_url":"not a url"}`},
	} {
		if errs := s.Validate(req).GetErrors(); len(errs) != 1 || errs[0].GetField() != "base_url" || errs[0].GetCode() != "unknown" {
			t.Fatalf("%s %s: %v", req.CredentialsJson, req.SettingsJson, errs)
		}
	}
	resp := s.Validate(&pluginv1.ValidateCredentialsRequest{AccountType: "apikey",
		CredentialsJson: `{"api_key":" sk-12345678 "}`, SettingsJson: `{"project_id":"p"}`})
	if len(resp.GetErrors()) != 0 || resp.GetNormalizedCredentialsJson() != `{"api_key":"sk-12345678"}` || resp.GetNormalizedSettingsJson() != `{"project_id":"p"}` {
		t.Fatalf("valid = %v", resp)
	}
	cfg, err := s.FromAccount(&pluginv1.Account{CredentialsJson: `{"api_key":"k"}`, SettingsJson: `{"base_url":"not a url"}`})
	if err != nil || *cfg != (Config{APIKey: "k"}) {
		t.Fatalf("FromAccount = %+v, %v", cfg, err)
	}
}
