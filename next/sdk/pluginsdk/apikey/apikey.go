// Package apikey validates and reads the api_key + base_url credential form.
// Spec.Validate is its one ValidateCredentials (anthropic, openai, gemini,
// relay, volcengine and the other key-based types use it): the key is any
// non-empty string, saved trimmed; the base URL is saved as the user wrote it,
// normalized only in ways that keep its meaning (NormalizeBaseURL).
// Spec.FromAccount reads them for a request, the base URL without the one
// trailing version segment it may carry (Spec.Versions). Spec.Texts only
// overrides the wording each plugin shipped.
package apikey

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// Config is the normalized account configuration extracted from credentials
// and settings JSON by Spec.FromAccount.
type Config struct {
	APIKey  string
	BaseURL string
}

// Texts overrides the wording of a few Spec.Validate field errors. An empty
// field keeps the SDK text. The console renders these strings verbatim and the
// built-in plugins shipped their own wording before this helper existed; the
// validation rules are the same either way.
//
// A message is plain text with named placeholders, never a printf template: a
// message containing a bare percent sign ("100%") comes out unchanged, and an
// unknown placeholder is left as it is.
type Texts struct {
	// UnsupportedAccountType is the account_type message; "{type}" is replaced
	// by the received account type, Go-quoted as %q used to quote it.
	UnsupportedAccountType string
	// KeyNotString, when set, reports a non-string api_key with this message
	// instead of the SDK's own type message.
	KeyNotString string
	// KeyNotStringCode is the code reported with KeyNotString. Default
	// "pattern" (the code this helper has always used for that message);
	// plugins that keep the SDK's "type" code set it explicitly.
	KeyNotStringCode string
	// BaseURLRequired is the message of a missing base_url when
	// RequireBaseURL is set.
	BaseURLRequired string
	// BaseURLFormat is the base_url format message; "{reason}" is replaced by
	// the reason.
	BaseURLFormat string
}

// placeholder replaces one named placeholder in a Texts message. It never
// interprets the surrounding text as a format string.
func placeholder(msg, name, value string) string {
	return strings.ReplaceAll(msg, "{"+name+"}", value)
}

// Spec describes an account type that uses api_key and optional base_url.
// It validates, normalizes, and extracts these fields from ValidateCredentialsRequest
// and Account protos.
type Spec struct {
	// AccountType is the expected account type (e.g. "apikey", "relay_key").
	AccountType string
	// DefaultBaseURL is used when base_url is empty or absent.
	DefaultBaseURL string
	// Versions are the version segments a base URL may end in (e.g.
	// []string{"/v1beta", "/v1"}), each starting with "/" and not ending in
	// one. They are kept on save; FromAccount removes the longest one the
	// stored URL's path ends in, at most one, compared on the escaped form
	// and case-sensitively, and every route path is appended to what is left.
	Versions []string
	// RequireBaseURL = true makes base_url mandatory (for relay).
	RequireBaseURL bool
	// NoBaseURL declares a type without base_url (vertex_apikey): Validate
	// refuses one in either object as an unknown key, even when Fields names
	// it, and FromAccount leaves Config.BaseURL empty. It is not used with
	// RequireBaseURL, DefaultBaseURL or Versions.
	NoBaseURL bool
	// Texts overrides the wording of a few field errors (see Texts).
	Texts Texts
	// Fields are the keys other than api_key and base_url the form may
	// send, each with its rules (fieldcheck.Any() for a key taking any
	// value). Validate checks them, reading each from the credentials, then
	// the settings, and refuses every other key of either object with code
	// "unknown"; nil is the same as empty: only api_key and base_url (only
	// api_key under NoBaseURL). A rule for api_key or base_url adds to the
	// checks above, only when those passed, and sees the value Validate
	// saves: the key trimmed, the base URL normalized (versions kept),
	// wherever the key was sent.
	Fields fieldcheck.Fields
}

// Validate implements ValidateCredentials for the account type described by s.
func (s Spec) Validate(in *pluginv1.ValidateCredentialsRequest) *pluginv1.ValidateCredentialsResponse {
	var errs pluginsdk.FieldErrors
	if in.GetAccountType() != s.AccountType {
		msg := s.Texts.UnsupportedAccountType
		if msg == "" {
			msg = "unsupported account type / 不支持的账号类型"
		} else {
			msg = placeholder(msg, "type", strconv.Quote(in.GetAccountType()))
		}
		errs = errs.Add("account_type", "unsupported", msg)
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}
	creds, err := decodeObject(in.GetCredentialsJson())
	if err != nil {
		errs = errs.Add("", "invalid_json", "credentials must be a JSON object / 凭证必须是 JSON 对象")
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}
	settings, err := decodeObject(in.GetSettingsJson())
	if err != nil {
		errs = errs.Add("", "invalid_json", "settings must be a JSON object / 设置必须是 JSON 对象")
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}

	key, errs := s.validateKey(errs, creds, settings)
	baseURL, errs := s.validateBaseURL(errs, creds, settings)
	errs = s.checkFields(errs, normalized(creds, key, baseURL), normalized(settings, key, baseURL))
	if len(errs) > 0 {
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}

	// Normalize each object in place: api_key and base_url are rewritten
	// where they are, the keys Fields names pass through untouched. Moving
	// base_url from settings into credentials would take it out of reach of
	// the core's guardedSettings check and the console form. An empty object
	// stays empty: "" tells the core to keep what it has.
	return &pluginv1.ValidateCredentialsResponse{
		NormalizedCredentialsJson: s.rewriteObject(creds, key, baseURL),
		NormalizedSettingsJson:    s.rewriteObject(settings, key, baseURL),
	}
}

