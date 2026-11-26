package account

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

func itoa(n int64) string { return x.Itoa64(n) }

// aad binds encrypted credentials to the plugin declaring the account type.
// (Before account types were decoupled from platforms it was the platform
// id; the built-in anthropic plugin uses the same string for both.)
func aad(pluginKey string) []byte { return []byte("account:" + pluginKey) }

type fields = map[string]json.RawMessage

// parseObject decodes a JSON object; nil/empty input is an empty object.
func parseObject(b []byte) (fields, error) {
	m := fields{}
	if len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = fields{}
	}
	return m, nil
}

func mustJSON(m fields) []byte {
	if m == nil {
		m = fields{}
	}
	b, _ := json.Marshal(m)
	return b
}

// split separates top-level settings keys (stored as plain JSONB) from the
// rest (encrypted).
func split(all fields, settingsFields []string) (secretPart, settings fields) {
	secretPart, settings = fields{}, fields{}
	for k, v := range all {
		if slices.Contains(settingsFields, k) {
			settings[k] = v
		} else {
			secretPart[k] = v
		}
	}
	return secretPart, settings
}

// merge combines the decrypted secret part and settings into the object the
// console submitted.
func merge(secretJSON, settingsJSON []byte) (fields, error) {
	all, err := parseObject(secretJSON)
	if err != nil {
		return nil, err
	}
	st, err := parseObject(settingsJSON)
	if err != nil {
		return nil, err
	}
	for k, v := range st {
		all[k] = v
	}
	return all, nil
}

// mask replaces non-empty sensitive values (gjson paths) with core.SecretMask.
func mask(b []byte, sensitive []string) []byte {
	for _, p := range sensitive {
		r := gjson.GetBytes(b, p)
		if !r.Exists() || r.Type == gjson.Null || (r.Type == gjson.String && r.Str == "") {
			continue
		}
		if nb, err := sjson.SetBytes(b, p, core.SecretMask); err == nil {
			b = nb
		}
	}
	return b
}

// maskAll masks every top-level value (used when the account type is no
// longer registered and its sensitive fields are unknown).
func maskAll(secretPart fields) fields {
	out := make(fields, len(secretPart))
	for k := range secretPart {
		out[k] = json.RawMessage(strconv.Quote(core.SecretMask))
	}
	return out
}

// unmask puts stored values back where the client sent core.SecretMask and returns the
// paths it restored.
func unmask(b, old []byte, sensitive []string) ([]byte, []string) {
	var restored []string
	for _, p := range sensitive {
		r := gjson.GetBytes(b, p)
		if r.Type != gjson.String || r.Str != core.SecretMask {
			continue
		}
		var err error
		var nb []byte
		if o := gjson.GetBytes(old, p); o.Exists() {
			nb, err = sjson.SetRawBytes(b, p, []byte(o.Raw))
			restored = append(restored, p)
		} else {
			nb, err = sjson.DeleteBytes(b, p)
		}
		if err == nil {
			b = nb
		}
	}
	return b, restored
}

// addressChanged reports whether any upstream field of the type differs
// between the submitted credentials b and the stored ones old (nil on create:
// any non-empty address).
func addressChanged(b, old []byte, at manifest.AccountType) bool {
	return slices.ContainsFunc(at.UpstreamFields, func(f string) bool {
		return addressValue(b, f) != addressValue(old, f)
	})
}

// addressValue is the comparison form of field f of b: strings after
// guardNorm, other values compacted; absent, null and "" are all "".
// Versions are not applied: a value differing only by a version segment is a
// changed address.
func addressValue(b []byte, f string) string {
	r := gjson.GetBytes(b, f)
	if r.Type != gjson.JSON {
		return guardNorm(r.String(), nil)
	}
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(r.Raw)) != nil {
		return r.Raw
	}
	return buf.String()
}

