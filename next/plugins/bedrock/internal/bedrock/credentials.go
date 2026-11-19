package bedrock

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/accountform"
	"github.com/Sub2API-Devs/sup2api/next/sdk/reqsign"
)

// object is a decoded JSON object: credentials or settings.
type object = accountform.Object

var fieldError = accountform.FieldError

// Credential and settings fields (the account form, ui/native/src/fields.ts).
const (
	fieldAPIKey          = "api_key"
	fieldAccessKeyID     = "access_key_id"
	fieldSecretAccessKey = "secret_access_key"
	fieldSessionToken    = "session_token"
	fieldRegion          = "region"
	fieldProfilePrefix   = "inference_profile_prefix"
	// fieldSessionExpiresAt is a reminder for the administrator only: nothing
	// reads it to decide anything.
	fieldSessionExpiresAt = "session_expires_at"
)

var (
	// regionPattern is strict because the region becomes part of the host.
	regionPattern      = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d$`)
	accessKeyIDPattern = regexp.MustCompile(`^[A-Z0-9]{16,128}$`)
	visiblePattern     = regexp.MustCompile(`^[!-~]+$`)
)

// visible accepts a secret pasted whole: min to max visible characters.
func visible(min, max int) func(string) bool {
	return func(s string) bool { return len(s) >= min && len(s) <= max && visiblePattern.MatchString(s) }
}

// The shapes of the signing credentials (the account form checks the same
// bounds). AWS defines them and they take part in SigV4.
var (
	validAccessKeyID  = accessKeyIDPattern.MatchString
	validSecretKey    = visible(1, 1024)
	validSessionToken = visible(1, 8192)
)

// unsupportedPartition reports a region outside the partitions RuntimeHost
// can address. It only knows amazonaws.com, which serves the commercial and
// the GovCloud regions; the China regions (cn-*) are under amazonaws.com.cn
// and the isolated ones (us-iso-*, us-isob-*, us-isof-*, eu-isoe-*) under
// domains of their own, all with accounts separate from the global ones.
func unsupportedPartition(region string) bool {
	geography, rest, _ := strings.Cut(region, "-")
	return geography == "cn" || strings.HasPrefix(rest, "iso")
}

// unsupportedPartitionMessage is what an account in such a region is told.
const unsupportedPartitionMessage = "the AWS China regions (cn-*) are not supported, nor are the isolated regions (us-iso-*, us-isob-* and the like): their endpoints are not under amazonaws.com" +
	" / 不支持 AWS 中国区（cn-*）和隔离分区（us-iso-*、us-isob-* 等）：其接入地址不在 amazonaws.com 下"

// NoProfilePrefix is the inference_profile_prefix of an account that sends
// model ids as they are.
const NoProfilePrefix = "none"

// DefaultProfilePrefix is the inference_profile_prefix of an account that
// names none: the global cross-region inference profile. On bedrock-runtime
// the current Claude models are invoked through an inference profile (their
// AWS model cards list no in-Region endpoint, or one in a handful of
// regions), and global is the one profile every model of the default mapping
// but Opus 4.1 has, from every commercial region.
const DefaultProfilePrefix = "global"

// ProfilePrefixes are the values of inference_profile_prefix: NoProfilePrefix
// or the cross-region inference prefix put before model ids.
var ProfilePrefixes = []string{NoProfilePrefix, "us", "eu", "apac", "jp", "au", "in", "global"}

// auth is how the accounts of a type authorize their requests.
type auth struct {
	// signed: the plugin signs the request with the account's access key
	// (SigV4). Otherwise the account's API key is sent as a bearer token.
	signed bool
	// session: the access key is temporary and comes with a session token.
	session bool
}

var auths = map[string]auth{
	TypeAPIKey:    {},
	TypeAccessKey: {signed: true},
	TypeSTS:       {signed: true, session: true},
}

// keys are the keys the form of the type sends, credentials and settings;
// ValidateCredentials refuses any other.
func (a auth) keys() []string {
	keys := []string{fieldRegion, fieldProfilePrefix}
	switch {
	case !a.signed:
		return append(keys, fieldAPIKey)
	case a.session:
		return append(keys, fieldAccessKeyID, fieldSecretAccessKey, fieldSessionToken, fieldSessionExpiresAt)
	}
	return append(keys, fieldAccessKeyID, fieldSecretAccessKey)
}

// credential checks a credential field that must be present and valid.
func credential(creds object, field string, valid func(string) bool, required, malformed string) *pluginv1.FieldError {
	switch v := creds.Str(field); {
	case v == "":
		return fieldError(field, "required", required)
	case !valid(v):
		return fieldError(field, "pattern", malformed)
	}
	return nil
}

// checkCredentials checks the credential fields of a type.
func (a auth) checkCredentials(creds object) *pluginv1.FieldError {
	if !a.signed {
		// Any non-blank key, as apikey.Spec takes an api_key
		// (DESIGN-2026-11-20 §5.1): it only goes into the Authorization
		// header, and a value holding control characters is never sent
		// (the core and net/http refuse such a header).
		if creds.Str(fieldAPIKey) == "" {
			return fieldError(fieldAPIKey, "required", "API key is required / 请填写 API Key")
		}
		return nil
	}
	if bad := credential(creds, fieldAccessKeyID, validAccessKeyID,
		"access key ID is required / 请填写 Access Key ID", "not an AWS access key ID (16-128 upper-case letters and digits) / 不是合法的 AWS Access Key ID（16–128 位大写字母和数字）"); bad != nil {
		return bad
	}
	if bad := credential(creds, fieldSecretAccessKey, validSecretKey,
		"secret access key is required / 请填写 Secret Access Key", "secret access key must not contain spaces or line breaks / Secret Access Key 不能包含空格或换行"); bad != nil {
		return bad
	}
	if !a.session {
		return nil
	}
	return credential(creds, fieldSessionToken, validSessionToken,
		"session token is required / 请填写 Session Token", "session token must not contain spaces or line breaks / Session Token 不能包含空格或换行")
}

// checkSettings checks the settings every type shares and fills in the
// defaults: DefaultRegion and DefaultProfilePrefix.
func checkSettings(settings object) *pluginv1.FieldError {
	for field, def := range map[string]string{fieldRegion: DefaultRegion, fieldProfilePrefix: DefaultProfilePrefix} {
		if settings.Str(field) == "" {
			settings[field] = def
		}
	}
	switch region := settings.Str(fieldRegion); {
	case !regionPattern.MatchString(region):
		return fieldError(fieldRegion, "pattern", "not an AWS region, e.g. us-east-1 / 不是合法的 AWS 区域，例如 us-east-1")
	case unsupportedPartition(region):
		return fieldError(fieldRegion, "unsupported", unsupportedPartitionMessage)
	case !slices.Contains(ProfilePrefixes, settings.Str(fieldProfilePrefix)):
		return fieldError(fieldProfilePrefix, "enum", "unknown inference profile prefix / 未知的跨区域推理前缀")
	}
	return nil
}

// checkExpiryHint checks the expiry an administrator noted for temporary
// credentials. Only a submitted form is held to it: a stored account serves
// requests whatever the hint says.
func checkExpiryHint(settings object) *pluginv1.FieldError {
	at := settings.Str(fieldSessionExpiresAt)
	if at == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		return fieldError(fieldSessionExpiresAt, "format", "not a date and time / 不是合法的日期时间")
	}
	return nil
}

// authOf is the auth of an account type, or the error refusing the type.
func authOf(typ string) (auth, *pluginv1.FieldError) {
	a, ok := auths[typ]
	if !ok {
		return a, fieldError("account_type", "unsupported", "unsupported account type "+strconv.Quote(typ)+" / 不支持的账号类型")
	}
	return a, nil
}

// check validates what requests are built from: the credentials of an
// account of the type, its region and its prefix. It is the same for a
// submitted form and for a stored account about to serve a request.
func (a auth) check(creds, settings object) *pluginv1.FieldError {
	if bad := a.checkCredentials(creds); bad != nil {
		return bad
	}
	return checkSettings(settings)
}

// ValidateCredentials implements pluginsdk.Platform. An account without a
// region or a prefix choice is stored with the defaults.
func (p *Plugin) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	form, refused := accountform.DecodeForm(in)
	if refused != nil {
		return refused, nil
	}
	a, bad := authOf(in.GetAccountType())
	if bad != nil {
		return accountform.Invalid(bad), nil
	}
	// Keys the form does not send are refused (code "unknown").
	if refused := form.Only(a.keys()...); refused != nil {
		return refused, nil
	}
	bad = a.check(form.Creds, form.Settings)
	if bad == nil {
		bad = checkExpiryHint(form.Settings)
	}
	if bad != nil {
		return accountform.Invalid(bad), nil
	}
	return form.Normalized(), nil
}

// account is what a request needs of a stored account.
type account struct {
	auth   auth
	creds  object
	region string
	// prefix is the inference profile prefix put before model ids
	// (NoProfilePrefix: none).
	prefix string
}

// readAccount decodes an account of one of the plugin's types, with the
// status codes vertex answers: INVALID_ARGUMENT when the credentials or the
// settings are not a JSON object, FAILED_PRECONDITION when requests cannot
// be built for the account (a type that is not the plugin's, a missing or
// malformed credential, region or prefix). Either way the gateway moves on
// to another account.
func readAccount(acc *pluginv1.Account) (account, error) {
	creds, settings, err := accountform.DecodeAccount(acc)
	if err != nil {
		return account{}, err
	}
	a, bad := authOf(acc.GetType())
	if bad == nil {
		bad = a.check(creds, settings)
	}
	if bad != nil {
		return account{}, status.Errorf(codes.FailedPrecondition, "account %d: %s: %s", acc.GetId(), bad.GetField(), bad.GetMessage())
	}
	return account{auth: a, creds: creds, region: settings.Str(fieldRegion), prefix: settings.Str(fieldProfilePrefix)}, nil
}

// authorize returns the headers that authorize req, a request of the
// account that will send exactly body: the API key as a bearer token, or
// the SigV4 signature of req made at now (req's Content-Type included,
// since it is signed). Signing settles req's URL and host; they must not
// change afterwards.
func (a account) authorize(req *http.Request, body []byte, now time.Time) (http.Header, error) {
	if !a.auth.signed {
		return http.Header{"Authorization": {"Bearer " + a.creds.Str(fieldAPIKey)}}, nil
	}
	c := reqsign.AWSCredentials{
		Region: a.region, Service: SigningService,
		AccessKeyID: a.creds.Str(fieldAccessKeyID), SecretAccessKey: a.creds.Str(fieldSecretAccessKey),
	}
	if a.auth.session {
		c.SessionToken = a.creds.Str(fieldSessionToken)
	}
	if err := reqsign.SignAWSSigV4(req, body, c, now); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return reqsign.AWSSigV4Headers(req), nil
}
