package bedrock

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// validate calls ValidateCredentials and decodes the settings the core would
// store.
func validate(t *testing.T, typ string, creds, settings map[string]any) (*pluginv1.ValidateCredentialsResponse, map[string]any) {
	t.Helper()
	resp, err := newTestPlugin(Endpoints{}).ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{
		AccountType: typ, CredentialsJson: mustJSON(creds), SettingsJson: mustJSON(settings),
	})
	if err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
	if resp.GetNormalizedCredentialsJson() != "" {
		t.Fatalf("credentials are never rewritten, got %d bytes", len(resp.GetNormalizedCredentialsJson()))
	}
	return resp, normalized(t, resp.GetNormalizedSettingsJson(), settings)
}

// with is base with over on top; a nil value removes the key.
func with(base, over map[string]any) map[string]any {
	m := maps.Clone(base)
	for k, v := range over {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	return m
}

func TestValidateCredentialsAccepts(t *testing.T) {
	for typ, creds := range typeCreds {
		t.Run(typ, func(t *testing.T) {
			// Nothing but the credentials: the defaults are stored.
			resp, settings := validate(t, typ, creds, nil)
			if len(resp.GetErrors()) != 0 {
				t.Fatalf("errors = %v", fieldErrors(resp))
			}
			if settings[fieldRegion] != DefaultRegion || settings[fieldProfilePrefix] != DefaultProfilePrefix || DefaultProfilePrefix != "global" || len(settings) != 2 {
				t.Fatalf("stored settings = %v", settings)
			}
			// Complete settings are kept as submitted.
			for _, prefix := range ProfilePrefixes {
				resp, _ := validate(t, typ, creds, settingsOf("eu-central-1", prefix))
				if len(resp.GetErrors()) != 0 || resp.GetNormalizedSettingsJson() != "" {
					t.Fatalf("prefix %q: errors %v, normalized %q", prefix, fieldErrors(resp), resp.GetNormalizedSettingsJson())
				}
			}
		})
	}
	// Any non-blank API key is accepted (DESIGN-2026-11-20 §5.1), and the
	// request carries it trimmed.
	for name, key := range map[string]string{
		"1 character":        "k",
		"4096 characters":    strings.Repeat("k", 4096),
		"inner space":        "ABSK key with spaces",
		"not ASCII":          "ABSK-ключ",
		"surrounding spaces": "  " + testAPIKey + "\n",
	} {
		creds := map[string]any{fieldAPIKey: key}
		if resp, _ := validate(t, TypeAPIKey, creds, nil); len(resp.GetErrors()) != 0 {
			t.Fatalf("api key %s: errors = %v", name, fieldErrors(resp))
		}
		up := build(t, executeRequest(stored(TypeAPIKey, creds, nil), testHaiku, false, claudeBody(testHaiku, false)))
		if got := up.GetHeaders()["authorization"]; got != "Bearer "+strings.TrimSpace(key) {
			t.Fatalf("api key %s: Authorization = %q", name, got)
		}
	}
	// The expiry hint of temporary credentials is a date and time.
	resp, settings := validate(t, TypeSTS, stsCreds, map[string]any{fieldRegion: "us-west-2", fieldSessionExpiresAt: "2026-11-18T08:30:00.000Z"})
	if len(resp.GetErrors()) != 0 || settings[fieldSessionExpiresAt] != "2026-11-18T08:30:00.000Z" {
		t.Fatalf("session_expires_at: %v %v", fieldErrors(resp), settings)
	}
}

func TestValidateCredentialsRejects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ      string
		creds    map[string]any
		settings map[string]any
		want     string
	}{
		{"unknown type", "bedrock_role", apiKeyCreds, nil, "account_type:unsupported"},

		{"api key missing", TypeAPIKey, map[string]any{}, nil, "api_key:required"},
		{"api key blank", TypeAPIKey, map[string]any{fieldAPIKey: "   "}, nil, "api_key:required"},
		{"api key not a string", TypeAPIKey, map[string]any{fieldAPIKey: 12345678}, nil, "api_key:required"},
		{"api key type given an access key ID: a key its form does not send", TypeAPIKey, map[string]any{fieldAccessKeyID: testAccessKeyID}, nil, "access_key_id:unknown"},

		{"access key id missing", TypeAccessKey, with(akskCreds, map[string]any{fieldAccessKeyID: nil}), nil, "access_key_id:required"},
		{"access key id lower case", TypeAccessKey, with(akskCreds, map[string]any{fieldAccessKeyID: "akiaiosfodnn7example"}), nil, "access_key_id:pattern"},
		{"access key id too short", TypeAccessKey, with(akskCreds, map[string]any{fieldAccessKeyID: "AKIA123"}), nil, "access_key_id:pattern"},
		{"secret missing", TypeAccessKey, with(akskCreds, map[string]any{fieldSecretAccessKey: nil}), nil, "secret_access_key:required"},
		{"secret with a line break", TypeAccessKey, with(akskCreds, map[string]any{fieldSecretAccessKey: "wJalr\nXUtnFEMI"}), nil, "secret_access_key:pattern"},
		{"access key type given an api key: a key its form does not send", TypeAccessKey, apiKeyCreds, nil, "api_key:unknown"},

		{"sts access key id missing", TypeSTS, with(stsCreds, map[string]any{fieldAccessKeyID: nil}), nil, "access_key_id:required"},
		{"sts secret missing", TypeSTS, with(stsCreds, map[string]any{fieldSecretAccessKey: nil}), nil, "secret_access_key:required"},
		{"sts session token missing", TypeSTS, akskCreds, nil, "session_token:required"},
		{"sts session token with a space", TypeSTS, with(stsCreds, map[string]any{fieldSessionToken: "IQoJ b3Jp"}), nil, "session_token:pattern"},
		{"sts expiry not a time", TypeSTS, stsCreds, map[string]any{fieldSessionExpiresAt: "tomorrow"}, "session_expires_at:format"},

		{"region not a region", TypeAPIKey, apiKeyCreds, settingsOf("useast1", ""), "region:pattern"},
		{"region upper case", TypeAccessKey, akskCreds, settingsOf("US-EAST-1", ""), "region:pattern"},
		{"region smuggling a host", TypeSTS, stsCreds, settingsOf("us-east-1.evil.example", ""), "region:pattern"},
		{"region with a path", TypeAPIKey, apiKeyCreds, settingsOf("us-east-1/x", ""), "region:pattern"},
		{"China region", TypeAPIKey, apiKeyCreds, settingsOf("cn-north-1", ""), "region:unsupported"},
		{"China region, access key", TypeAccessKey, akskCreds, settingsOf("cn-northwest-1", "global"), "region:unsupported"},
		{"isolated region", TypeAPIKey, apiKeyCreds, settingsOf("us-iso-east-1", ""), "region:unsupported"},
		{"isolated region b, access key", TypeAccessKey, akskCreds, settingsOf("us-isob-east-1", "us"), "region:unsupported"},
		{"isolated region f, temporary credentials", TypeSTS, stsCreds, settingsOf("us-isof-south-1", ""), "region:unsupported"},
		{"isolated region in Europe", TypeAPIKey, apiKeyCreds, settingsOf("eu-isoe-west-1", ""), "region:unsupported"},
		{"European sovereign cloud region", TypeAPIKey, apiKeyCreds, settingsOf("eusc-de-east-1", ""), "region:pattern"},
		{"unknown prefix", TypeAPIKey, apiKeyCreds, settingsOf("us-east-1", "cn"), "inference_profile_prefix:enum"},
		{"prefix with the dot", TypeAccessKey, akskCreds, settingsOf("us-east-1", "us."), "inference_profile_prefix:enum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := validate(t, tc.typ, tc.creds, tc.settings)
			if got := fieldErrors(resp); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("errors = %v, want %s", got, tc.want)
			}
			if resp.GetNormalizedSettingsJson() != "" {
				t.Fatalf("a refused account is not normalized: %q", resp.GetNormalizedSettingsJson())
			}
			if tc.want == "session_expires_at:format" {
				return // the hint is never read once the account is stored
			}
			// The same account, had it been stored, serves no request.
			_, err := newTestPlugin(Endpoints{}).BuildUpstream(context.Background(),
				executeRequest(stored(tc.typ, tc.creds, tc.settings), testHaiku, false, claudeBody(testHaiku, false)))
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("BuildUpstream for the refused account: %v", err)
			}
		})
	}
}