// reenterRequired fails each restored secret: a caller who may not see the
// stored secrets must type them again to send them to a new address.
func reenterRequired(ctx context.Context, restored []string) error {
	fe := make([]core.FieldError, len(restored))
	for i, p := range restored {
		fe[i] = core.FieldError{Field: "credentials." + p, Code: "reenter_required",
			Message: t(ctx, "enter this secret again when changing the upstream address", "修改上游地址时需要重新输入该密钥")}
	}
	return core.InvalidFields(fe...)
}

// ---------------------------------------------------------------- preparation

// prepared is the storable form of submitted credentials.
type prepared struct {
	enc      []byte
	settings []byte
	secret   []byte
	// addressChanged: an upstream address field differs from the stored one
	// (on create: one is set).
	addressChanged bool
}

// credBase is what prepare needs of an existing account: the stored secret
// part, the merged plaintext and whether the caller may reveal it.
type credBase struct {
	secret []byte
	old    []byte
	reveal bool
}

// credBase decrypts a's credentials; reveal is whether a is in the caller's
// account:credential:view scope.
func (s *Service) credBase(ctx context.Context, a *row) (*credBase, error) {
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, err
	}
	all, err := merge(plain, a.Settings)
	if err != nil {
		return nil, err
	}
	scope, err := s.scopeOf(ctx, perm.AccountCredentialView)
	if err != nil {
		return nil, err
	}
	reveal, err := inScope(ctx, s.d.DB.Gorm, a.ID, scope)
	if err != nil {
		return nil, err
	}
	return &credBase{secret: plain, old: mustJSON(all), reveal: reveal}, nil
}

// prepare validates credentials (the plugin, then the guarded
// settings without account:settings:custom) and encrypts the non-settings
// part. base is the stored account (nil on create): core.SecretMask values are replaced
// from it, guarded fields equal to their old value pass, and restoring a
// secret the caller may not reveal while changing the upstream address fails
// before the plugin sees it.
func (s *Service) prepare(ctx context.Context, bt core.AccountTypeBinding, raw json.RawMessage, base *credBase) (*prepared, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, core.InvalidFields(core.FieldError{Field: "credentials", Code: "invalid",
			Message: t(ctx, "credentials must be a JSON object", "凭证必须为 JSON 对象")})
	}
	if _, err := parseObject(raw); err != nil {
		return nil, core.InvalidFields(core.FieldError{Field: "credentials", Code: "invalid",
			Message: t(ctx, "credentials must be a JSON object", "凭证必须为 JSON 对象")})
	}
	b := []byte(raw)
	var old []byte
	var restored []string
	if base != nil {
		old = base.old
		b, restored = unmask(b, old, bt.Type.SensitiveFields)
	}
	moved := addressChanged(b, old, bt.Type)
	if len(restored) > 0 && moved && !base.reveal {
		return nil, reenterRequired(ctx, restored)
	}
	environmentKeys := extensionKeys(bt)
	if len(environmentKeys) > 0 {
		all, _ := parseObject(b)
		for _, key := range environmentKeys {
			delete(all, key)
		}
		b = mustJSON(all)
	}
	all, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	sec, st := split(all, bt.Type.SettingsFields)
	secJSON, stJSON := mustJSON(sec), mustJSON(st)
	if bt.Client != nil {
		resp, err := bt.Client.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{
			AccountType:     bt.Type.ID,
			CredentialsJson: string(secJSON),
			SettingsJson:    string(stJSON),
		})
		if err != nil {
			return nil, err
		}
		if len(resp.GetErrors()) > 0 {
			fe := make([]core.FieldError, 0, len(resp.GetErrors()))
			for _, e := range resp.GetErrors() {
				f := "credentials"
				if p := strings.TrimPrefix(e.GetField(), "/"); p != "" {
					f += "." + strings.ReplaceAll(p, "/", ".")
				}
				fe = append(fe, core.FieldError{Field: f, Code: e.GetCode(), Message: e.GetMessage()})
			}
			return nil, core.InvalidFields(fe...)
		}
		if resp.GetNormalizedCredentialsJson() != "" || resp.GetNormalizedSettingsJson() != "" {
			nc, ns := secJSON, stJSON
			if v := resp.GetNormalizedCredentialsJson(); v != "" {
				nc = []byte(v)
			}
			if v := resp.GetNormalizedSettingsJson(); v != "" {
				ns = []byte(v)
			}
			all, err := merge(nc, ns)
			if err != nil {
				return nil, core.ErrPluginUnavailable.WithMessage("plugin returned invalid normalized credentials").WithCause(err)
			}
			sec, st = split(all, bt.Type.SettingsFields)
			secJSON, stJSON = mustJSON(sec), mustJSON(st)
		}
	}
	custom, err := s.customSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !custom {
		if err := checkGuarded(ctx, bt.Type.GuardedSettings, st, old); err != nil {
			return nil, err
		}
	}
	enc, err := s.d.Cipher.Encrypt(secJSON, aad(bt.Plugin.Key))
	if err != nil {
		return nil, err
	}
	if len(environmentKeys) > 0 {
		previous, err := parseObject(old)
		if err != nil {
			return nil, err
		}
		for _, key := range environmentKeys {
			if value, ok := previous[key]; ok {
				st[key] = value
			}
		}
		stJSON = mustJSON(st)
	}
	return &prepared{enc: enc, settings: stJSON, secret: secJSON, addressChanged: moved}, nil
}