// validateKey reads api_key from credentials then settings and returns the
// trimmed key: any non-empty string. It appends exactly one api_key error
// when the key is missing, blank or not a string.
func (s Spec) validateKey(errs pluginsdk.FieldErrors, creds, settings map[string]any) (string, pluginsdk.FieldErrors) {
	rawKey, _ := lookup("api_key", creds, settings)
	key, isString := rawKey.(string)
	key = strings.TrimSpace(key)
	switch {
	case rawKey == nil || (isString && key == ""):
		return "", errs.Add("api_key", "required", "API key is required / 请填写 API Key")
	case !isString:
		if s.Texts.KeyNotString != "" {
			code := s.Texts.KeyNotStringCode
			if code == "" {
				code = "pattern"
			}
			return "", errs.Add("api_key", code, s.Texts.KeyNotString)
		}
		return "", errs.Add("api_key", "type", "api_key must be a string / api_key 必须是字符串")
	}
	return key, errs
}

// validateBaseURL reads base_url from settings then credentials and returns
// the normalized URL (s.DefaultBaseURL when absent). A type without base_url
// leaves it to checkFields.
func (s Spec) validateBaseURL(errs pluginsdk.FieldErrors, creds, settings map[string]any) (string, pluginsdk.FieldErrors) {
	if s.NoBaseURL {
		return "", errs
	}
	rawBase, _ := lookup("base_url", settings, creds)
	baseStr, isStr := rawBase.(string)
	baseStr = strings.TrimSpace(baseStr)
	switch {
	case rawBase == nil || (isStr && baseStr == ""):
		if s.RequireBaseURL {
			msg := s.Texts.BaseURLRequired
			if msg == "" {
				msg = "Base URL is required / 请填写 Base URL"
			}
			return "", errs.Add("base_url", "required", msg)
		}
		return s.DefaultBaseURL, errs
	case !isStr:
		return "", errs.Add("base_url", "type", "base_url must be a string / base_url 必须是字符串")
	}
	normalized, err := s.normalizeBaseURL(baseStr)
	if err != nil {
		msg := s.Texts.BaseURLFormat
		if msg == "" {
			msg = "base_url {reason} / base_url 格式错误"
		}
		return "", errs.Add("base_url", "format", placeholder(msg, "reason", err.Error()))
	}
	return normalized, errs
}

// checkFields adds the errors of s.Fields (api_key and, unless NoBaseURL,
// base_url known) on the normalized objects, except at a field that already
// has one.
func (s Spec) checkFields(errs pluginsdk.FieldErrors, creds, settings map[string]any) pluginsdk.FieldErrors {
	fields := fieldcheck.Fields{"api_key": fieldcheck.Any(), "base_url": fieldcheck.Any()}
	maps.Copy(fields, s.Fields)
	if s.NoBaseURL {
		delete(fields, "base_url")
	}
	for _, e := range fieldcheck.Check(fields, creds, settings) {
		if !slices.ContainsFunc(errs, func(x *pluginv1.FieldError) bool { return x.GetField() == e.GetField() }) {
			errs = append(errs, e)
		}
	}
	return errs
}

// normalized is obj with api_key and base_url replaced by key and baseURL
// where obj holds them; every other key as it is.
func normalized(obj map[string]any, key, baseURL string) map[string]any {
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		switch k {
		case "api_key":
			out[k] = key
		case "base_url":
			out[k] = baseURL
		default:
			out[k] = v
		}
	}
	return out
}

// rewriteObject re-encodes obj with api_key and base_url normalized where the
// key already exists; an empty object stays empty ("" keeps the core's value).
func (s Spec) rewriteObject(obj map[string]any, key, baseURL string) string {
	if len(obj) == 0 {
		return ""
	}
	b, _ := json.Marshal(normalized(obj, key, baseURL))
	return string(b)
}

// FromAccount extracts and validates the api_key and base_url from an Account
// proto. Config.BaseURL is the root: the base URL without the version segment
// it ends in (Spec.Versions); empty under NoBaseURL, which reads no base_url.
func (s Spec) FromAccount(acc *pluginv1.Account) (*Config, error) {
	if acc == nil {
		return nil, status.Error(codes.FailedPrecondition, "account required")
	}
	creds, err := decodeObject(acc.GetCredentialsJson())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid credentials JSON: %v", err)
	}
	settings, err := decodeObject(acc.GetSettingsJson())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid settings JSON: %v", err)
	}

	cfg := &Config{}
	if v, ok := lookup("api_key", creds, settings); ok {
		cfg.APIKey, _ = v.(string)
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.APIKey == "" {
		return nil, status.Error(codes.InvalidArgument, "missing api_key")
	}
	if s.NoBaseURL {
		return cfg, nil
	}

	base := ""
	if v, ok := lookup("base_url", settings, creds); ok {
		base, _ = v.(string)
	}
	if cfg.BaseURL, err = s.normalizeBaseURL(strings.TrimSpace(base)); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid base_url: %v", err)
	}
	if cfg.BaseURL == "" && s.RequireBaseURL {
		return nil, status.Error(codes.InvalidArgument, "base_url is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = s.DefaultBaseURL
	}
	cfg.BaseURL = s.root(cfg.BaseURL)

	return cfg, nil
}

