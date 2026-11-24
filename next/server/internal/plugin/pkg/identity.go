package pkg

import "github.com/Sub2API-Devs/sup2api/next/sdk/manifest"

// Identity is what a response describing a plugin says of its plugin id
// (manifest.PluginID, CONTRACTS §137) besides the id itself: the publisher
// it names and the form people see ("codex", "erio/codex").
type Identity struct {
	Publisher string `json:"publisher"`
	DisplayID string `json:"display_id"`
}

// IdentityOf is the Identity of plugin id.
func IdentityOf(id string) Identity {
	publisher, _, _ := manifest.ParsePluginID(id)
	return Identity{Publisher: publisher, DisplayID: manifest.DisplayID(id)}
}
