package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Mask replaces secret setting values in responses; sending it back in a
// PUT keeps the stored value.
const Mask = "******"

// ConfigAAD binds plugins.config_enc to its plugin (shared with the runtime).
func ConfigAAD(key string) []byte { return []byte("plugin-config:" + key) }

func isNoRows(err error) bool { return store.IsNoRows(err) }

// SettingsView is GET /plugins/:key/settings.
type SettingsView struct {
	Mode      string         `json:"mode"` // iframe | native | none
	Page      string         `json:"page,omitempty"`
	Component string         `json:"component,omitempty"`
	Values    map[string]any `json:"values"`
	// Secrets lists the masked fields as pointers below values ("a/b" for
	// nested ones), credentials and sensitive addresses alike.
	Secrets []string               `json:"secret_fields"`
	fields  []manifest.SecretField // ui.settings.secrets, classified
	// maskAll: every top-level setting is a credential, because nothing says
	// which ones are: no ui.settings, a mode this core does not know (an old
	// package's schema settings), or native or iframe settings without
	// ui.settings.secrets.
	maskAll bool

	// Permission is the RBAC key of ui.settings.permission: its holders may
	// read and edit these settings too (CONTRACTS §75.1).
	Permission string `json:"permission,omitempty"`
}

// settingsForm loads the settings form of the plugin after checking that the
// caller holds corePerm or the permission the plugin names for its settings.
// The permission check comes first: a caller holding neither gets 403 whether
// or not the plugin exists.
func (a *API) settingsForm(c *gin.Context, key, corePerm string) (*SettingsView, bool) {
	m, _, err := a.loadCurrentManifest(c.Request.Context(), key)
	v := &SettingsView{Mode: "none", Values: map[string]any{}, Secrets: []string{}, maskAll: true}
	var f *manifest.Form
	if m != nil && m.UI != nil {
		f = m.UI.Settings
	}
	if f != nil && f.Permission != "" {
		v.Permission = manifest.PermissionKey(key, f.Permission)
	}
	if !httpapi.CanAny(c, a.d.Authz, corePerm, v.Permission) {
		return nil, false
	}
	if err != nil {
		httpapi.Fail(c, err)
		return nil, false
	}
	if f == nil {
		return v, true
	}
	v.Mode, v.Page, v.Component = f.Mode, f.Page, f.Component
	// Native or iframe settings mask what ui.settings.secrets names ([] =
	// nothing); without the key nothing says which settings are secret and
	// maskAll stays, as for any other mode.
	if (f.Mode == "native" || f.Mode == "iframe") && f.Secrets != nil {
		v.fields, v.maskAll = f.SecretFields(), false
	}
	return v, true
}

// topLevelCredentials makes each named top-level setting a credential.
func topLevelCredentials(names []string) []manifest.SecretField {
	out := make([]manifest.SecretField, len(names))
	for i, name := range names {
		out[i] = manifest.SecretField{Path: []string{name}, Credential: true}
	}
	return out
}

func sortSecrets(fields []manifest.SecretField) []manifest.SecretField {
	sort.Slice(fields, func(i, j int) bool { return fields[i].Pointer() < fields[j].Pointer() })
	return fields
}

// secrets are the masked fields of the form given the values at hand (stored,
// submitted): the declared ones, or under maskAll every top-level key of them.
// A declared path that meets a value other than an object on its way (an
// array, whose elements cannot be named) in any of values masks and keeps
// that value whole instead.
func (v *SettingsView) secrets(values ...map[string]any) []manifest.SecretField {
	if !v.maskAll {
		return wholeAtNonObjects(v.fields, values)
	}
	seen := map[string]bool{}
	for _, m := range values {
		for k := range m {
			seen[k] = true
		}
	}
	return sortSecrets(topLevelCredentials(slices.Collect(maps.Keys(seen))))
}

// wholeAtNonObjects cuts each field's path at the first value on its way that
// is not an object in any of values; fields cut to the same path become one,
// a credential if any of them is.
func wholeAtNonObjects(fields []manifest.SecretField, values []map[string]any) []manifest.SecretField {
	out := make([]manifest.SecretField, 0, len(fields))
	for _, f := range fields {
		f.Path = f.Path[:objectDepth(f.Path, values)]
		if i := slices.IndexFunc(out, func(o manifest.SecretField) bool { return slices.Equal(o.Path, f.Path) }); i >= 0 {
			out[i].Credential = out[i].Credential || f.Credential
			continue
		}
		out = append(out, f)
	}
	return sortSecrets(out)
}

