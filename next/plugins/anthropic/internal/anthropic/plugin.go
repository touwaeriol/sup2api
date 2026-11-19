// Package anthropic implements the Anthropic plugin: the platform of its
// apikey account type (the anthropic preset of sdk/pluginsdk/standard) and
// the model catalog admin route. The anthropic platform and its endpoints
// are built into the core (sdk/platforms); this plugin declares none.
package anthropic

import (
	"context"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// Plugin is the anthropic plugin: the apikey platform (pluginsdk.Platform),
// the model catalog route (pluginsdk.HTTP) and pluginsdk.Initializer.
type Plugin struct {
	*pluginsdk.Router
	*standard.Platform
	host pluginsdk.Host
}

// New returns the plugin.
func New() *Plugin {
	p := &Plugin{Router: pluginsdk.NewRouter(), Platform: standard.New(APIKey())}
	p.Handle("GET", "/models", p.listModels)
	return p
}

// APIKey is the apikey type: the anthropic preset (every request field is
// forwarded), its credentials api_key and base_url only.
func APIKey() standard.Type { return anthropic.APIKey("apikey") }

// Init implements pluginsdk.Initializer.
func (p *Plugin) Init(_ context.Context, h pluginsdk.Host) error {
	p.host = h
	return nil
}
