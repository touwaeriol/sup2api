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
// official store under manifest.OfficialPublisher, whose plugin ids are
// their bare keys (CONTRACTS §137). That publisher is reserved: a package
// naming it installs only with official trust (an official signature, or
// the official store's index listing the publisher as official, the
// version's trust level, §136.2) or from the built-in channel, so nobody
// can take an id a later image ships as built-in (the built-in install
// would fail on every node). Any other publisher's ids start with "_" and
// never meet a first-party one. A platform or notification channel id is
// reserved for its own plugin whatever the source: the channel's provider
// reads every notification sent on it. TestReservedCoverRepo keeps the
// lists in step with next/plugins.
var (
	// removedKeys maps the plugin id of a first-party plugin the core no
	// longer supports to the release that removed it: no package of it
	// installs, whatever its signature or source, since the core side it
	// needs is gone.
	removedKeys = map[string]string{"ccgateway": "0.1.290", "claude_oauth": "0.1.304", "custom": "0.1.301", "growth": "0.1.304", "volcengine": "0.1.304"}
	// reservedPlatforms maps a platform id to the plugin id it belongs to.
	reservedPlatforms = map[string]string{"typesafe": "typesafe"}
	// reservedChannels maps a notification channel id to the plugin id it
	// belongs to.
	reservedChannels = map[string]string{"email": "mailer"}
)

// reservedUse is the first platform or notification channel of m reserved
// for another plugin: its field path and a message, or "" when none.
func reservedUse(m *manifest.Manifest) (field, msg string) {
	for i, p := range m.Platforms {
		if owner, ok := reservedPlatforms[p.ID]; ok && owner != m.ID() {
			return fmt.Sprintf("platforms[%d].id", i), fmt.Sprintf("platform id %q is reserved for the first-party plugin %q", p.ID, owner)
		}
	}
	if m.Notifications == nil {
		return "", ""
	}
	for i, c := range m.Notifications.Channels {
		if owner, ok := reservedChannels[c.ID]; ok && owner != m.ID() {
			return fmt.Sprintf("notifications.channels[%d].id", i), fmt.Sprintf("notification channel %q is reserved for the first-party plugin %q", c.ID, owner)
		}
	}
	return "", ""
}

// ErrReservedKey: a package of a removed first-party plugin, or one using a
// platform or notification channel id reserved for another plugin.
var ErrReservedKey = core.NewError(http.StatusForbidden, "plugin_key_reserved", "the plugin, platform or notification channel id is reserved")

// ErrReservedPublisher: a package neither official nor built in names the
// first-party publisher (manifest.OfficialPublisher).
var ErrReservedPublisher = core.NewError(http.StatusForbidden, "plugin_publisher_reserved", "the publisher is reserved for the first-party plugins")

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
	others, endpoints, channels, err := s.otherResources(ctx, q, m.ID())
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
// given trust level. A removed plugin is refused from any source; the
// first-party publisher needs official trust or the built-in channel,
// except in dev mode.
func checkReserved(m *manifest.Manifest, source, trust string, dev bool) error {
	if release, ok := removedKeys[m.ID()]; ok {
		return ErrReservedKey.WithMessage(fmt.Sprintf("plugin %q was removed in core %s and cannot be installed (see CHANGELOG %s)", manifest.DisplayID(m.ID()), release, release))
	}
	if field, msg := reservedUse(m); field != "" {
		return ErrReservedKey.WithMessage(msg)
	}
	if dev || m.Publisher != manifest.OfficialPublisher || trust == pkg.TrustOfficial || source == "builtin" {
		return nil
	}
	return ErrReservedPublisher.WithMessage(fmt.Sprintf("publisher %q is reserved for the first-party plugins: only an official package (signed by an official key, or listed by the official store under an official publisher) may name it", m.Publisher))
}
