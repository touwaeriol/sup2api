package bedrock

import (
	"slices"
	"strings"
	"testing"
)

// TestCredentialConstraintsRefused: for every constraint ValidateCredentials
// puts on a field, an input breaking it, written out here, is refused at that
// field, and a key the type's form does not send is refused with code
// "unknown". The core checks nothing else on save. The api_key takes any
// non-blank string (DESIGN-2026-11-20 §5.1); TestValidateCredentialsAccepts
// checks such keys are accepted.
func TestCredentialConstraintsRefused(t *testing.T) {
	type input struct {
		name            string
		creds, settings map[string]any
		field           string
	}
	// Constraints of the credential fields shared by aksk and sts.
	accessKey := func(base map[string]any) []input {
		return []input{
			{"access_key_id required", with(base, map[string]any{fieldAccessKeyID: nil}), nil, fieldAccessKeyID},
			{"access_key_id not a string", with(base, map[string]any{fieldAccessKeyID: 1234567890123456}), nil, fieldAccessKeyID},
			{"access_key_id in lower case", with(base, map[string]any{fieldAccessKeyID: "akiaiosfodnn7example"}), nil, fieldAccessKeyID},
			{"access_key_id of 15 characters", with(base, map[string]any{fieldAccessKeyID: "AKIA12345678901"}), nil, fieldAccessKeyID},
			{"access_key_id of 129 characters", with(base, map[string]any{fieldAccessKeyID: strings.Repeat("A", 129)}), nil, fieldAccessKeyID},
			{"secret_access_key required", with(base, map[string]any{fieldSecretAccessKey: nil}), nil, fieldSecretAccessKey},
			{"secret_access_key empty", with(base, map[string]any{fieldSecretAccessKey: ""}), nil, fieldSecretAccessKey},
			{"secret_access_key over 1024 characters", with(base, map[string]any{fieldSecretAccessKey: strings.Repeat("s", 1025)}), nil, fieldSecretAccessKey},
			{"secret_access_key with a space", with(base, map[string]any{fieldSecretAccessKey: "wJalr XUtn"}), nil, fieldSecretAccessKey},
			{"secret_access_key with non-ASCII", with(base, map[string]any{fieldSecretAccessKey: "wJalrXUtné"}), nil, fieldSecretAccessKey},
		}
	}
	// Constraints of the settings every type shares.
	settings := func(creds map[string]any) []input {
		return []input{
			{"region in upper case", creds, settingsOf("US-EAST-1", ""), fieldRegion},
			{"region without a number", creds, settingsOf("us-east", ""), fieldRegion},
			{"region is a host", creds, settingsOf("us-east-1.example.com", ""), fieldRegion},
			{"inference_profile_prefix not listed", creds, settingsOf("us-east-1", "cn"), fieldProfilePrefix},
			{"inference_profile_prefix with the dot", creds, settingsOf("us-east-1", "global."), fieldProfilePrefix},
			{"unknown key (credentials)", with(creds, map[string]any{"legacy": "x"}), nil, "legacy"},
			{"unknown key (settings)", creds, map[string]any{"legacy": "x"}, "legacy"},
		}
	}
	cases := map[string][]input{
		TypeAPIKey: append([]input{
			{"api_key required", map[string]any{}, nil, fieldAPIKey},
			{"api_key not a string", map[string]any{fieldAPIKey: 123456789}, nil, fieldAPIKey},
		}, settings(apiKeyCreds)...),
		TypeAccessKey: append(accessKey(akskCreds), settings(akskCreds)...),
		TypeSTS: append(append(accessKey(stsCreds),
			input{"session_token required", with(stsCreds, map[string]any{fieldSessionToken: nil}), nil, fieldSessionToken},
			input{"session_token empty", with(stsCreds, map[string]any{fieldSessionToken: ""}), nil, fieldSessionToken},
			input{"session_token over 8192 characters", with(stsCreds, map[string]any{fieldSessionToken: strings.Repeat("t", 8193)}), nil, fieldSessionToken},
			input{"session_token with a line break", with(stsCreds, map[string]any{fieldSessionToken: "IQoJ\nb3Jp"}), nil, fieldSessionToken},
			input{"session_expires_at not a date and time", stsCreds, map[string]any{fieldSessionExpiresAt: "2026-11-20 10:30"}, fieldSessionExpiresAt},
		), settings(stsCreds)...),
	}
	for typ, inputs := range cases {
		for _, in := range inputs {
			t.Run(typ+"/"+in.name, func(t *testing.T) {
				resp, _ := validate(t, typ, in.creds, in.settings)
				got := fieldErrors(resp)
				if len(got) != 1 || !strings.HasPrefix(got[0], in.field+":") || (in.field == "legacy") != (got[0] == "legacy:unknown") {
					t.Fatalf("errors = %v, want one at %s", got, in.field)
				}
			})
		}
	}
}

// TestFormKeysAccepted: every key the form of a type sends, all submitted
// at once (credentials and settings split as the manifest's settingsFields
// do), passes the unknown-key check; a key only another type's form sends
// does not.
func TestFormKeysAccepted(t *testing.T) {
	settings := map[string]any{fieldRegion: "us-east-1", fieldProfilePrefix: "global"}
	cases := map[string]struct{ creds, settings map[string]any }{
		TypeAPIKey:    {apiKeyCreds, settings},
		TypeAccessKey: {akskCreds, settings},
		TypeSTS:       {stsCreds, with(settings, map[string]any{fieldSessionExpiresAt: "2026-11-20T10:30:00Z"})},
	}
	for typ, c := range cases {
		t.Run(typ, func(t *testing.T) {
			if resp, _ := validate(t, typ, c.creds, c.settings); len(resp.GetErrors()) != 0 {
				t.Fatalf("errors = %v, want none", fieldErrors(resp))
			}
		})
	}
	// A key only another type's form sends.
	for _, c := range []struct {
		typ   string
		creds map[string]any
		key   string
	}{
		{TypeAPIKey, with(apiKeyCreds, map[string]any{fieldSessionToken: testSessionToken}), fieldSessionToken},
		{TypeAccessKey, with(akskCreds, map[string]any{fieldSessionToken: testSessionToken}), fieldSessionToken},
		{TypeSTS, with(stsCreds, map[string]any{fieldAPIKey: testAPIKey}), fieldAPIKey},
	} {
		if resp, _ := validate(t, c.typ, c.creds, settings); !slices.Equal(fieldErrors(resp), []string{c.key + ":unknown"}) {
			t.Errorf("%s with %s: errors = %v", c.typ, c.key, fieldErrors(resp))
		}
	}
}
