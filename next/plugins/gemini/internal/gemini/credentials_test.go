package gemini

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account form's rules (api_key a non-empty
// string; an optional base_url, an http(s) URL without whitespace, query or
// fragment, saved as written; no other key) hold in ValidateCredentials,
// which is final: the core checks nothing else. The shared api_key +
// base_url cases and a /v1beta kept like a /v1.
func TestCredentialsRefused(t *testing.T) {
	form := apikeytest.Form{AccountType: "apikey", Key: "AIzaSyAbcdefgh", BaseURL: "https://x.example.com"}
	apikeytest.Run(t, New().ValidateCredentials, append(form.Cases(),
		apikeytest.Case{Name: "base_url with /v1beta saved as written", AccountType: "apikey", Creds: `{"api_key": "AIzaSyAbcdefgh", "base_url": "https://x.example.com/v1beta/"}`, Want: `{"api_key":"AIzaSyAbcdefgh","base_url":"https://x.example.com/v1beta"}`},
	))
}
