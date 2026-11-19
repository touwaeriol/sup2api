package volcengine

// Tests of the asset library fields on the account form: the AK/SK pair is
// optional but indivisible, and the endpoint and region have to be usable in
// a V4 credential scope.

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
)

// validate runs ValidateCredentials in process and returns the field errors
// by field name.
func validate(t *testing.T, creds, settings string) (map[string]string, *pluginv1.ValidateCredentialsResponse) {
	t.Helper()
	resp, err := New().ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{
		AccountType: AccountTypeAPIKey, CredentialsJson: creds, SettingsJson: settings,
	})
	if err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
	out := map[string]string{}
	for _, e := range resp.GetErrors() {
		out[e.GetField()] = e.GetCode()
	}
	return out, resp
}

// TestAssetFieldsAreOptional: an Ark account with no asset library is the
// normal case and must validate exactly as before stage three.
func TestAssetFieldsAreOptional(t *testing.T) {
	errs, resp := validate(t, `{"api_key":"ark-key-12345678"}`, `{"base_url":""}`)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if resp.GetNormalizedCredentialsJson() != `{"api_key":"ark-key-12345678"}` {
		t.Fatalf("normalized credentials = %s", resp.GetNormalizedCredentialsJson())
	}
	// No asset key was submitted, so none is invented.
	if strings.Contains(resp.GetNormalizedSettingsJson(), FieldAssetBaseURL) {
		t.Fatalf("an asset endpoint appeared out of nowhere: %s", resp.GetNormalizedSettingsJson())
	}
}

// TestAssetKeyPairIsIndivisible is the pair constraint: half a pair cannot
// sign anything, and accepting it would leave an account that looks
// configured and fails on first use.
func TestAssetKeyPairIsIndivisible(t *testing.T) {
	errs, _ := validate(t, `{"api_key":"ark-key-12345678","access_key":"`+testAK+`"}`, `{}`)
	if errs[FieldSecretKey] != "required" {
		t.Fatalf("access key alone: errors = %v", errs)
	}
	errs, _ = validate(t, `{"api_key":"ark-key-12345678","secret_key":"`+testSK+`"}`, `{}`)
	if errs[FieldAccessKey] != "required" {
		t.Fatalf("secret key alone: errors = %v", errs)
	}
	// An endpoint or a region without a key pair is the same mistake seen
	// from the other side: the account looks enabled and is not.
	errs, _ = validate(t, `{"api_key":"ark-key-12345678"}`, `{"asset_base_url":"`+DefaultAssetBaseURL+`"}`)
	if errs[FieldAccessKey] != "required" {
		t.Fatalf("endpoint without keys: errors = %v", errs)
	}
	errs, _ = validate(t, `{"api_key":"ark-key-12345678"}`, `{"asset_region":"cn-beijing"}`)
	if errs[FieldAccessKey] != "required" {
		t.Fatalf("region without keys: errors = %v", errs)
	}
}

// TestAssetFieldsNormalized: values are trimmed and the endpoint is
// normalized so it still matches guardedSettings (CONTRACTS §21.3) after a
// paste with a trailing slash.
func TestAssetFieldsNormalized(t *testing.T) {
	errs, resp := validate(t,
		`{"api_key":"ark-key-12345678","access_key":"  `+testAK+`  ","secret_key":" `+testSK+` "}`,
		`{"base_url":"","asset_base_url":"`+DefaultAssetBaseURL+`/ ","asset_region":" cn-beijing "}`)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	creds, err := apikey.DecodeObject(resp.GetNormalizedCredentialsJson())
	if err != nil {
		t.Fatal(err)
	}
	if creds[FieldAccessKey] != testAK || creds[FieldSecretKey] != testSK {
		t.Fatalf("normalized credentials = %s", resp.GetNormalizedCredentialsJson())
	}
	settings, err := apikey.DecodeObject(resp.GetNormalizedSettingsJson())
	if err != nil {
		t.Fatal(err)
	}
	if settings[FieldAssetBaseURL] != DefaultAssetBaseURL {
		t.Fatalf("normalized asset endpoint = %v, want %q", settings[FieldAssetBaseURL], DefaultAssetBaseURL)
	}
	if settings[FieldAssetRegion] != DefaultAssetRegion {
		t.Fatalf("normalized asset region = %v", settings[FieldAssetRegion])
	}
	// The API base URL and the asset endpoint are separate values and must
	// not have been merged.
	if settings["base_url"] != DefaultBaseURL {
		t.Fatalf("base_url = %v, want %q", settings["base_url"], DefaultBaseURL)
	}
}

