package check

import (
	"fmt"
	"regexp"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// publisherIDRe is the rule of a publisher id: the manifest's publisher as
// a market index and the plugin registry name it (CONTRACTS §11.1). Like a
// plugin key it holds no "__" and does not end in "_", so that a plugin id
// (manifest.PluginID) splits back into its publisher and key.
var publisherIDRe = regexp.MustCompile(`^[a-z0-9](_?[a-z0-9])*$`)

// PublisherIDMaxLen is the longest publisher id: the one that still makes a
// plugin id of manifest.PluginIDMaxLen bytes with the shortest plugin key
// ("_" + publisher + "__" + two bytes).
const PublisherIDMaxLen = manifest.PluginIDMaxLen - len("_") - len("__") - minPluginKeyLen

// minPluginKeyLen is the shortest plugin key pluginKeyRe accepts.
const minPluginKeyLen = 2

// ValidatePublisherID reports why id cannot be a publisher id of a market
// index or the plugin registry (nil when it can). The registry tool and the
// core's market client share it.
func ValidatePublisherID(id string) error {
	if !publisherIDRe.MatchString(id) {
		return fmt.Errorf("publisher id %q must match %s", id, publisherIDRe)
	}
	if len(id) > PublisherIDMaxLen {
		return fmt.Errorf("publisher id %q is longer than %d bytes", id, PublisherIDMaxLen)
	}
	return nil
}

// ValidatePluginID reports why publisher's plugin key cannot be installed
// (nil when it can): both parts valid and the plugin id they make at most
// manifest.PluginIDMaxLen bytes.
func ValidatePluginID(publisher, key string) error {
	if err := ValidatePublisherID(publisher); err != nil {
		return err
	}
	if err := ValidatePluginKey(key); err != nil {
		return err
	}
	if id := manifest.PluginID(publisher, key); len(id) > manifest.PluginIDMaxLen {
		return fmt.Errorf("plugin id %q (publisher %q, key %q) is longer than %d bytes", id, publisher, key, manifest.PluginIDMaxLen)
	}
	return nil
}

// identity checks the plugin's key and publisher and the length of the
// plugin id they make (manifest.PluginID).
func (v *validator) identity() {
	m := v.m
	keyCode, keyMsg := pluginKeyProblem(m.Key)
	if keyCode != "" {
		v.add("key", keyCode, "%s", keyMsg)
	}
	if err := ValidatePublisherID(m.Publisher); err != nil {
		v.add("publisher", "invalid_format", "%s", err)
	} else if keyCode == "" && len(m.ID()) > manifest.PluginIDMaxLen {
		v.add("key", "too_long", "plugin id %q is longer than %d bytes: shorten the key or the publisher", m.ID(), manifest.PluginIDMaxLen)
	}
}
