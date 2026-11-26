package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rows"
)

// settingsCheck is a plugin's ValidateSettings: it records what it was
// given, runs during (once) and answers answer (or err).
type settingsCheck struct {
	got    []string
	answer *pluginv1.ValidateSettingsResponse
	err    error
	during func()
}

func (s *settingsCheck) ValidateSettings(_ context.Context, in *pluginv1.ValidateSettingsRequest) (*pluginv1.ValidateSettingsResponse, error) {
	s.got = append(s.got, in.GetConfigJson())
	if during := s.during; during != nil {
		s.during = nil
		during()
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.answer, nil
}

// guardSecrets are the ui.settings.secrets most tests install guard with.
var guardSecrets = []manifest.SettingsSecret{{Path: "webhook_secret"}, {Path: "notify/url", Sensitive: true}}

// installNativeSettings installs guard with native settings that declare
// secrets in ui.settings.secrets.
func installNativeSettings(t *testing.T, h *harness, root pkgtest.Key, secrets []manifest.SettingsSecret) {
	t.Helper()
	guard := pkgtest.Guard("guard", "0.1.0", "sub2api")
	guard.UI.Settings = &manifest.Form{Mode: "native", Component: "GuardSettings", Permission: "stats:read", Secrets: secrets}
	code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(guard, root)))
	require.Equal(t, http.StatusOK, code, out)
	consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "db.schema"}, {Permission: "gateway.hook"},
		{Permission: "events"}, {Permission: "jobs"}, {Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}
	code, out = h.do("POST", "/plugins/guard/versions/0.1.0/consent", "admin", consent)
	require.Equal(t, http.StatusOK, code, out)
}

func (h *harness) storedSettings() string {
	h.t.Helper()
	var enc []byte
	require.NoError(h.t, h.db.SQL.QueryRowContext(context.Background(), `SELECT config_enc FROM plugins WHERE key = 'guard'`).Scan(&enc))
	cipher, _ := secret.New(bytes.Repeat([]byte{7}, 32))
	plain, err := cipher.Decrypt(enc, rows.ConfigAAD("guard"))
	require.NoError(h.t, err)
	return string(plain)
}

func fieldsOf(out map[string]any) map[string]string {
	e, _ := out["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	list, _ := d["fields"].([]any)
	got := map[string]string{}
	for _, f := range list {
		m := f.(map[string]any)
		got[m["field"].(string)], _ = m["code"].(string)
	}
	return got
}

// Native settings mask what ui.settings.secrets names and nothing else; masks
// keep the stored values and the plugin-permission holder re-enters a kept
// credential (§75.1).
func TestNativeSettingsMaskDeclaredSecrets(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, guardSecrets)

	code, out := h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"threshold": 3, "webhook_secret": "s3cret", "notify": map[string]any{"url": "https://hooks.example/x", "name": "n"}}})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "native", data(out)["mode"])
	assert.Equal(t, map[string]any{"threshold": 3.0, "webhook_secret": core.SecretMask, "notify": map[string]any{"url": core.SecretMask, "name": "n"}}, data(out)["values"])
	assert.Equal(t, []any{"notify/url", "webhook_secret"}, data(out)["secret_fields"])

	code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"threshold": 4, "webhook_secret": core.SecretMask, "notify": map[string]any{"url": core.SecretMask, "name": "n"}}})
	require.Equal(t, http.StatusOK, code, out)
	assert.JSONEq(t, `{"threshold":4,"webhook_secret":"s3cret","notify":{"url":"https://hooks.example/x","name":"n"}}`, h.storedSettings())

	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{
		"threshold": 5, "webhook_secret": core.SecretMask, "notify": map[string]any{"url": core.SecretMask, "name": "n"}}})
	require.Equal(t, http.StatusBadRequest, code, out)
	assert.Equal(t, map[string]string{"values/webhook_secret": "reenter"}, fieldsOf(out))

	// Without the secrets key nothing says which settings are secret: every
	// top-level one is masked as a credential.
	_, err := h.db.SQL.ExecContext(context.Background(),
		`UPDATE plugin_versions SET manifest = manifest #- '{ui,settings,secrets}' WHERE plugin_key = 'guard'`)
	require.NoError(t, err)
	code, out = h.do("GET", "/plugins/guard/settings", "admin", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, map[string]any{"threshold": core.SecretMask, "webhook_secret": core.SecretMask, "notify": core.SecretMask}, data(out)["values"])
	assert.Equal(t, []any{"notify", "threshold", "webhook_secret"}, data(out)["secret_fields"])

	// secrets: [] declares that none is.
	_, err = h.db.SQL.ExecContext(context.Background(),
		`UPDATE plugin_versions SET manifest = jsonb_set(manifest, '{ui,settings,secrets}', '[]') WHERE plugin_key = 'guard'`)
	require.NoError(t, err)
	code, out = h.do("GET", "/plugins/guard/settings", "admin", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "s3cret", data(out)["values"].(map[string]any)["webhook_secret"])
	assert.Equal(t, []any{}, data(out)["secret_fields"])
}

