package vertex

import (
	"strings"
	"testing"
)

// TestCredentialConstraintsRefused: for every constraint ValidateCredentials
// puts on a field, an input breaking it, written out here, is refused at that
// field (with the code given, where one is), and a key the type's form does
// not send is refused with code "unknown". The core checks nothing else on
// save. The api_key takes any non-blank string (DESIGN-2026-11-20 §5.1);
// TestValidateAPIKey checks such keys are accepted.
func TestCredentialConstraintsRefused(t *testing.T) {
	type input struct {
		name            string
		creds, settings map[string]any
		field, code     string
	}
	// The constraints of a key file type: the file, then project and
	// location, then unknown keys. fileOf writes a valid file, so only the
	// length refuses the long one.
	keyFile := func(field string, fileOf func(map[string]any) string) []input {
		file := fileOf(nil)
		long := fileOf(map[string]any{"padding": strings.Repeat("x", maxKeyFileLength)})
		return []input{
			{field + " required", map[string]any{}, nil, field, ""},
			{field + " not a string", map[string]any{field: 12}, nil, field, ""},
			{field + " of 1 character", map[string]any{field: "{"}, nil, field, ""},
			{field + " over 32768 characters", map[string]any{field: long}, nil, field, "too_long"},
			{"project_id malformed", map[string]any{field: file}, map[string]any{fieldProject: "Bad_Project"}, fieldProject, ""},
			{"project_id too short", map[string]any{field: file}, map[string]any{fieldProject: "abc"}, fieldProject, ""},
			{"location malformed", map[string]any{field: file}, map[string]any{fieldLocation: "US"}, fieldLocation, ""},
			{"location is a host", map[string]any{field: file}, map[string]any{fieldLocation: "evil.example/x"}, fieldLocation, ""},
			{"unknown credentials key", map[string]any{field: file, "legacy": "x"}, nil, "legacy", "unknown"},
			{"unknown settings key", map[string]any{field: file}, map[string]any{"legacy": "x"}, "legacy", "unknown"},
		}
	}
	const key = "AQ.Ab8RN6-example"
	cases := map[string][]input{
		TypeServiceAccount: keyFile("service_account_json", saJSON),
		TypeAuthorizedUser: keyFile("authorized_user_json", userJSON),
		TypeAPIKey: {
			{"api_key required", map[string]any{}, nil, fieldAPIKey, ""},
			{"api_key not a string", map[string]any{fieldAPIKey: 123456789}, nil, fieldAPIKey, ""},
			{"project_id malformed", map[string]any{fieldAPIKey: key}, map[string]any{fieldProject: "Bad_Project", fieldLocation: "us-central1"}, fieldProject, ""},
			{"location malformed", map[string]any{fieldAPIKey: key}, map[string]any{fieldProject: testProject, fieldLocation: "US"}, fieldLocation, ""},
			{"unknown credentials key", map[string]any{fieldAPIKey: key, "legacy": "x"}, nil, "legacy", "unknown"},
			{"unknown settings key", map[string]any{fieldAPIKey: key}, map[string]any{"legacy": "x"}, "legacy", "unknown"},
			{"base_url in the credentials (the type has none)", map[string]any{fieldAPIKey: key, "base_url": "https://evil.example"}, nil, "base_url", "unknown"},
			{"base_url in the settings (the type has none)", map[string]any{fieldAPIKey: key}, map[string]any{"base_url": "https://evil.example"}, "base_url", "unknown"},
		},
	}
	for typ, inputs := range cases {
		for _, in := range inputs {
			t.Run(typ+"/"+in.name, func(t *testing.T) {
				resp, _, _ := validate(t, typ, in.creds, in.settings)
				got := fieldErrors(resp)
				if len(got) != 1 || !strings.HasPrefix(got[0], in.field+":"+in.code) {
					t.Fatalf("errors = %v, want one at %s:%s", got, in.field, in.code)
				}
			})
		}
	}
}

// TestKeyFileFormKeepsTokenState: editing a key file account sends back the
// token state the core wrote on renewal (access_token, expires_at,
// token_for); the unknown-key check takes them.
func TestKeyFileFormKeepsTokenState(t *testing.T) {
	for typ, kind := range keyKinds {
		file := saJSON(nil)
		if typ == TypeAuthorizedUser {
			file = userJSON(nil)
		}
		t.Run(typ, func(t *testing.T) {
			creds := map[string]any{kind.field: file, fieldAccessToken: "ya29.token", fieldExpiresAt: 1800000000, fieldTokenFor: "fp"}
			resp, _, _ := validate(t, typ, creds, map[string]any{fieldProject: testProject, fieldLocation: LocationGlobal})
			if got := fieldErrors(resp); len(got) != 0 {
				t.Fatalf("errors = %v, want none", got)
			}
		})
	}
}
