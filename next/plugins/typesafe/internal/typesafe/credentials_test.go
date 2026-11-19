package typesafe

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account form's rules (api_key a non-empty
// string; an optional base_url, an http(s) URL without whitespace, query or
// fragment, saved as written; no other key) hold in ValidateCredentials,
// which is final: the core checks nothing else. The shared api_key +
// base_url cases.
func TestCredentialsRefused(t *testing.T) {
	form := apikeytest.Form{AccountType: "apikey", Key: "ts-abcdefgh", BaseURL: "https://x.example.com"}
	apikeytest.Run(t, New().ValidateCredentials, form.Cases())
}
