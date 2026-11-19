package accountform

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

func TestDecode(t *testing.T) {
	for _, raw := range []string{"", "  ", "null", "{}"} {
		if o, err := Decode(raw); err != nil || o == nil || len(o) != 0 {
			t.Errorf("%q: %v %v", raw, o, err)
		}
	}
	for _, raw := range []string{"{", "[1]", `"x"`, "1", `{} {}`, `{"a":1} x`} {
		if _, err := Decode(raw); err == nil {
			t.Errorf("%q decodes", raw)
		}
	}
	o, err := Decode(` {"a":" x ","n":12345678901234567890,"f":1.50,"b":true} `)
	if err != nil {
		t.Fatal(err)
	}
	if o.Str("a") != "x" || o.Str("n") != "" || o.Str("b") != "" || o.Str("missing") != "" {
		t.Errorf("Str: %q %q %q", o.Str("a"), o.Str("n"), o.Str("b"))
	}
	// Numbers survive a round trip as written.
	if got := string(o.JSON()); got != `{"a":" x ","b":true,"f":1.50,"n":12345678901234567890}` {
		t.Errorf("JSON = %s", got)
	}
}

func fieldErrors(resp *pluginv1.ValidateCredentialsResponse) []string {
	var out []string
	for _, e := range resp.GetErrors() {
		out = append(out, e.GetField()+":"+e.GetCode())
	}
	return out
}

func TestForm(t *testing.T) {
	for _, in := range []*pluginv1.ValidateCredentialsRequest{
		{CredentialsJson: "{", SettingsJson: "{}"},
		{CredentialsJson: "{}", SettingsJson: "[]"},
	} {
		form, refused := DecodeForm(in)
		if form != nil || !slices.Equal(fieldErrors(refused), []string{":invalid"}) {
			t.Errorf("%v: form %v, errors %v", in, form, fieldErrors(refused))
		}
	}

	form, refused := DecodeForm(&pluginv1.ValidateCredentialsRequest{CredentialsJson: `{ "k": "v", "n": 1.0 }`})
	if refused != nil {
		t.Fatalf("refused: %v", fieldErrors(refused))
	}
	// Nothing changed: the core keeps what was submitted, whatever its spacing.
	if resp := form.Normalized(); resp.GetNormalizedCredentialsJson() != "" || resp.GetNormalizedSettingsJson() != "" || len(resp.GetErrors()) != 0 {
		t.Errorf("unchanged form = %v", resp)
	}
	form.Settings["region"] = "us-east-1"
	if resp := form.Normalized(); resp.GetNormalizedCredentialsJson() != "" || resp.GetNormalizedSettingsJson() != `{"region":"us-east-1"}` {
		t.Errorf("changed settings = %v", resp)
	}
	delete(form.Creds, "k")
	if resp := form.Normalized(); resp.GetNormalizedCredentialsJson() != `{"n":1.0}` {
		t.Errorf("changed credentials = %v", resp)
	}
}

func TestDecodeAccount(t *testing.T) {
	creds, settings, err := DecodeAccount(&pluginv1.Account{Id: 7, CredentialsJson: `{"api_key":"k"}`})
	if err != nil || creds.Str("api_key") != "k" || settings == nil || len(settings) != 0 {
		t.Fatalf("%v %v %v", creds, settings, err)
	}
	for _, acc := range []*pluginv1.Account{
		{Id: 7, CredentialsJson: "{"},
		{Id: 7, CredentialsJson: "{}", SettingsJson: "[1]"},
	} {
		if _, _, err := DecodeAccount(acc); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%v: %v", acc, err)
		}
	}
}

// Only refuses keys outside the list in either object.
func TestOnly(t *testing.T) {
	f, bad := DecodeForm(&pluginv1.ValidateCredentialsRequest{
		CredentialsJson: `{"access_key_id":"A","token_for":"x"}`, SettingsJson: `{"region":"r","proxy":1}`})
	if bad != nil {
		t.Fatal(bad)
	}
	if resp := f.Only("access_key_id", "token_for", "region", "proxy"); resp != nil {
		t.Fatalf("all known = %v", resp)
	}
	resp := f.Only("access_key_id", "region")
	var got []string
	for _, e := range resp.GetErrors() {
		if e.GetCode() != "unknown" {
			t.Errorf("code = %q", e.GetCode())
		}
		got = append(got, e.GetField())
	}
	if !slices.Equal(got, []string{"proxy", "token_for"}) {
		t.Fatalf("unknown = %v", got)
	}
}