func TestValidateCredentialsMalformedJSON(t *testing.T) {
	p := newTestPlugin(Endpoints{})
	for _, in := range []*pluginv1.ValidateCredentialsRequest{
		{AccountType: TypeAPIKey, CredentialsJson: `[`, SettingsJson: `{}`},
		{AccountType: TypeAPIKey, CredentialsJson: mustJSON(apiKeyCreds), SettingsJson: `"x"`},
	} {
		resp, err := p.ValidateCredentials(context.Background(), in)
		if err != nil || !slices.Equal(fieldErrors(resp), []string{":invalid"}) {
			t.Fatalf("%v %v", fieldErrors(resp), err)
		}
	}
}

// The partitions whose endpoints are not under amazonaws.com (China and the
// isolated ones) are refused with one message in both languages; GovCloud,
// which is served under amazonaws.com like the commercial regions, is not.
func TestPartitions(t *testing.T) {
	for _, region := range []string{"cn-north-1", "cn-northwest-1", "us-iso-east-1", "us-iso-west-1", "us-isob-east-1", "us-isof-east-1", "eu-isoe-west-1"} {
		if !unsupportedPartition(region) {
			t.Errorf("%s is not refused", region)
		}
		resp, _ := validate(t, TypeAPIKey, apiKeyCreds, settingsOf(region, ""))
		if len(resp.GetErrors()) != 1 {
			t.Fatalf("%s: errors = %v", region, fieldErrors(resp))
		}
		msg := resp.GetErrors()[0].GetMessage()
		for _, part := range []string{"China regions (cn-*) are not supported", "isolated regions (us-iso-*, us-isob-*", "不支持 AWS 中国区（cn-*）和隔离分区"} {
			if !strings.Contains(msg, part) {
				t.Errorf("%s: message = %q, want %q in it", region, msg, part)
			}
		}
	}
	for _, region := range []string{"us-gov-west-1", "us-gov-east-1", "ca-central-1", "us-east-1", "il-central-1", "ap-southeast-7"} {
		if unsupportedPartition(region) {
			t.Errorf("%s is refused", region)
		}
	}
	for _, region := range []string{"us-gov-west-1", "us-gov-east-1", "ca-central-1"} {
		if resp, _ := validate(t, TypeAccessKey, akskCreds, settingsOf(region, "")); len(resp.GetErrors()) != 0 {
			t.Errorf("%s: %v", region, fieldErrors(resp))
		}
		if RuntimeHost(region) != "bedrock-runtime."+region+".amazonaws.com" {
			t.Errorf("%s: host %s", region, RuntimeHost(region))
		}
	}
}

