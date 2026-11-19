package volcengine

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// The account forms are native code (ui/native) and the core checks nothing
// else on save, so ValidateCredentials is the only check of the credential
// constraints. These tests pin that it refuses one counterexample of each,
// written out here.
//
// The core splits the form's object by the type's settingsFields before it
// calls ValidateCredentials (server account/creds.go prepare), and so do these
// tests.

// rejectCase is one input ValidateCredentials refuses: base with `set`
// applied (a nil value deletes the key), refused at `field`.
type rejectCase struct {
	name  string
	set   map[string]any
	field string
}

// settingsFields are the manifest's accountTypes[].settingsFields.
var settingsFields = map[string][]string{
	AccountTypeAPIKey: {"base_url", "asset_base_url", "asset_region"},
	AccountTypeRelay:  {"base_url", "video_endpoint", "asset_endpoint", "api_prefix", "video_api_prefix", "asset_base_url", "asset_region"},
}

// formInput is base with set applied, split the way the core splits it.
func formInput(accountType string, base, set map[string]any) *pluginv1.ValidateCredentialsRequest {
	creds, settings := map[string]any{}, map[string]any{}
	for k, v := range base {
		creds[k] = v
	}
	for k, v := range set {
		if v == nil {
			delete(creds, k)
		} else {
			creds[k] = v
		}
	}
	for _, f := range settingsFields[accountType] {
		if v, ok := creds[f]; ok {
			settings[f] = v
			delete(creds, f)
		}
	}
	c, _ := json.Marshal(creds)
	s, _ := json.Marshal(settings)
	return &pluginv1.ValidateCredentialsRequest{AccountType: accountType, CredentialsJson: string(c), SettingsJson: string(s)}
}

func assertRefused(t *testing.T, accountType string, base map[string]any, cases []rejectCase) {
	t.Helper()
	p := New()
	ok, err := p.ValidateCredentials(context.Background(), formInput(accountType, base, nil))
	if err != nil || len(ok.GetErrors()) != 0 {
		t.Fatalf("%s: the valid base is refused: %v %v", accountType, ok.GetErrors(), err)
	}
	for _, c := range cases {
		r, err := p.ValidateCredentials(context.Background(), formInput(accountType, base, c.set))
		if err != nil {
			t.Fatalf("%s %s: %v", accountType, c.name, err)
		}
		fields := []string{}
		for _, e := range r.GetErrors() {
			fields = append(fields, e.GetField())
		}
		if len(fields) == 0 {
			t.Errorf("%s %s: accepted, want refused", accountType, c.name)
		} else if !strings.Contains(","+strings.Join(fields, ",")+",", ","+c.field+",") {
			t.Errorf("%s %s: refused at %v, want %s", accountType, c.name, fields, c.field)
		}
	}
}

// assetCases are the asset library fields both types share: access_key and
// secret_key at most 256 visible ASCII characters, asset_base_url an
// optional http(s) URL without query or fragment, asset_region at most 64
// of [a-z0-9-]. The AK/SK pair of the base is valid, so each case fails on
// its own field.
var assetCases = []rejectCase{
	{"access_key of 257 characters", map[string]any{"access_key": strings.Repeat("a", 257)}, "access_key"},
	{"access_key with a space", map[string]any{"access_key": "AKLT 12345678"}, "access_key"},
	{"access_key not ASCII", map[string]any{"access_key": "AKLT密钥12345678"}, "access_key"},
	{"access_key not a string", map[string]any{"access_key": 12345678}, ""},
	{"secret_key of 257 characters", map[string]any{"secret_key": strings.Repeat("s", 257)}, "secret_key"},
	{"secret_key with a space", map[string]any{"secret_key": "secret 12345678"}, "secret_key"},
	{"secret_key not ASCII", map[string]any{"secret_key": "secret密钥12345678"}, "secret_key"},
	{"asset_base_url not http", map[string]any{"asset_base_url": "ftp://ark.example.com"}, "asset_base_url"},
	{"asset_base_url relative", map[string]any{"asset_base_url": "ark.example.com"}, "asset_base_url"},
	{"asset_base_url with a query", map[string]any{"asset_base_url": "https://ark.example.com/?a=1"}, "asset_base_url"},
	{"asset_base_url with a fragment", map[string]any{"asset_base_url": "https://ark.example.com/#a"}, "asset_base_url"},
	{"asset_base_url with a space", map[string]any{"asset_base_url": "https://ark.example.com/a b"}, "asset_base_url"},
	{"asset_region of 65 characters", map[string]any{"asset_region": strings.Repeat("a", 65)}, "asset_region"},
	{"asset_region upper case", map[string]any{"asset_region": "CN-Beijing"}, "asset_region"},
	{"asset_region with an underscore", map[string]any{"asset_region": "cn_beijing"}, "asset_region"},
	{"asset_region not a string", map[string]any{"asset_region": 1}, ""},
}

