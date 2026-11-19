// Package gemini is the Gemini API key platform: the gemini preset as it is
// (x-goog-api-key on https://generativelanguage.googleapis.com, the model of
// the request in the path, every request field forwarded).
package gemini

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// TypeAPIKey is the plugin's one account type (manifest accountTypes[].id).
const TypeAPIKey = "apikey"

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(APIKey()) }

// APIKey is the apikey type: the gemini preset, its credentials api_key and
// base_url only.
func APIKey() standard.Type { return gemini.APIKey(TypeAPIKey) }
