package install

import (
	"context"
	"fmt"
	"net/http"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// First-party plugins (next/plugins) are shipped in the image or in the
// official market. Their keys and the platform ids they declare are reserved
// (audit 2026-10-09 P1-5): a package uploaded by hand may use them only with
// an official signature, so nobody can take a key or platform a later image
// ships as built-in (the built-in install would fail on every node). Built-in
// installs are a trusted channel; a market install claims one only when its
// index lists the publisher as official (the version's trust level, CONTRACTS
// §136.2), like any other package. A platform or
// notification channel id is reserved for its own plugin whatever the source:
// the channel's provider reads every notification sent on it.
// TestReservedCoverRepo keeps the lists in step with next/plugins.
var (
	reservedKeys = map[string]bool{
		"anthropic": true, "azure_openai": true, "bedrock": true, "codex": true, "dynweight": true, "gemini": true, "guard": true,
		"mailer": true, "moderation": true, "newapi": true, "openai": true, "payment": true, "relay": true, "sub2api": true, "typesafe": true,
		"vertex": true,
	}
	// removedKeys maps the key of a first-party plugin the core no longer
	// supports to the release that removed it: no package of it installs,
	// whatever its signature or source, since the core side it needs is gone.
	removedKeys = map[string]string{"ccgateway": "0.1.290", "claude_oauth": "0.1.304", "custom": "0.1.301", "growth": "0.1.304", "volcengine": "0.1.304"}
	// reservedPlatforms maps a platform id to the plugin it belongs to.
	reservedPlatforms = map[string]string{"typesafe": "typesafe"}
	// reservedChannels maps a notification channel id to the plugin it
	// belongs to.
	reservedChannels = map[string]string{"email": "mailer"}
)

// reservedUse is the first platform or notification channel of m reserved
// for another plugin: its field path and a message, or "" when none.
func reservedUse(m *manifest.Manifest) (field, msg string) {
	for i, p := range m.Platforms {
		if owner, ok := reservedPlatforms[p.ID]; ok && owner != m.Key {
			return fmt.Sprintf("platforms[%d].id", i), fmt.Sprintf("platform id %q is reserved for the first-party plugin %q", p.ID, owner)
		}
	}
	if m.Notifications == nil {
		return "", ""
	}
	for i, c := range m.Notifications.Channels {
		if owner, ok := reservedChannels[c.ID]; ok && owner != m.Key {
			return fmt.Sprintf("notifications.channels[%d].id", i), fmt.Sprintf("notification channel %q is reserved for the first-party plugin %q", c.ID, owner)
		}
	}
	return "", ""
}

// ErrReservedKey: a package neither official nor built in claims the key of
// a first-party plugin, or a package uses a platform or notification channel
// id reserved for another plugin.
var ErrReservedKey = core.NewError(http.StatusForbidden, "plugin_key_reserved", "the plugin key, platform or notification channel id is reserved")

// ErrResourceConflict: a version declares a platform, gateway endpoint or
// notification channel another approved plugin holds, a built-in platform id or a path the core
// reserves (details.fields, codes as check.Conflicts).
var ErrResourceConflict = core.NewError(http.StatusConflict, "plugin_resource_conflict",
	"another plugin or the core already holds a platform or endpoint this version declares")

// CheckResources checks that version of key can hold the exclusive resources
// it declares now: none held by another approved plugin, by the core's
// built-in platforms or by its reserved paths (audit 2026-10-09 P2-2). Not
// only at upload: approval, enable and upgrade call it, since another plugin
// may have been approved and the core upgraded since.
func (s *Service) CheckResources(ctx context.Context, key, version string) error {
	m, err := LoadManifest(ctx, s.d.DB.Gorm, key, version)
	if err != nil {
		return err
	}
	return s.checkResources(ctx, s.d.DB.Gorm, m)
}

func (s *Service) checkResources(ctx context.Context, q *gorm.DB, m *manifest.Manifest) error {
	others, endpoints, channels, err := s.otherResources(ctx, q, m.Key)
	if err != nil {
		return err
	}
	if conflicts := pkg.Conflicts(m, others, endpoints, channels); len(conflicts) > 0 {
		return ErrResourceConflict.WithDetails(map[string]any{"fields": conflicts})
	}
	if field, msg := reservedUse(m); field != "" {
		return ErrResourceConflict.WithDetails(map[string]any{"fields": []core.FieldError{{Field: field, Code: "reserved", Message: msg}}})
	}
	return nil
}

// checkReserved applies the reservations to a package from source with the
// given trust level. claim is true when the key is not installed yet: only a
// new claim of a reserved key needs a trusted channel (an installed plugin
// keeps its publisher check). A removed key is refused from any source.
func checkReserved(m *manifest.Manifest, source, trust string, claim bool) error {
	if release, ok := removedKeys[m.Key]; ok {
		return ErrReservedKey.WithMessage(fmt.Sprintf("plugin %q was removed in core %s and cannot be installed (see CHANGELOG %s)", m.Key, release, release))
	}
	if field, msg := reservedUse(m); field != "" {
		return ErrReservedKey.WithMessage(msg)
	}
	if !claim || !reservedKeys[m.Key] || trust == pkg.TrustOfficial || source == "builtin" {
		return nil
	}
	return ErrReservedKey.WithMessage(fmt.Sprintf("plugin key %q is reserved for a first-party plugin: only an official package (signed, or listed by a market under an official publisher) may claim it", m.Key))
}
