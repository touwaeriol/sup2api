package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// The ui.settings of the first-party plugins whose settings moved from JSON
// Schema forms to native components (mailer, codex, guard), read from
// their real manifests: GET /plugins/:key/settings masks exactly the secrets
// the schema forms marked (mailer's password) and nothing else, and a masked
// value sent back keeps the stored one. Each form is installed on the test
// guard package, which runs no plugin process.
func TestFirstPartyNativeSettingsSecrets(t *testing.T) {
	for _, c := range []struct {
		plugin  string
		values  map[string]any
		secrets []any
	}{
		{"mailer", map[string]any{"host": "smtp.example.com", "port": 587.0, "security": "starttls", "username": "ops", "password": "smtp-secret",
			"from_address": "noreply@example.com", "from_name": "Ops", "timeout_seconds": 20.0}, []any{"password"}},
		{"codex", map[string]any{"user_agent": "ua/1", "originator": "or"}, []any{}},
		{"guard", map[string]any{"webhook_url": "https://hooks.example.com/guard?token=x", "record_snippets": true, "alert_cooldown_sec": 60.0}, []any{}},
	} {
		t.Run(c.plugin, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "plugins", c.plugin, "manifest.json"))
			require.NoError(t, err)
			var m manifest.Manifest
			require.NoError(t, json.Unmarshal(raw, &m))
			require.NotNil(t, m.UI.Settings)

			h, root := newHarness(t)
			guard := pkgtest.Guard("guard", "0.1.0", "sub2api")
			guard.UI.Settings = &manifest.Form{Mode: m.UI.Settings.Mode, Component: m.UI.Settings.Component, Secrets: m.UI.Settings.Secrets}
			code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(guard, root)))
			require.Equal(t, http.StatusOK, code, out)
			consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "db.schema"}, {Permission: "gateway.hook"},
				{Permission: "events"}, {Permission: "jobs"}, {Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}
			code, out = h.do("POST", "/plugins/guard/versions/0.1.0/consent", "admin", consent)
			require.Equal(t, http.StatusOK, code, out)

			code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": c.values})
			require.Equal(t, http.StatusOK, code, out)
			want := map[string]any{}
			for k, v := range c.values {
				want[k] = v
			}
			for _, s := range c.secrets {
				want[s.(string)] = core.SecretMask
			}
			assert.Equal(t, want, data(out)["values"])
			assert.Equal(t, c.secrets, data(out)["secret_fields"])

			// Sent back masked, a secret keeps its stored value.
			code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": data(out)["values"]})
			require.Equal(t, http.StatusOK, code, out)
			stored := map[string]any{}
			require.NoError(t, json.Unmarshal([]byte(h.storedSettings()), &stored))
			assert.Equal(t, c.values, stored)
		})
	}
}