// apiKeyCases: api_key required, a non-empty string; nothing else about it is
// checked (acceptedKeys).
var apiKeyCases = []rejectCase{
	{"no api_key", map[string]any{"api_key": nil}, "api_key"},
	{"empty api_key", map[string]any{"api_key": ""}, "api_key"},
	{"blank api_key", map[string]any{"api_key": " \t "}, "api_key"},
	{"api_key not a string", map[string]any{"api_key": 12345678}, "api_key"},
}

// acceptedKeys are api_key values both types accept: any non-empty string,
// whatever its length or characters.
var acceptedKeys = []string{"1234567", strings.Repeat("k", 4096), "ark key 12345678", "ark-密钥-12345678"}

// assertKeysAccepted checks that each of acceptedKeys on base is accepted.
func assertKeysAccepted(t *testing.T, accountType string, base map[string]any) {
	t.Helper()
	for _, key := range acceptedKeys {
		r, err := New().ValidateCredentials(context.Background(), formInput(accountType, base, map[string]any{"api_key": key}))
		if err != nil || len(r.GetErrors()) != 0 {
			t.Errorf("%s api_key %.20q: %v %v, want it accepted", accountType, key, r.GetErrors(), err)
		}
	}
}

func TestApiKeyFormConstraints(t *testing.T) {
	base := map[string]any{"api_key": "ark-key-12345678", "access_key": "AKLT12345678", "secret_key": "secret12345678"}
	cases := append(append([]rejectCase{}, apiKeyCases...), assetCases...)
	// base_url: optional, an http(s) URL without query, fragment or spaces.
	cases = append(cases,
		rejectCase{"base_url not http", map[string]any{"base_url": "ftp://ark.example.com"}, "base_url"},
		rejectCase{"base_url relative", map[string]any{"base_url": "ark.cn-beijing.volces.com"}, "base_url"},
		rejectCase{"base_url with a query", map[string]any{"base_url": "https://ark.example.com/?a=1"}, "base_url"},
		rejectCase{"base_url with a fragment", map[string]any{"base_url": "https://ark.example.com/#a"}, "base_url"},
		rejectCase{"base_url with a space", map[string]any{"base_url": "https://ark.example.com/a b"}, "base_url"},
	)
	assertRefused(t, AccountTypeAPIKey, base, cases)
	assertKeysAccepted(t, AccountTypeAPIKey, base)
}