// root is base without the version segment its path ends in
// (manifest.TrimVersion with s.Versions) and the "/" left before it; the
// scheme and host are never looked at. base is a stored base URL (a
// NormalizeBaseURL output, its path in escaped form) or the default.
func (s Spec) root(base string) string {
	i := pathStart(base)
	return base[:i] + strings.TrimRight(manifest.TrimVersion(base[i:], s.Versions), "/")
}

// pathStart is the index of the path in an absolute URL: the first "/" after
// "://", or len(u) when there is none.
func pathStart(u string) int {
	_, rest, _ := strings.Cut(u, "://")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return len(u) - len(rest) + i
	}
	return len(u)
}

func (s Spec) normalizeBaseURL(baseStr string) (string, error) {
	if strings.TrimSpace(baseStr) == "" {
		if s.RequireBaseURL {
			return "", fmt.Errorf("is required")
		}
		return s.DefaultBaseURL, nil
	}
	return NormalizeBaseURL(baseStr)
}

// NormalizeBaseURL validates and normalizes a base URL without changing what
// it means: surrounding whitespace trimmed, the scheme lower-cased, the path
// in the escaped form the user wrote ("%2F" stays "%2F", raw non-ASCII is
// percent-encoded) without trailing "/". No suffix is removed. Empty, whitespace
// inside, "?", "#", userinfo and anything but an absolute http(s) URL are
// refused (the account forms' ^https?://[^\s?#]+$). A value it returns
// normalizes to itself.
func NormalizeBaseURL(baseStr string) (string, error) {
	baseStr = strings.TrimSpace(baseStr)
	if baseStr == "" {
		return "", fmt.Errorf("is required")
	}
	if strings.ContainsFunc(baseStr, unicode.IsSpace) {
		return "", fmt.Errorf("must not contain whitespace")
	}
	if strings.ContainsAny(baseStr, "?#") {
		return "", fmt.Errorf("must not contain credentials, query or fragment")
	}
	u, err := url.Parse(baseStr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("must be an absolute http(s) URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("must not contain credentials, query or fragment")
	}
	origin := url.URL{Scheme: u.Scheme, Host: u.Host}
	return origin.String() + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// ModelList describes a model listing request: the path on base_url (with
// whatever page size the upstream expects, e.g. "/v1/models?limit=1000"), where
// the ids sit in the answer, and the prefix to strip from each id.
type ModelList struct {
	// Path is appended to the account's base_url.
	Path string
	// IdsPath is the gjson path of the model ids in the response.
	IdsPath string
	// StripPrefix is removed from the front of every id ("models/" for
	// Gemini's resource names).
	StripPrefix string
}

// ModelListRequest builds the GET a plugin's BuildModelsRequest returns: a
// model listing on base_url + list.Path, with the request's headers minus
// content-type (nothing is sent in the body). headers is the map
// upstreamHeaders just built; it is copied, never modified, so a caller may
// reuse its header map.
func ModelListRequest(cfg *Config, list ModelList, headers map[string]string) *pluginv1.BuildModelsRequestResponse {
	h := make(map[string]string, len(headers))
	for k, v := range headers {
		if !strings.EqualFold(k, "content-type") {
			h[k] = v
		}
	}
	return &pluginv1.BuildModelsRequestResponse{
		Method:      "GET",
		Url:         cfg.BaseURL + list.Path,
		Headers:     h,
		IdsPath:     list.IdsPath,
		StripPrefix: list.StripPrefix,
	}
}

// ForwardHeaders copies the client headers the core handed over
// (ExecuteRequest.inbound_headers) into out: lower-case, trimmed, blank ones
// dropped. The core passes only the manifest's passHeaders, so nothing more
// is filtered.
func ForwardHeaders(out, inbound map[string]string) {
	for name, value := range inbound {
		if value = strings.TrimSpace(value); value != "" {
			out[strings.ToLower(name)] = value
		}
	}
}

// Helper functions

// DecodeObject parses a JSON string into a map. An empty string (or "null")
// yields an empty map, which is what a form posting nothing looks like.
func DecodeObject(raw string) (map[string]any, error) {
	return decodeObject(raw)
}

func decodeObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// Lookup searches the given maps in order and returns the first non-nil value
// of key (the same precedence Spec.Validate applies).
func Lookup(key string, objs ...map[string]any) (any, bool) {
	return lookup(key, objs...)
}

func lookup(key string, objs ...map[string]any) (any, bool) {
	for _, o := range objs {
		if v, ok := o[key]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}