// ---------------------------------------------------------------- guarded settings

// checkGuarded enforces manifest guardedSettings (CONTRACTS §21.3) on the
// normalized settings of a caller without account:settings:custom: each
// guarded field must be empty/absent, one of the allowed values or (on
// update) the value the account already had. Values are compared after
// guardNorm with the setting's versions. Violations are invalid_argument with
// fields[{credentials.<field>, forbidden}].
func checkGuarded(ctx context.Context, guards []manifest.GuardedSetting, settings fields, old []byte) error {
	var fe []core.FieldError
	for _, g := range guards {
		raw, ok := settings[g.Field]
		if !ok {
			continue
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			// Not a string: null passes (the plugin default applies), anything
			// else cannot be an official URL.
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			v = string(raw)
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		want := guardNorm(v, g.Versions)
		if slices.ContainsFunc(g.Allowed, func(a string) bool { return guardNorm(a, g.Versions) == want }) {
			continue
		}
		if old != nil {
			if prev := gjson.GetBytes(old, g.Field); prev.Type == gjson.String && guardNorm(prev.Str, g.Versions) == want {
				continue
			}
		}
		fe = append(fe, core.FieldError{Field: "credentials." + g.Field, Code: "forbidden",
			Message: t(ctx, "only the official value is allowed for this field", "该字段只能使用官方地址")})
	}
	if len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// guardNorm is the comparison form of a guarded value: trimmed, without
// trailing slashes, and (when it parses as an absolute URL) with lower-case
// scheme and host and without one of versions ending the path
// (manifest.TrimVersion) and the "/" left before it, as the SDK's request
// root.
// The version is looked for after the trailing slashes are gone, so
// "https://API.example.com/v1/" compares as "https://api.example.com", and
// "https://x//v1" as "https://x".
func guardNorm(v string, versions []string) string {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if u, err := url.Parse(v); err == nil && u.Scheme != "" && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		esc := u.EscapedPath()
		if p := manifest.TrimVersion(esc, versions); p != esc {
			u.RawPath = strings.TrimRight(p, "/")
			u.Path, _ = url.PathUnescape(u.RawPath) // a prefix of a valid escaped path
		}
		return u.String()
	}
	return v
}

// decrypt returns the plaintext secret part of an account.
func (s *Service) decrypt(pluginKey string, enc []byte) ([]byte, error) {
	return s.d.Cipher.Decrypt(enc, aad(pluginKey))
}