func TestRelayFormConstraints(t *testing.T) {
	base := map[string]any{"api_key": "relay-key-12345678", "base_url": "https://relay.example.com", "access_key": "AKLT12345678", "secret_key": "secret12345678"}
	cases := append(append([]rejectCase{}, apiKeyCases...), assetCases...)
	// base_url: required, at least 8 characters, an http(s) URL without
	// query, fragment or spaces.
	cases = append(cases,
		rejectCase{"no base_url", map[string]any{"base_url": nil}, "base_url"},
		rejectCase{"empty base_url", map[string]any{"base_url": ""}, "base_url"},
		rejectCase{"base_url of 7 characters", map[string]any{"base_url": "http://"}, "base_url"},
		rejectCase{"base_url not http", map[string]any{"base_url": "ftp://relay.example.com"}, "base_url"},
		rejectCase{"base_url with a query", map[string]any{"base_url": "https://relay.example.com/?a=1"}, "base_url"},
		rejectCase{"base_url with a fragment", map[string]any{"base_url": "https://relay.example.com/#a"}, "base_url"},
		rejectCase{"base_url with a space", map[string]any{"base_url": "https://relay.example.com/a b"}, "base_url"},
	)
	// api_prefix, video_api_prefix: at most 128 characters, "/" or a path
	// starting with one slash, without spaces, query or fragment.
	for _, f := range []string{"api_prefix", "video_api_prefix"} {
		cases = append(cases,
			rejectCase{f + " of 129 characters", map[string]any{f: "/" + strings.Repeat("p", 128)}, f},
			rejectCase{f + " without a leading slash", map[string]any{f: "v1"}, f},
			rejectCase{f + " with two leading slashes", map[string]any{f: "//v1"}, f},
			rejectCase{f + " with a space", map[string]any{f: "/v 1"}, f},
			rejectCase{f + " with a query", map[string]any{f: "/v1?a=1"}, f},
			rejectCase{f + " with a fragment", map[string]any{f: "/v1#a"}, f},
			rejectCase{f + " not a string", map[string]any{f: 1}, f},
		)
	}
	// video_endpoint, asset_endpoint: at most 2048 characters, an http(s)
	// URL or a path starting with one slash, without spaces, query or
	// fragment.
	for _, f := range []string{"video_endpoint", "asset_endpoint"} {
		cases = append(cases,
			rejectCase{f + " of 2049 characters", map[string]any{f: "/" + strings.Repeat("e", 2048)}, f},
			rejectCase{f + " relative", map[string]any{f: "api/v3/tasks"}, f},
			rejectCase{f + " with two leading slashes", map[string]any{f: "//relay.example.com/tasks"}, f},
			rejectCase{f + " not http", map[string]any{f: "ftp://relay.example.com/tasks"}, f},
			rejectCase{f + " with a space", map[string]any{f: "/api/v3/my tasks"}, f},
			rejectCase{f + " with a query", map[string]any{f: "https://relay.example.com/tasks?a=1"}, f},
			rejectCase{f + " with a fragment", map[string]any{f: "/tasks#a"}, f},
			rejectCase{f + " not a string", map[string]any{f: 1}, f},
		)
	}
	assertRefused(t, AccountTypeRelay, base, cases)
	assertKeysAccepted(t, AccountTypeRelay, base)

	// Both endpoints are optional and take a path or an http(s) URL.
	p := New()
	for _, f := range []string{"video_endpoint", "asset_endpoint"} {
		for _, v := range []string{"", "/", "/api/v3/contents/generations/tasks", "/api/support/v1/asset", "https://relay.example.com/tasks", "http://relay.example.com/assets"} {
			r, err := p.ValidateCredentials(context.Background(), formInput(AccountTypeRelay, base, map[string]any{f: v}))
			if err != nil || len(r.GetErrors()) != 0 {
				t.Errorf("%s %q refused: %v %v", f, v, r.GetErrors(), err)
			}
		}
	}
}

// TestFormUnknownKeys: each type takes every key its form sends, all at
// once, and refuses any other (code "unknown"), in the credentials and in the
// settings.
func TestFormUnknownKeys(t *testing.T) {
	assets := map[string]any{"api_key": "ark-key-12345678", "access_key": "AKLT12345678", "secret_key": "secret12345678", "asset_region": "cn-beijing"}
	full := map[string]map[string]any{
		AccountTypeAPIKey: withKeys(assets, map[string]any{"base_url": "https://ark.cn-beijing.volces.com", "asset_base_url": "https://ark.cn-beijing.volcengineapi.com"}),
		AccountTypeRelay: withKeys(assets, map[string]any{
			"base_url": "https://relay.example.com", "video_endpoint": "/api/v3/contents/generations/tasks", "asset_endpoint": "/api/support/v1/asset",
			"api_prefix": "/api/v3", "video_api_prefix": "/doubao/api/v3", "asset_base_url": "https://relay.example.com/api/support/v1/asset",
		}),
	}
	p := New()
	for typ, base := range full {
		in := formInput(typ, base, nil)
		if r, err := p.ValidateCredentials(context.Background(), in); err != nil || len(r.GetErrors()) != 0 {
			t.Errorf("%s: every form key at once refused: %v %v", typ, r.GetErrors(), err)
		}
		for name, req := range map[string]*pluginv1.ValidateCredentialsRequest{
			"credentials": formInput(typ, base, map[string]any{"legacy": "x"}),
			"settings":    {AccountType: typ, CredentialsJson: in.GetCredentialsJson(), SettingsJson: strings.TrimSuffix(in.GetSettingsJson(), "}") + `,"legacy":"x"}`},
		} {
			r, err := p.ValidateCredentials(context.Background(), req)
			if errs := r.GetErrors(); err != nil || len(errs) != 1 || errs[0].GetField() != "legacy" || errs[0].GetCode() != "unknown" {
				t.Errorf("%s: an unknown %s key: errors = %v (%v), want legacy:unknown", typ, name, errs, err)
			}
		}
	}
}

// withKeys is base with more keys.
func withKeys(base, more map[string]any) map[string]any {
	out := maps.Clone(base)
	maps.Copy(out, more)
	return out
}
