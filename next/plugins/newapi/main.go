// Command newapi is the sub2api-next New API plugin: the apikey account
// type of a New API site on the built-in openai, anthropic and gemini
// platforms and the TypeSafe plugin's typesafe platform, with its balance.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/newapi/internal/newapi"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(newapi.New(), pluginsdk.WithManifest(manifestJSON))
}
