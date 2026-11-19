package relay

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account form's rules (api_key a non-empty
// string; a required base_url, an http(s) URL without whitespace, query or
// fragment, saved as written; no other key) hold in ValidateCredentials,
// which is final: the core checks nothing else. The shared api_key +
// base_url cases.
func TestCredentialsRefused(t *testing.T) {
	form := apikeytest.Form{AccountType: "relay_key", Key: "sk-relay-abcdef", BaseURL: "https://site.example.com", RequireBaseURL: true}
	apikeytest.Run(t, New().ValidateCredentials, form.Cases())
}