// TestAssetFieldsRejected covers the values that would produce a broken
// signature rather than an obvious failure.
func TestAssetFieldsRejected(t *testing.T) {
	cases := []struct {
		name, creds, settings, field, code string
	}{
		{"endpoint is not a URL", `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"` + testSK + `"}`,
			`{"asset_base_url":"ark.cn-beijing.volcengineapi.com"}`, FieldAssetBaseURL, "format"},
		{"endpoint carries a query", `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"` + testSK + `"}`,
			`{"asset_base_url":"https://ark.cn-beijing.volcengineapi.com/?x=1"}`, FieldAssetBaseURL, "format"},
		{"region with a slash", `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"` + testSK + `"}`,
			`{"asset_region":"cn-beijing/extra"}`, FieldAssetRegion, "pattern"},
		{"region with uppercase", `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"` + testSK + `"}`,
			`{"asset_region":"CN-Beijing"}`, FieldAssetRegion, "pattern"},
		{"access key too short", `{"api_key":"ark-key-12345678","access_key":"AK","secret_key":"` + testSK + `"}`,
			`{}`, FieldAccessKey, "pattern"},
		{"secret key with a space", `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"a b c d e f"}`,
			`{}`, FieldSecretKey, "pattern"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := validate(t, tc.creds, tc.settings)
			if errs[tc.field] != tc.code {
				t.Fatalf("errors = %v, want %s: %s", errs, tc.field, tc.code)
			}
		})
	}
}

// TestAssetConfigOf reads an account the way the routes do.
func TestAssetConfigOf(t *testing.T) {
	acc := func(creds, settings string) *pluginv1.Account {
		return &pluginv1.Account{Id: 3, Name: "a", Type: AccountTypeAPIKey, SettingsJson: settings, CredentialsJson: creds}
	}
	// No pair: the asset library is off, and that is not an error.
	cfg, err := AssetConfigOf(acc(`{"api_key":"ark-key-12345678"}`, `{}`))
	if err != nil || cfg != nil {
		t.Fatalf("plain account = %+v %v", cfg, err)
	}
	// Defaults: the control-plane endpoint and cn-beijing.
	cfg, err = AssetConfigOf(acc(`{"access_key":"`+testAK+`","secret_key":"`+testSK+`"}`, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != DefaultAssetBaseURL || cfg.Region != DefaultAssetRegion {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.AccessKey != testAK || cfg.SecretKey != testSK || cfg.AccountID != 3 {
		t.Fatalf("config = %+v", cfg)
	}
	// The asset endpoint is not the API base URL: setting base_url must not
	// move the asset calls.
	cfg, err = AssetConfigOf(acc(`{"access_key":"`+testAK+`","secret_key":"`+testSK+`"}`,
		`{"base_url":"`+BytePlusBaseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != DefaultAssetBaseURL {
		t.Fatalf("base_url leaked into the asset endpoint: %+v", cfg)
	}
	// A stored account with half a pair (written before stage three, or past
	// the form) is reported, not silently treated as "off".
	if _, err := AssetConfigOf(acc(`{"access_key":"`+testAK+`"}`, `{}`)); err == nil {
		t.Fatal("half a key pair must be an error, not a disabled asset library")
	}
}
