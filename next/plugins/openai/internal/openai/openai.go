// Package openai is the OpenAI API key platform: the openai preset with the
// account's organization and project, forwarding every request field.
package openai

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// TypeAPIKey is the account type id (accountTypes[].id of manifest.json).
const TypeAPIKey = "apikey"

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(APIKey()) }

// visible is a string of at most n visible ASCII characters ([!-~]),
// empty allowed.
func visible(n int) fieldcheck.Field { return fieldcheck.String().MaxLen(n).Pattern(`^[!-~]*$`) }

// APIKey is the apikey type: the preset (the key, the base URL), plus the
// rest of the account form's rules: the organization and project an account
// may set, at most 128 visible ASCII characters each, which replace the
// client's headers as they are. No other key is accepted.
func APIKey() standard.Type {
	t := openai.APIKey(TypeAPIKey)
	t.Spec.Fields = fieldcheck.Fields{"organization": visible(128), "project": visible(128)}
	t.Headers = append(t.Headers,
		standard.Set("openai-organization", standard.Field("organization")),
		standard.Set("openai-project", standard.Field("project")))
	return t
}
