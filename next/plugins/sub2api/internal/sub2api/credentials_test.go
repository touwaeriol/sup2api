package sub2api

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account form's rules (api_key a non-empty
// string; a required base_url, an http(s) URL without whitespace, query or
// fragment, saved as written; no other key) hold in ValidateCredentials,
// which is final: the core checks nothing else. The shared api_key +
// base_url cases and a /v1beta kept like a /v1.
func TestCredentialsRefused(t *testing.T) {
	form := apikeytest.Form{AccountType: "apikey", Key: "sk-abcdefgh", BaseURL: "https://site.example.com", RequireBaseURL: true}
	apikeytest.Run(t, New().ValidateCredentials, append(form.Cases(),
		apikeytest.Case{Name: "base_url with /v1beta saved as written", AccountType: "apikey", Creds: `{"api_key": "sk-abcdefgh", "base_url": "https://site.example.com/v1beta/"}`, Want: `{"api_key":"sk-abcdefgh","base_url":"https://site.example.com/v1beta"}`},
	))
}
