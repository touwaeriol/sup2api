package vertex

import (
	"context"
	"regexp"
	"strconv"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/accountform"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// Credential and settings fields besides the key file (keyKinds).
const (
	fieldAPIKey      = "api_key"
	fieldAccessToken = "access_token"
	fieldExpiresAt   = "expires_at"
	// fieldTokenFor holds the fingerprint of the key the stored access token
	// was issued for.
	fieldTokenFor = "token_for"
	fieldProject  = "project_id"
	fieldLocation = "location"
)

// maxKeyFileLength bounds the key file of a submitted form, in characters
// (the bound the account form has always had).
const maxKeyFileLength = 32768

// expiredAt is the expires_at of an account that has no access token yet: a
// readable expiry in the past, so the core's next refresh pass fetches the
// first token (it skips accounts without a readable expiry, CONTRACTS §48.3).
const expiredAt = 1

var (
	projectPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	locationPattern = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)
)

// apiKeySpec checks the api_key of an API key account the way every API key
// form does: any non-blank string, saved trimmed. The type has no base_url;
// project_id and location are its other keys (checked by target), any other
// is refused.
var apiKeySpec = apikey.Spec{AccountType: TypeAPIKey, NoBaseURL: true,
	Fields: fieldcheck.Fields{fieldProject: fieldcheck.Any(), fieldLocation: fieldcheck.Any()}}

// expiry reads expires_at the way the core does.
var expiry = &manifest.AccountRefresh{}

// object is a decoded JSON object: credentials or settings. Numbers are kept
// as written, so re-encoding what ValidateCredentials decoded changes none
// (the token expiry, large integers).
type object = accountform.Object

var (
	fieldError = accountform.FieldError
	invalid    = accountform.Invalid
)

// target is where an account's requests go.
type target struct {
	Project  string
	Location string
}

func readTarget(settings object) target {
	return target{Project: settings.Str(fieldProject), Location: settings.Str(fieldLocation)}
}

// express reports the address without project and location, which only an
// API key can use.
func (t target) express() bool { return t.Project == "" && t.Location == "" }

// check refuses a target requests cannot be built for. expressOK allows
// leaving both parts empty.
func (t target) check(expressOK bool) *pluginv1.FieldError {
	missing := "is required / 必填"
	if expressOK {
		missing = "is required when the other of project ID and location is set: fill in both or neither / 项目 ID 和区域要么都填，要么都不填"
	}
	switch {
	case expressOK && t.express():
		return nil
	case t.Project == "":
		return fieldError(fieldProject, "required", "project ID "+missing)
	case t.Location == "":
		return fieldError(fieldLocation, "required", "location "+missing)
	case !projectPattern.MatchString(t.Project):
		return fieldError(fieldProject, "pattern", "not a Google Cloud project ID / 不是合法的 Google Cloud 项目 ID")
	case !locationPattern.MatchString(t.Location):
		return fieldError(fieldLocation, "pattern", "not a Vertex AI location / 不是合法的 Vertex AI 区域")
	}
	return nil
}

// ---------------------------------------------------------------- validation

// ValidateCredentials implements pluginsdk.Platform. Besides checking the
// submitted fields it normalizes them (the core stores the normalized form):
//
//   - a key file account without project ID takes the one its file names,
//     and without location takes "global";
//   - a key file account whose stored access token is missing, was issued
//     for another key or has no readable expiry loses the token and gets
//     expires_at 1, which makes the core fetch a token on its next refresh
//     pass (bootstrapToken).
func (p *Plugin) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	form, refused := accountform.DecodeForm(in)
	if refused != nil {
		return refused, nil
	}
	typ := in.GetAccountType()
	if typ == TypeAPIKey {
		return validateAPIKeyAccount(in, form.Settings), nil
	}
	kind, keyed := keyKinds[typ]
	if !keyed {
		return invalid(fieldError("account_type", "unsupported", "unsupported account type "+strconv.Quote(typ)+" / 不支持的账号类型")), nil
	}
	// The token state the core writes on renewal comes back with every edit.
	if refused := form.Only(kind.field, fieldAccessToken, fieldExpiresAt, fieldTokenFor, fieldProject, fieldLocation); refused != nil {
		return refused, nil
	}
	if bad := validateKeyAccount(kind, form.Creds, form.Settings); bad != nil {
		return invalid(bad), nil
	}
	return form.Normalized(), nil
}

// validateKeyAccount checks a key file account and fills in what the file
// implies: the project, the default location and the token state.
func validateKeyAccount(kind keyKind, creds, settings object) *pluginv1.FieldError {
	raw := creds.Str(kind.field)
	switch {
	case raw == "":
		return fieldError(kind.field, "required", "paste the JSON key file / 请粘贴 JSON 密钥文件的内容")
	case utf8.RuneCountInString(raw) > maxKeyFileLength:
		return fieldError(kind.field, "too_long", "the key file must be at most "+strconv.Itoa(maxKeyFileLength)+" characters / 密钥文件最多 "+strconv.Itoa(maxKeyFileLength)+" 个字符")
	}
	key, err := kind.parse(raw)
	if err != nil {
		return fieldError(kind.field, "invalid", err.Error())
	}
	if settings.Str(fieldProject) == "" && key.project() != "" {
		settings[fieldProject] = key.project()
	}
	if settings.Str(fieldLocation) == "" {
		settings[fieldLocation] = LocationGlobal
	}
	if bad := readTarget(settings).check(false); bad != nil {
		return bad
	}
	bootstrapToken(creds, key.fingerprint())
	return nil
}

// bootstrapToken keeps a stored access token only when it was issued for the
// key with this fingerprint and has a readable expiry. Otherwise the token is
// dropped and expires_at becomes expiredAt, so the next refresh pass (or the
// refresh right after the save) fetches one for the submitted key.
func bootstrapToken(creds object, keyFingerprint string) {
	_, readable := expiry.ExpiresAt(creds)
	if readable && creds.Str(fieldAccessToken) != "" && creds.Str(fieldTokenFor) == keyFingerprint {
		return
	}
	delete(creds, fieldAccessToken)
	delete(creds, fieldTokenFor)
	creds[fieldExpiresAt] = expiredAt
}

// validateAPIKeyAccount checks an API key account: the key by apiKeySpec
// (which also trims it), and that project and location are given together
// (the full address) or not at all (the express address).
func validateAPIKeyAccount(in *pluginv1.ValidateCredentialsRequest, settings object) *pluginv1.ValidateCredentialsResponse {
	resp := apiKeySpec.Validate(in)
	if len(resp.GetErrors()) > 0 {
		return resp
	}
	if bad := readTarget(settings).check(true); bad != nil {
		return invalid(bad)
	}
	return resp
}

// ---------------------------------------------------------------- stored accounts

// account is what a request needs of a stored account.
type account struct {
	typ    string
	creds  object
	target target
}

// readAccount decodes an account of one of the plugin's types and checks
// that requests can be addressed for it.
func readAccount(acc *pluginv1.Account) (account, error) {
	a := account{typ: acc.GetType()}
	_, keyed := keyKinds[a.typ]
	if !keyed && a.typ != TypeAPIKey {
		return a, status.Errorf(codes.FailedPrecondition, "account %d: unsupported account type %q", acc.GetId(), a.typ)
	}
	creds, settings, err := accountform.DecodeAccount(acc)
	if err != nil {
		return a, err
	}
	a.creds, a.target = creds, readTarget(settings)
	if bad := a.target.check(!keyed); bad != nil {
		return a, status.Errorf(codes.FailedPrecondition, "account %d: %s: %s", acc.GetId(), bad.GetField(), bad.GetMessage())
	}
	return a, nil
}

// keyFile parses the key file of a key file account.
func (a account) keyFile() (keyFile, error) {
	kind, keyed := keyKinds[a.typ]
	if !keyed {
		return nil, status.Errorf(codes.InvalidArgument, "account type %q has no credentials to refresh", a.typ)
	}
	key, err := kind.parse(a.creds.Str(kind.field))
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, kind.field+": "+err.Error())
	}
	return key, nil
}
