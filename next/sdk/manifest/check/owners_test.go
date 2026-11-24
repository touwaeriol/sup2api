package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Owners are keyed by plugin id, not by bare key: a third-party plugin
// keyed like an installed official one does not own that plugin's platform,
// endpoints or notification channels.
func TestOwnersByPluginID(t *testing.T) {
	channels := &manifest.Notifications{Channels: []manifest.NotificationChannel{{ID: "relay", Label: manifest.LocalizedText{"en": "Relay"}}}}
	official := controlsManifest(nil)
	official.Key, official.Publisher, official.Notifications = "codex", manifest.OfficialPublisher, channels
	pfs, eps := OthersFromManifests([]*manifest.Manifest{official})
	chs := ChannelsFromManifests([]*manifest.Manifest{official})
	if pfs[0].PluginID != "codex" || eps[0].PluginID != "codex" || chs[0].PluginID != "codex" {
		t.Fatalf("owners = %+v %+v %+v", pfs, eps, chs)
	}
	opt := ValidateOptions{Tooling: true, OtherPlatforms: pfs, OtherEndpoints: eps, OtherChannels: chs}

	third := controlsManifest(nil)
	third.Key, third.Publisher, third.Notifications = "codex", "erio", channels
	third.Capabilities = append(third.Capabilities, manifest.Capability{ID: manifest.CapNotifyChannel})
	err := Validate(third, map[string][]byte{}, opt)
	for field, code := range map[string]string{
		"platforms[0].id":                "platform_conflict",
		"platforms[0].endpoints[0].path": "endpoint_conflict",
		"notifications.channels[0].id":   "channel_conflict",
	} {
		if !hasCode(err, field, code) {
			t.Errorf("erio/codex: no %s %s in %v", field, code, err)
		}
	}
	// The official plugin's own resources are its own (an upgrade).
	official.Capabilities = third.Capabilities
	err = Validate(official, map[string][]byte{}, opt)
	for _, field := range []string{"platforms[0].id", "platforms[0].endpoints[0].path", "notifications.channels[0].id"} {
		if f, _ := Fields(err); hasAnyCode(f, field) {
			t.Errorf("codex upgrade: %s reported in %v", field, err)
		}
	}
}

func hasAnyCode(fields []FieldError, field string) bool {
	for _, f := range fields {
		if f.Field == field {
			return true
		}
	}
	return false
}