// objectDepth is how much of path leads through objects: len(path), or the
// depth of the first value on its way that is not an object in one of values.
func objectDepth(path []string, values []map[string]any) int {
	n := len(path)
	for _, m := range values {
		var cur any = m
		for i := 0; i < n; i++ {
			obj, ok := cur.(map[string]any)
			if !ok {
				n = i
				break
			}
			if cur = obj[path[i]]; cur == nil {
				break
			}
		}
	}
	return n
}

// valueAt returns the value at path inside nested objects.
func valueAt(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// parentOf returns the object holding the last element of path.
func parentOf(m map[string]any, path []string) (map[string]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	v, ok := valueAt(m, path[:len(path)-1])
	if !ok {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

// maskValues replaces every non-empty secret value in values with Mask.
func maskValues(fields []manifest.SecretField, values map[string]any) {
	for _, f := range fields {
		if obj, ok := parentOf(values, f.Path); ok {
			if val, ok := obj[f.Path[len(f.Path)-1]]; ok && val != nil && val != "" {
				obj[f.Path[len(f.Path)-1]] = Mask
			}
		}
	}
}

// mergeSettings resolves masked values in in against the stored old values
// and returns the top-level keys that change. A caller holding only the
// plugin's own permission (manager false) keeps a stored credential only
// when nothing at all changes: re-entering another credential, editing a
// sensitive address or any plain setting needs every kept credential typed
// again, or it could point an address at itself and have the stored
// credential sent there. A masked sensitive address is kept freely: it is
// not a credential and stays where it was (CONTRACTS §75.1).
func mergeSettings(fields []manifest.SecretField, old, in map[string]any, manager bool, validate func(map[string]any) error) ([]string, error) {
	var kept []manifest.SecretField
	for _, f := range fields {
		obj, ok := parentOf(in, f.Path)
		name := f.Path[len(f.Path)-1]
		if !ok || obj[name] != Mask {
			continue
		}
		if ov, ok := valueAt(old, f.Path); ok {
			obj[name] = ov
			if f.Credential {
				kept = append(kept, f)
			}
		} else {
			delete(obj, name)
		}
	}
	if validate != nil {
		if err := validate(in); err != nil {
			return nil, err
		}
	}
	var changed []string
	for k, v := range in {
		ob, _ := json.Marshal(old[k])
		nb, _ := json.Marshal(v)
		if _, had := old[k]; !had || !bytes.Equal(ob, nb) {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, ok := in[k]; !ok {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	if !manager && len(kept) > 0 && len(changed) > 0 {
		errs := make([]core.FieldError, 0, len(kept))
		for _, f := range kept {
			errs = append(errs, core.FieldError{Field: "values/" + f.Pointer(), Code: "reenter",
				Message: "re-enter this secret to change settings"})
		}
		return nil, core.InvalidFields(errs...)
	}
	return changed, nil
}

func (a *API) loadValues(c *gin.Context, q *gorm.DB, key string, forUpdate bool) (map[string]any, error) {
	if forUpdate {
		if err := store.RequireTx(q); err != nil {
			return nil, err
		}
	}
	db := store.G(c.Request.Context(), q).Select("config_enc").Where("key = ?", key)
	if forUpdate {
		db = db.Clauses(store.ForUpdate())
	}
	var row model.Plugin
	if err := db.Take(&row).Error; err != nil {
		if isNoRows(err) {
			return nil, core.ErrNotFound.WithMessage("plugin not found")
		}
		return nil, err
	}
	enc := row.ConfigEnc
	values := map[string]any{}
	if len(enc) == 0 {
		return values, nil
	}
	if a.d.Cipher == nil {
		return nil, core.ErrUnavailable.WithMessage("settings encryption unavailable")
	}
	plain, err := a.d.Cipher.Decrypt(enc, ConfigAAD(key))
	if err != nil {
		return nil, fmt.Errorf("decrypt plugin settings: %w", err)
	}
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("decode plugin settings: %w", err)
	}
	return values, nil
}

func (a *API) getSettings(c *gin.Context) {
	key := c.Param("key")
	v, ok := a.settingsForm(c, key, PermRead)
	if !ok {
		return
	}
	a.renderSettings(c, key, v)
}

// renderSettings answers the form v with the stored values, secrets masked.
func (a *API) renderSettings(c *gin.Context, key string, v *SettingsView) {
	values, err := a.loadValues(c, a.d.DB.Gorm, key, false)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	fields := v.secrets(values)
	maskValues(fields, values)
	v.Values, v.Secrets = values, make([]string, len(fields))
	for i, f := range fields {
		v.Secrets[i] = f.Pointer()
	}
	httpapi.OK(c, v)
}

func (a *API) putSettings(c *gin.Context) {
	key := c.Param("key")
	form, ok := a.settingsForm(c, key, PermManage)
	if !ok {
		return
	}
	var in struct {
		Values map[string]any `json:"values"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if in.Values == nil {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "values", Code: "required", Message: "values must be an object"}))
		return
	}
	if a.d.Cipher == nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage("settings encryption unavailable"))
		return
	}
	manager := core.HasGranted(c.Request.Context(), PermManage)
	var changed []string
	err := a.d.DB.Tx(c.Request.Context(), func(tx *gorm.DB) error {
		old, err := a.loadValues(c, tx, key, true)
		if err != nil {
			return err
		}
		validate := func(v map[string]any) error { return a.pluginValidate(c.Request.Context(), key, v) }
		if changed, err = mergeSettings(form.secrets(old, in.Values), old, in.Values, manager, validate); err != nil {
			return err
		}
		plain, err := json.Marshal(in.Values)
		if err != nil {
			return err
		}
		enc, err := a.d.Cipher.Encrypt(plain, ConfigAAD(key))
		if err != nil {
			return err
		}
		if err := updatePlugin(store.G(c.Request.Context(), tx), key, "config_enc", enc).Error; err != nil {
			return err
		}
		// Only key names are audited; values may be secret.
		return audit.Audit(ctx(c), tx, actor(c), "plugin.settings.update", "plugin", key, map[string]any{"changed_keys": changed, "request_id": core.RequestID(c.Request.Context())})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	install.Notify(c.Request.Context(), a.d.Bus, key)
	a.renderSettings(c, key, form)
}

// pluginValidate has the plugin check values (PluginService.ValidateSettings)
// and replaces them with what it normalized. A plugin that is not enabled is
// not in the generation and not asked: Configure checks the stored values
// when it loads, as for account credentials. One that does not implement the
// call accepts.
func (a *API) pluginValidate(ctx context.Context, key string, values map[string]any) error {
	if a.d.Registry == nil || a.d.Registry.Current() == nil {
		return nil
	}
	plugin, ok := a.d.Registry.Current().SettingsValidator(key)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	resp, err := plugin.ValidateSettings(ctx, &pluginv1.ValidateSettingsRequest{ConfigJson: string(raw)})
	if status.Code(err) == codes.Unimplemented {
		return nil
	}
	if err != nil {
		return err
	}
	if errs := resp.GetErrors(); len(errs) > 0 {
		fe := make([]core.FieldError, len(errs))
		for i, e := range errs {
			fe[i] = core.FieldError{Field: settingsField(e.GetField()), Code: e.GetCode(), Message: e.GetMessage()}
		}
		return core.InvalidFields(fe...)
	}
	if n := resp.GetNormalizedConfigJson(); n != "" {
		var normalized map[string]any
		if err := json.Unmarshal([]byte(n), &normalized); err != nil || normalized == nil {
			return core.ErrPluginUnavailable.WithMessage("plugin returned invalid normalized settings").WithCause(err)
		}
		clear(values)
		maps.Copy(values, normalized)
	}
	return nil
}

// settingsField is the error field of a JSON pointer below the values:
// "values/a/b", or "values" for the whole object.
func settingsField(pointer string) string {
	if p := strings.TrimPrefix(pointer, "/"); p != "" {
		return "values/" + p
	}
	return "values"
}
