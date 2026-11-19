// Command sub2api is the sub2api-next Sub2API plugin: the apikey account
// type of a Sub2API site (legacy or next) on the built-in openai,
// anthropic and gemini platforms, with its balance.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/sub2api/internal/sub2api"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(sub2api.New(), pluginsdk.WithManifest(manifestJSON))
}
