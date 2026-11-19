// Command azure-openai is the sub2api-next Azure OpenAI plugin: account
// types for the built-in openai platform on an Azure OpenAI resource.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/azure-openai/internal/azureopenai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(azureopenai.New(), pluginsdk.WithManifest(manifestJSON))
}