// A declared path that meets an array masks and keeps the array whole: its
// elements cannot be named one by one (§75.1).
func TestNativeSettingsSecretThroughArray(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, []manifest.SettingsSecret{{Path: "hooks/0/secret"}})

	code, out := h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"threshold": 3, "hooks": []any{map[string]any{"url": "u", "secret": "s3cret"}}}})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, map[string]any{"threshold": 3.0, "hooks": core.SecretMask}, data(out)["values"])
	assert.Equal(t, []any{"hooks"}, data(out)["secret_fields"])

	code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{"threshold": 4, "hooks": core.SecretMask}})
	require.Equal(t, http.StatusOK, code, out)
	assert.JSONEq(t, `{"threshold":4,"hooks":[{"url":"u","secret":"s3cret"}]}`, h.storedSettings())

	// The plugin-permission holder re-enters it like any kept credential.
	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{"threshold": 5, "hooks": core.SecretMask}})
	require.Equal(t, http.StatusBadRequest, code, out)
	assert.Equal(t, map[string]string{"values/hooks": "reenter"}, fieldsOf(out))
}

// A credential and a sensitive address cut to the same value through an array
// are one secret, a credential: the plugin-permission holder re-enters it.
func TestNativeSettingsCredentialWinsAtOneValue(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, []manifest.SettingsSecret{{Path: "hooks/0/secret"}, {Path: "hooks/0/url", Sensitive: true}})

	code, out := h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"threshold": 3, "hooks": []any{map[string]any{"url": "u", "secret": "s3cret"}}}})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, []any{"hooks"}, data(out)["secret_fields"])

	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{"threshold": 4, "hooks": core.SecretMask}})
	require.Equal(t, http.StatusBadRequest, code, out)
	assert.Equal(t, map[string]string{"values/hooks": "reenter"}, fieldsOf(out))
	assert.JSONEq(t, `{"threshold":3,"hooks":[{"url":"u","secret":"s3cret"}]}`, h.storedSettings())
}

