// Command typesafe is the sub2api-next TypeSafe plugin: the typesafe
// platform (POST /v1/systemone) and its API key account type.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/typesafe/internal/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(typesafe.New(), pluginsdk.WithManifest(manifestJSON))
}
