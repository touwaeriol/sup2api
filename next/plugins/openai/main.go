// Command openai is the sub2api-next OpenAI plugin: the API key account type
// of the built-in openai platform.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/openai/internal/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(openai.New(), pluginsdk.WithManifest(manifestJSON))
}
