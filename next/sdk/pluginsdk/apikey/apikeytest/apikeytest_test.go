package apikeytest_test

import (
	"context"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

func validate(s apikey.Spec) func(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return func(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
		return s.Validate(in), nil
	}
}

// The shared cases hold for the SDK's own api_key + base_url check, with
// base_url optional or required, with fields of the type's own.
func TestCases(t *testing.T) {
	optional := apikeytest.Form{AccountType: "apikey", Key: "sk-abcdefgh", BaseURL: "https://x.example.com/a"}
	apikeytest.Run(t, validate(apikey.Spec{AccountType: "apikey", DefaultBaseURL: "https://api.example.com", Versions: []string{"/v1"}}), optional.Cases())

	required := apikeytest.Form{AccountType: "relay", Key: "sk-abcdefgh", BaseURL: "http://relay.example.com", RequireBaseURL: true, Fill: map[string]any{"api_version": "2024-10-21"}}
	spec := apikey.Spec{AccountType: "relay", RequireBaseURL: true, Fields: fieldcheck.Fields{"api_version": fieldcheck.String().Required()}}
	apikeytest.Run(t, validate(spec), append(required.Cases(),
		apikeytest.Case{Name: "api_version missing", AccountType: "relay", Creds: `{"api_key":"sk-abcdefgh","base_url":"http://relay.example.com"}`, Field: "api_version", Code: "required"},
	))
}

// GuardedVersions compares the versions of each guarded base_url, and only
// those, with Spec.Versions.
func TestGuardedVersions(t *testing.T) {
	spec := apikey.Spec{AccountType: "apikey", Versions: []string{"/v1beta", "/v1"}}
	guarded := func(field string, versions ...string) manifest.AccountType {
		return manifest.AccountType{ID: "apikey", GuardedSettings: []manifest.GuardedSetting{{Field: field, Allowed: []string{"https://x.example.com"}, Versions: versions}}}
	}
	cases := []struct {
		name string
		at   manifest.AccountType
		spec apikey.Spec
		ok   bool
	}{
		{"same versions", guarded("base_url", "/v1beta", "/v1"), spec, true},
		{"other order", guarded("base_url", "/v1", "/v1beta"), spec, false},
		{"missing versions", guarded("base_url"), spec, false},
		{"no Spec.Versions", guarded("base_url"), apikey.Spec{Versions: []string{}}, true},
		{"versions without Spec.Versions", guarded("base_url", "/v1"), apikey.Spec{}, false},
		{"no guarded base_url", guarded("region"), spec, true},
		{"no guarded settings", manifest.AccountType{ID: "relay"}, spec, true},
	}
	for _, c := range cases {
		rec := &recorder{TB: t}
		apikeytest.GuardedVersions(rec, c.at, c.spec)
		if rec.failed == c.ok {
			t.Errorf("%s: failed = %v, want %v", c.name, rec.failed, !c.ok)
		}
	}
}

// recorder is a testing.TB that records a failure instead of reporting it.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }
