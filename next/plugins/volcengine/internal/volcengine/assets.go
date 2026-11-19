package volcengine

// This file holds the asset library configuration of an account: the AK/SK
// pair the Ark asset OpenAPI is signed with and the endpoint it is sent to
// (docs/PLUGIN-VOLCENGINE-ARK.md §4.5).
//
// The asset library is OFF by default and per account: an account with no
// access_key and no secret_key is a perfectly valid Ark account that simply
// does not take part in it. That is why the form does not require the pair
// and AssetConfigOf answers (nil, nil) for such an account instead of an
// error.

import (
	"encoding/json"
	"fmt"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
)

// Account form fields of the asset library.
const (
	// FieldAccessKey is the Volcengine account's access key id. It lives in
	// the credentials (it is not in settingsFields, so the core stores it
	// encrypted) but is NOT in sensitiveFields: an access key id is an
	// identifier, and masking it would stop an operator from telling two
	// accounts apart in the form.
	FieldAccessKey = "access_key"
	// FieldSecretKey is the matching secret. It is in sensitiveFields, so
	// the console never shows it again after saving.
	FieldSecretKey = "secret_key"
	// FieldAssetBaseURL is the asset OpenAPI endpoint (settings).
	FieldAssetBaseURL = "asset_base_url"
	// FieldAssetEndpoint is a relay's complete asset URL or path below base_url.
	FieldAssetEndpoint = "asset_endpoint"
	// FieldAssetRegion is the signing region (settings).
	FieldAssetRegion = "asset_region"
)

const (
	// DefaultAssetBaseURL is Ark's control plane, which serves the asset
	// library OpenAPI. NOTE the host: it is NOT the Ark API host of
	// DefaultBaseURL (ark.cn-beijing.volces.com). The data plane
	// (/api/v3/chat/completions ...) and the control plane
	// (POST / with ?Action=...) are two different services on two different
	// names, and mixing them up yields a signed request to a host that has
	// no idea what Action means.
	DefaultAssetBaseURL = "https://ark.cn-beijing.volcengineapi.com"
	// DefaultAssetRegion is the signing region used when an account names
	// none.
	DefaultAssetRegion = "cn-beijing"
	// BytePlusAssetRegion is the region of the overseas (BytePlus) stack;
	// operators pointing asset_base_url at it have to set this too, because
	// the region is part of the V4 credential scope and a wrong one is
	// rejected upstream with a signature error, not with a region error.
	BytePlusAssetRegion = "ap-southeast-1"

	// AssetServiceName / AssetAPIVersion are the service and version of
	// every asset library Action (volcengine top-level API convention).
	AssetServiceName = "ark"
	AssetAPIVersion  = "2024-01-01"
)

// AssetConfig is one account's asset library configuration.
type AssetConfig struct {
	AccountID int64
	Name      string
	AccessKey string
	SecretKey string
	// BaseURL is the asset OpenAPI endpoint without a trailing slash.
	BaseURL string
	Region  string
}

// assetRegionOK reports whether r is a plausible volcengine region id. It is
// part of the V4 credential scope, so it must not carry separators that
// would let a value smuggle extra scope segments into the signature.
func assetRegionOK(r string) bool {
	if r == "" || len(r) > 64 {
		return false
	}
	for _, c := range r {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// assetKeyOK reports whether k is printable ASCII without spaces, 8-256
// bytes. Volcengine AK/SK are base64-ish fixed-length strings; the bound is
// deliberately loose (STS-issued keys are longer).
func assetKeyOK(k string) bool {
	if len(k) < 8 || len(k) > 256 {
		return false
	}
	for _, r := range k {
		if r <= ' ' || r > '~' {
			return false
		}
	}
	return true
}

// assetFields reads the four asset fields out of a credentials/settings
// pair. Values are trimmed; the keys are looked up in the object they belong
// to first, then in the other one, because ValidateCredentials sees a single
// merged object before the core splits it (server account/creds.go split()).
func assetFields(credentialsJSON, settingsJSON string) (ak, sk, base, region string, err error) {
	creds, err := apikey.DecodeObject(credentialsJSON)
	if err != nil {
		return "", "", "", "", fmt.Errorf("credentials: %w", err)
	}
	settings, err := apikey.DecodeObject(settingsJSON)
	if err != nil {
		return "", "", "", "", fmt.Errorf("settings: %w", err)
	}
	str := func(field string, first, second map[string]any) (string, error) {
		v, ok := apikey.Lookup(field, first, second)
		if !ok {
			return "", nil
		}
		s, isStr := v.(string)
		if !isStr {
			return "", fmt.Errorf("%s must be a string", field)
		}
		return strings.TrimSpace(s), nil
	}
	if ak, err = str(FieldAccessKey, creds, settings); err != nil {
		return
	}
	if sk, err = str(FieldSecretKey, creds, settings); err != nil {
		return
	}
	if base, err = str(FieldAssetBaseURL, settings, creds); err != nil {
		return
	}
	region, err = str(FieldAssetRegion, settings, creds)
	return ak, sk, base, region, err
}

// AssetConfigOf returns the asset library configuration of an account read
// through Host.Account, or (nil, nil) when the account does not take part in
// the asset library (no access_key and no secret_key).
func AssetConfigOf(acc *pluginv1.Account) (*AssetConfig, error) {
	if acc == nil {
		return nil, fmt.Errorf("no account")
	}
	id, creds, settings := acc.GetId(), acc.GetCredentialsJson(), acc.GetSettingsJson()
	ak, sk, base, region, err := assetFields(creds, settings)
	if err != nil {
		return nil, fmt.Errorf("account %d: %w", id, err)
	}
	if ak == "" && sk == "" {
		return nil, nil
	}
	if ak == "" || sk == "" {
		// Reachable for an account stored before this version, or written
		// past the form: ValidateCredentials refuses the half-filled pair.
		return nil, fmt.Errorf("account %d: the asset library needs both access_key and secret_key", id)
	}
	url, err := effectiveAssetEndpoint(acc.GetType(), creds, settings, base)
	if err != nil {
		return nil, fmt.Errorf("account %d: asset_base_url: %w", id, err)
	}
	if url == "" {
		return nil, nil
	}
	if region == "" {
		region = DefaultAssetRegion
	}
	if !assetRegionOK(region) {
		return nil, fmt.Errorf("account %d: asset_region %q is not a region id", id, region)
	}
	return &AssetConfig{AccountID: id, Name: acc.GetName(), AccessKey: ak, SecretKey: sk, BaseURL: url, Region: region}, nil
}

// AssetEnabled reports whether an account's SETTINGS carry asset library
// configuration (an endpoint or a region). It is NOT "the asset library is
// on": the AK/SK live in the encrypted credentials, which ListAccounts
// deliberately does not return, and an account may perfectly well use the
// default endpoint and region with no settings at all. Callers that need the
// real answer have to read the credentials, which is audited.
func AssetEnabled(settingsJSON string) bool {
	_, _, base, region, err := assetFields("", settingsJSON)
	endpoint, endpointErr := assetSetting("", settingsJSON, FieldAssetEndpoint)
	return err == nil && endpointErr == nil && (base != "" || region != "" || endpoint != "")
}

func assetSetting(credentialsJSON, settingsJSON, field string) (string, error) {
	for _, raw := range []string{settingsJSON, credentialsJSON} {
		obj, err := apikey.DecodeObject(raw)
		if err != nil {
			return "", err
		}
		if value, exists := obj[field]; exists && value != nil {
			s, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("%s must be a string", field)
			}
			return strings.TrimSpace(s), nil
		}
	}
	return "", nil
}

// Explicit legacy URLs keep their meaning. A relay never silently falls back
// to the official control plane when no asset endpoint was configured.
func effectiveAssetEndpoint(accountType, credentialsJSON, settingsJSON, legacyBase string) (string, error) {
	if accountType == AccountTypeRelay {
		endpoint, err := assetSetting(credentialsJSON, settingsJSON, FieldAssetEndpoint)
		if err != nil {
			return "", err
		}
		if endpoint != "" {
			base, err := assetSetting(credentialsJSON, settingsJSON, "base_url")
			if err != nil {
				return "", err
			}
			return resolveEndpoint(base, endpoint)
		}
		if legacyBase == "" {
			return "", nil
		}
	}
	// For official accounts, empty legacyBase means use the default
	if legacyBase == "" {
		legacyBase = DefaultAssetBaseURL
	}
	return apikey.NormalizeBaseURL(legacyBase)
}

// validateAssetFields adds the asset library field errors to errs: the AK/SK
// pair has to be complete or completely empty, and the endpoint and region
// have to be usable in a V4 signature.
func validateAssetFields(errs pluginsdk.FieldErrors, credentialsJSON, settingsJSON string) pluginsdk.FieldErrors {
	ak, sk, base, region, err := assetFields(credentialsJSON, settingsJSON)
	if err != nil {
		return errs.Add("", "type", err.Error()+" / 字段类型不正确")
	}
	switch {
	case ak == "" && sk == "":
		// The asset library stays off for this account: legal, not an error.
	case ak == "":
		errs = errs.Add(FieldAccessKey, "required",
			"access_key is required when secret_key is set / 填写了 Secret Key 就必须填写 Access Key")
	case sk == "":
		errs = errs.Add(FieldSecretKey, "required",
			"secret_key is required when access_key is set / 填写了 Access Key 就必须填写 Secret Key")
	default:
		if !assetKeyOK(ak) {
			errs = errs.Add(FieldAccessKey, "pattern",
				"access_key must be 8-256 printable characters without spaces / Access Key 须为 8-256 个不含空格的可见字符")
		}
		if !assetKeyOK(sk) {
			errs = errs.Add(FieldSecretKey, "pattern",
				"secret_key must be 8-256 printable characters without spaces / Secret Key 须为 8-256 个不含空格的可见字符")
		}
	}
	if base != "" {
		if _, err := apikey.NormalizeBaseURL(base); err != nil {
			errs = errs.Add(FieldAssetBaseURL, "format",
				"asset_base_url must be an http(s) URL without credentials, query, fragment or whitespace / asset_base_url 必须是不含账号、查询参数、空白字符的 http(s) 地址")
		}
	}
	if region != "" && !assetRegionOK(region) {
		errs = errs.Add(FieldAssetRegion, "pattern",
			"asset_region must be a region id such as cn-beijing / asset_region 须为 cn-beijing 这样的区域 id")
	}
	// An endpoint or a region without the key pair is a half-configured
	// account: warning-free silence here would leave an operator believing
	// the asset library is on.
	if ak == "" && sk == "" && (base != "" || region != "") {
		errs = errs.Add(FieldAccessKey, "required",
			"asset_base_url/asset_region only take effect with an access_key and secret_key / 填了素材端点或区域就必须填 Access Key 与 Secret Key")
	}
	return errs
}

// normalizeAssetFields rewrites the four asset fields of an already
// normalized credentials/settings object (trimmed values, asset_base_url
// normalized). Keys that are absent stay absent: the core replaces the whole
// object, so inventing keys here would write fields the operator never
// filled in.
func normalizeAssetFields(objJSON string) string {
	obj, err := apikey.DecodeObject(objJSON)
	if err != nil || len(obj) == 0 {
		return objJSON
	}
	changed := false
	for _, f := range []string{FieldAccessKey, FieldSecretKey, FieldAssetRegion, FieldAssetEndpoint} {
		if v, ok := obj[f]; ok {
			if s, isStr := v.(string); isStr {
				obj[f] = strings.TrimSpace(s)
				changed = true
			}
		}
	}
	if v, ok := obj[FieldAssetBaseURL]; ok {
		if s, isStr := v.(string); isStr {
			// An explicitly empty value stays empty instead of being filled
			// with the default: guardedSettings treats "absent or empty" as
			// allowed, and an account that never opted in must not end up
			// carrying an endpoint it did not ask for.
			if n, err := apikey.NormalizeBaseURL(s); err == nil && strings.TrimSpace(s) != "" {
				obj[FieldAssetBaseURL] = n
			} else {
				obj[FieldAssetBaseURL] = strings.TrimSpace(s)
			}
			changed = true
		}
	}
	if !changed {
		return objJSON
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return objJSON
	}
	return string(b)
}

// validateWithAssets runs the shared API-key validation and then the asset
// library rules on top of it. The spec depends on the account type being
// created: the two differ in whether base_url has a default (assets.go's own
// rules are the same for both).
func (p *Plugin) validateWithAssets(in *pluginv1.ValidateCredentialsRequest) *pluginv1.ValidateCredentialsResponse {
	sp, err := specFor(in.GetAccountType())
	if err != nil {
		return &pluginv1.ValidateCredentialsResponse{Errors: pluginsdk.FieldErrors(nil).Add(
			"account_type", "unsupported",
			fmt.Sprintf("unsupported account type %q / 不支持的账号类型", in.GetAccountType()))}
	}
	resp := sp.Validate(in)
	errs := pluginsdk.FieldErrors(resp.GetErrors())
	if len(errs) > 0 {
		// The account type itself was rejected: further field errors would
		// be noise about a form that will not be submitted anyway.
		if errs[0].GetField() == "account_type" {
			return resp
		}
	}
	errs = validateAssetFields(errs, in.GetCredentialsJson(), in.GetSettingsJson())
	if in.GetAccountType() == AccountTypeRelay {
		errs = validateRelayBaseURL(errs, in.GetCredentialsJson(), in.GetSettingsJson())
		errs = validatePrefixFields(errs, in.GetSettingsJson())
	}
	endpoint, endpointErr := assetSetting(in.GetCredentialsJson(), in.GetSettingsJson(), FieldAssetEndpoint)
	if endpointErr != nil {
		errs = errs.Add(FieldAssetEndpoint, "type", endpointErr.Error())
	} else if endpoint != "" {
		if in.GetAccountType() != AccountTypeRelay {
			errs = errs.Add(FieldAssetEndpoint, "unsupported", "asset_endpoint is only available for Doubao video accounts / 素材端点自定义仅适用于豆包视频账号")
		} else {
			if _, err := effectiveAssetEndpoint(AccountTypeRelay, in.GetCredentialsJson(), in.GetSettingsJson(), ""); err != nil {
				errs = errs.Add(FieldAssetEndpoint, "format", err.Error())
			}
			ak, sk, _, _, _ := assetFields(in.GetCredentialsJson(), in.GetSettingsJson())
			if ak == "" && sk == "" {
				errs = errs.Add(FieldAccessKey, "required", "asset_endpoint requires access_key and secret_key / 填写素材端点需要 Access Key 和 Secret Key")
			}
		}
	}
	if len(errs) > 0 {
		return &pluginv1.ValidateCredentialsResponse{Errors: errs}
	}
	return &pluginv1.ValidateCredentialsResponse{
		NormalizedCredentialsJson: normalizeAssetFields(resp.GetNormalizedCredentialsJson()),
		NormalizedSettingsJson:    normalizePrefixFields(normalizeAssetFields(resp.GetNormalizedSettingsJson())),
	}
}