// PUT /plugins/:key/settings asks the enabled plugin (ValidateSettings) with
// the masked secrets restored: its field errors come back below values, its
// normalized object is stored; UNIMPLEMENTED and a plugin that is not in the
// generation accept.
func TestSettingsValidatedByPlugin(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, guardSecrets)
	put := func(values map[string]any) (int, map[string]any) {
		return h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": values})
	}

	// Not in the generation: stored as submitted.
	code, out := put(map[string]any{"threshold": 3, "webhook_secret": "s3cret"})
	require.Equal(t, http.StatusOK, code, out)

	check := &settingsCheck{answer: &pluginv1.ValidateSettingsResponse{Errors: []*pluginv1.FieldError{
		{Field: "notify/url", Code: "pattern", Message: "bad url"}, {Field: "", Code: "invalid", Message: "whole"}}}}
	h.validators["guard"] = check
	code, out = put(map[string]any{"threshold": 4, "webhook_secret": core.SecretMask, "notify": map[string]any{"url": "x"}})
	require.Equal(t, http.StatusBadRequest, code, out)
	assert.Equal(t, map[string]string{"values/notify/url": "pattern", "values": "invalid"}, fieldsOf(out))
	require.Len(t, check.got, 1)
	assert.JSONEq(t, `{"threshold":4,"webhook_secret":"s3cret","notify":{"url":"x"}}`, check.got[0], "secrets restored before the plugin sees them")
	assert.JSONEq(t, `{"threshold":3,"webhook_secret":"s3cret"}`, h.storedSettings(), "nothing stored")

	check.answer = &pluginv1.ValidateSettingsResponse{NormalizedConfigJson: `{"threshold":5,"webhook_secret":"s3cret","mode":"on"}`}
	code, out = put(map[string]any{"threshold": "5", "webhook_secret": core.SecretMask})
	require.Equal(t, http.StatusOK, code, out)
	assert.JSONEq(t, `{"threshold":5,"webhook_secret":"s3cret","mode":"on"}`, h.storedSettings())
	assert.Equal(t, map[string]any{"threshold": 5.0, "webhook_secret": core.SecretMask, "mode": "on"}, data(out)["values"])

	check.answer = &pluginv1.ValidateSettingsResponse{NormalizedConfigJson: `[1]`}
	code, out = put(map[string]any{"threshold": 6})
	require.Equal(t, http.StatusServiceUnavailable, code, out)

	check.err = status.Error(codes.Unimplemented, "plugin does not validate its settings")
	code, out = put(map[string]any{"threshold": 7, "webhook_secret": core.SecretMask})
	require.Equal(t, http.StatusOK, code, out)
	assert.JSONEq(t, `{"threshold":7,"webhook_secret":"s3cret"}`, h.storedSettings())

	check.err = status.Error(codes.Unavailable, "down")
	code, out = put(map[string]any{"threshold": 8})
	require.NotEqual(t, http.StatusOK, code, out)
	assert.JSONEq(t, `{"threshold":7,"webhook_secret":"s3cret"}`, h.storedSettings())
}

// The plugin checks the settings outside the saving transaction: another
// save while it checks (here from inside the check) is not blocked, and
// the first save then fails with 409 rather than overwrite it.
func TestSettingsChangedWhileValidated(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, guardSecrets)
	put := func(values map[string]any) (int, map[string]any) {
		return h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": values})
	}
	check := &settingsCheck{answer: &pluginv1.ValidateSettingsResponse{}}
	check.during = func() {
		code, out := put(map[string]any{"threshold": 2})
		assert.Equal(t, http.StatusOK, code, out)
	}
	h.validators["guard"] = check
	code, out := put(map[string]any{"threshold": 1})
	require.Equal(t, http.StatusConflict, code, out)
	assert.JSONEq(t, `{"threshold":2}`, h.storedSettings())
}

// has_settings: the current version declares native ui.settings; the
// console does not open iframe settings, nor an old package's schema
// settings, so they are not listed.
func TestHasSettingsMode(t *testing.T) {
	h, root := newHarness(t)
	installNativeSettings(t, h, root, guardSecrets)
	hasSettings := func() any {
		code, out := h.do("GET", "/plugins", "admin", nil)
		require.Equal(t, http.StatusOK, code, out)
		list, _ := out["data"].([]any)
		require.Len(t, list, 1)
		raw, _ := json.Marshal(out)
		assert.NotContains(t, string(raw), "s3cret")
		return list[0].(map[string]any)["has_settings"]
	}
	assert.Equal(t, true, hasSettings())

	for _, mode := range []string{"iframe", "schema"} {
		_, err := h.db.SQL.ExecContext(context.Background(), `UPDATE plugin_versions SET manifest = jsonb_set(manifest, '{ui,settings,mode}', to_jsonb($1::text)) WHERE plugin_key = 'guard'`, mode)
		require.NoError(t, err)
		assert.Equal(t, false, hasSettings(), mode)
	}
}
