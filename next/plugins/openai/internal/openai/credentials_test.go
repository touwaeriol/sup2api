package openai

import (
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account form's rules (api_key a non-empty
// string; base_url an http(s) URL without whitespace, query or fragment,
// saved as written; organization and project strings
// of at most 128 characters of [!-~]; no other key) hold in
// ValidateCredentials, which is final: the core checks nothing else.
// The shared api_key + base_url cases, then the two headers'.
func TestCredentialsRefused(t *testing.T) {
	form := apikeytest.Form{AccountType: TypeAPIKey, Key: "sk-proj-abcdef", BaseURL: "https://x.example.com"}
	with := func(name, fields, field, code string) apikeytest.Case {
		return apikeytest.Case{Name: name, AccountType: TypeAPIKey, Creds: `{"api_key": "sk-proj-abcdef"` + fields + `}`, Field: field, Code: code}
	}
	cases := append(form.Cases(),
		with("organization and project of 128 characters", `, "organization": "`+strings.Repeat("o", 128)+`", "project": "`+strings.Repeat("p", 128)+`"`, "", ""),
		with("empty organization and project", `, "organization": "", "project": ""`, "", ""),
	)
	for _, k := range []string{"organization", "project"} {
		cases = append(cases,
			with(k+" not a string", `, "`+k+`": 1`, k, "type"),
			with(k+" null", `, "`+k+`": null`, k, "type"),
			with(k+" longer than 128", `, "`+k+`": "`+strings.Repeat("o", 129)+`"`, k, "maxLength"),
			with(k+" with a space", `, "`+k+`": "org abc"`, k, "pattern"),
			with(k+" not ASCII", `, "`+k+`": "org-组织"`, k, "pattern"),
		)
	}
	apikeytest.Run(t, New().ValidateCredentials, cases)
}