// The expiry hint of temporary credentials takes no part in serving requests.
func TestExpiryHintIsOnlyAHint(t *testing.T) {
	for _, hint := range []string{"tomorrow", "2020-01-01T00:00:00Z", "2999-01-01T00:00:00Z"} {
		acc := stored(TypeSTS, stsCreds, map[string]any{fieldRegion: "us-west-2", fieldSessionExpiresAt: hint})
		up := build(t, executeRequest(acc, testHaiku, false, claudeBody(testHaiku, false)))
		if up.GetHeaders()["x-amz-security-token"] != testSessionToken {
			t.Errorf("hint %q: the request is not signed with the session token", hint)
		}
	}
}

// Validation messages and build errors name fields, never values.
func TestErrorsCarryNoCredentials(t *testing.T) {
	secret := "wJalr XUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	creds := with(stsCreds, map[string]any{fieldSecretAccessKey: secret})
	resp, _ := validate(t, TypeSTS, creds, nil)
	if len(resp.GetErrors()) != 1 || strings.Contains(resp.GetErrors()[0].GetMessage(), "wJalr") {
		t.Fatalf("validation error: %v", resp.GetErrors())
	}
	_, err := newTestPlugin(Endpoints{}).BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: stored(TypeSTS, creds, nil)})
	if err == nil || strings.Contains(err.Error(), "wJalr") || strings.Contains(err.Error(), testSessionToken) {
		t.Fatalf("build error: %v", err)
	}
}
