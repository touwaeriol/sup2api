// Package codex implements the codex plugin: two account types for the
// built-in openai platform that use the operator's OWN ChatGPT subscription
// (docs/PLUGIN-CODEX.md). They differ only in how the Codex CLI's tokens are
// obtained; afterwards both go to the ChatGPT Codex backend and refresh with
// the Codex client:
//
//   - codex_device: the Codex device code login (the public flow of the Codex
//     CLI's --device-auth);
//   - codex_oauth: Sign in with Codex, the Codex CLI's browser login
//     (authorization code + PKCE).
//
// Requests identify themselves by default truthfully: originator "sup2api"
// and User-Agent "sup2api-codex/<version>"; the operator may set others per
// account or in the plugin settings (agent.go). The plugin does not imitate
// the Codex CLI (no x-codex-* headers or TLS fingerprint) and does not add
// the Codex system prompt.
package codex

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/paramoverride"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/accountform"
)

const (
	// Version is the plugin version (manifest.json "version"); it is part of
	// the User-Agent every request sends.
	Version = "0.2.0"
	// UserAgent and Originator name sup2api as the client, truthfully: the
	// built-in values of the client headers (agent.go).
	UserAgent  = "sup2api-codex/" + Version
	Originator = "sup2api"

	// Account types (manifest accountTypes[].id).
	TypeDevice = "codex_device"
	TypeOAuth  = "codex_oauth"

	// PlatformID is the built-in platform both types serve.
	PlatformID = "openai"
	// Upstream protocols BuildUpstream speaks (manifest endpoints
	// "responses" and "responses_websocket"; chat completions reach the
	// plugin converted to Responses by the core).
	ProtocolResponses   = "openai.responses"
	ProtocolResponsesWS = "openai.responses_ws"

	// Default service roots; tests point them at fake servers (Endpoints).
	DefaultAuthBase    = "https://auth.openai.com"
	DefaultChatGPTBase = "https://chatgpt.com"

	// CodexClientID is the Codex CLI's public OAuth client (both logins and
	// the refresh).
	CodexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// RefreshScope is the scope of a refresh.
	RefreshScope = "openid profile email"
	// CodexRedirectURI and CodexScope are Sign in with Codex's: the redirect
	// registered for the Codex client and the scope the Codex CLI asks for.
	CodexRedirectURI = "http://localhost:1455/auth/callback"
	CodexScope       = "openid profile email offline_access"

	// WebSocketBeta opts the upstream connection into the Responses
	// WebSocket mode.
	WebSocketBeta = "responses_websockets=2026-02-06"

	// DefaultTestModel is used by BuildTestRequest when no model is given
	// (the smallest model of manifest defaultModels: the test uses the
	// owner's plan).
	DefaultTestModel = "gpt-6-luna"
	// ImageChatModel is the chat model driving the image_generation tool of
	// an image request: manifest adapted.images_*.model, which the core puts
	// at the top of the converted request, and the model of an image test.
	ImageChatModel = "gpt-6-luna"
	// Client protocols the core converts to Responses (manifest adapted).
	ProtocolChat       = "openai.chat"
	ProtocolImages     = "openai.images"
	ProtocolImagesEdit = "openai.images_edit"
)

// Endpoints are the service roots the plugin talks to (no trailing slash).
type Endpoints struct {
	// Auth is the OpenAI issuer: device code, token and authorize endpoints.
	Auth string
	// ChatGPT serves the Codex backend (the upstream).
	ChatGPT string
}

// DefaultEndpoints are the production roots.
var DefaultEndpoints = Endpoints{Auth: DefaultAuthBase, ChatGPT: DefaultChatGPTBase}

// Plugin is the codex plugin. It implements pluginsdk.Platform (Execute
// through UpstreamBuilder), CredentialRefresher, HTTP (authorization and
// usage reset routes) and Initializer. It lists no models: the Codex
// backend's model list is an internal endpoint of the Codex CLI, so the
// console keeps the manifest defaults.
type Plugin struct {
	*pluginsdk.Router
	host pluginsdk.Host
	ep   Endpoints
	// quota are the quota headers ClassifyError reads (quota.go).
	quota []manifest.QuotaHeader
	now   func() time.Time
	// log is the host log (Init); it discards until then. Never log tokens,
	// authorization codes, PKCE verifiers, state or session ids.
	log *slog.Logger
	// settings are the plugin settings (Configure).
	settings atomic.Pointer[Settings]
}

// New returns the plugin talking to the production endpoints.
func New(manifestJSON []byte) (*Plugin, error) {
	return NewWithEndpoints(DefaultEndpoints, manifestJSON)
}

// NewWithEndpoints returns the plugin talking to ep. manifestJSON is the
// plugin's own manifest.json: both account types declare the same quota
// headers (asserted by manifest_test), and ClassifyError reads codex_device's
// (quota.go).
func NewWithEndpoints(ep Endpoints, manifestJSON []byte) (*Plugin, error) {
	quota, err := quotaHeaders(manifestJSON)
	if err != nil {
		return nil, err
	}
	p := &Plugin{
		Router: pluginsdk.NewRouter(),
		ep: Endpoints{
			Auth:    strings.TrimRight(ep.Auth, "/"),
			ChatGPT: strings.TrimRight(ep.ChatGPT, "/"),
		},
		quota: quota,
		now:   time.Now,
		log:   slog.New(slog.DiscardHandler),
	}
	p.settings.Store(&Settings{})
	p.Handle(http.MethodPost, "/auth/device/start", p.handleDeviceStart)
	p.Handle(http.MethodPost, "/auth/device/poll", p.handleDevicePoll)
	p.Handle(http.MethodPost, "/auth/codex/start", p.handleCodexStart)
	p.Handle(http.MethodPost, "/auth/codex/exchange", p.handleCodexExchange)
	p.Handle(http.MethodPost, "/reset-credits/list", p.handleResetList)
	p.Handle(http.MethodPost, "/reset-credits/consume", p.handleResetConsume)
	p.Handle(http.MethodPost, "/reset-credits/summary", p.handleResetSummary)
	return p, nil
}

// Init implements pluginsdk.Initializer.
func (p *Plugin) Init(_ context.Context, h pluginsdk.Host) error {
	p.host = h
	if l := h.Logger(); l != nil {
		p.log = l
	}
	return nil
}

// ---------------------------------------------------------------- credentials

// credentials are the stored fields BuildUpstream and the refresh
// need. The full set is documented in docs/PLUGIN-CODEX.md §4.
type credentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	// UserAgent and Originator are the account's own client headers, ""
	// for none (agent.go).
	UserAgent  string `json:"user_agent"`
	Originator string `json:"originator"`
}

func parseCredentials(raw string) (credentials, error) {
	var c credentials
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	err := json.Unmarshal([]byte(raw), &c)
	c.AccessToken = strings.TrimSpace(c.AccessToken)
	c.RefreshToken = strings.TrimSpace(c.RefreshToken)
	c.IDToken = strings.TrimSpace(c.IDToken)
	c.ChatGPTAccountID = strings.TrimSpace(c.ChatGPTAccountID)
	c.UserAgent = strings.TrimSpace(c.UserAgent)
	c.Originator = strings.TrimSpace(c.Originator)
	return c, err
}

// knownType reports whether the account type is one of the plugin's.
func knownType(typ string) bool { return typ == TypeDevice || typ == TypeOAuth }

// masked is the value the core shows (and sends back) for a stored
// sensitive field; it means "keep the stored value".
const masked = "******"

// ValidateCredentials implements pluginsdk.Platform. Only access_token is
// required: the refresh token and the identity fields are optional (an
// account without them simply cannot be renewed or shows no email). String
// fields are trimmed, expires_at becomes a number, and email /
// chatgpt_account_id / plan_type missing from the credentials are filled in
// from the id_token payload (see jwtClaims for why it is not verified).
func (p *Plugin) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	typ := in.GetAccountType()
	if !knownType(typ) {
		return invalid("account_type", "unsupported", "unsupported account type "+strconv.Quote(typ)+" / 不支持的账号类型"), nil
	}
	var m credMap
	if err := json.Unmarshal([]byte(orEmptyObject(in.GetCredentialsJson())), &m); err != nil || m == nil {
		return invalid("access_token", "invalid", "credentials must be a JSON object / 凭证必须是 JSON 对象"), nil
	}
	changed := m.trimStrings()
	if bad := m.checkStrings(); bad != nil {
		return bad, nil
	}
	expChanged, bad := m.normalizeExpiresAt()
	if bad != nil {
		return bad, nil
	}
	filled := m.fillIdentity()
	out := &pluginv1.ValidateCredentialsResponse{}
	if changed || expChanged || filled {
		b, _ := json.Marshal(m)
		out.NormalizedCredentialsJson = string(b)
	}
	return out, nil
}

// credMap is the submitted credential object ValidateCredentials checks and
// normalizes in place.
type credMap map[string]any

// stringFields are the optional credential fields that must be strings
// besides the client headers (agentError).
var stringFields = []string{"refresh_token", "id_token", "email", "chatgpt_account_id", "plan_type"}

// str reads a string field: ("", true) when absent or null, ok=false when
// the value is not a string.
func (m credMap) str(k string) (string, bool) {
	v, present := m[k]
	if !present || v == nil {
		return "", true
	}
	s, ok := v.(string)
	return s, ok
}

// trimStrings trims every string value and reports whether one changed.
func (m credMap) trimStrings() bool {
	changed := false
	for k, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != s {
			m[k] = strings.TrimSpace(s)
			changed = true
		}
	}
	return changed
}

// checkStrings requires a non-empty access_token, string-typed optional
// fields and client headers (agentFields) that can be sent as header values.
func (m credMap) checkStrings() *pluginv1.ValidateCredentialsResponse {
	at, ok := m.str("access_token")
	if !ok {
		return invalid("access_token", "pattern", "access_token must be a string / access_token 必须是字符串")
	}
	if at == "" {
		return invalid("access_token", "required", "access_token is required: authorize the account first / 请先完成授权以获取 access_token")
	}
	for _, k := range stringFields {
		if _, ok := m.str(k); !ok {
			return invalid(k, "pattern", k+" must be a string / "+k+" 必须是字符串")
		}
	}
	if k, code, msg := m.agentError(); k != "" {
		return invalid(k, code, msg)
	}
	return nil
}

// agentError names the first client header (agentFields) of m that is not a
// string or cannot be sent as a header value: its field, code and message,
// k "" when there is none. The account's credentials and the authorization
// routes, which carry the form's values (readCall), are checked alike.
func (m credMap) agentError() (k, code, msg string) {
	for _, k := range agentFields {
		v, ok := m.str(k)
		if !ok {
			return k, "pattern", k + " must be a string / " + k + " 必须是字符串"
		}
		if !paramoverride.ValidHeaderValue(v) {
			return k, "pattern", k + " must not contain control characters / " + k + " 不能包含控制字符"
		}
	}
	return "", "", ""
}

// normalizeExpiresAt turns a string expires_at into a number (an empty one is
// dropped) and refuses anything that is not Unix seconds, a non-negative
// integer that fits int64, whether it came as a number or a string.
func (m credMap) normalizeExpiresAt() (changed bool, bad *pluginv1.ValidateCredentialsResponse) {
	const msg = "expires_at must be Unix seconds / expires_at 必须是 Unix 秒"
	switch v := m["expires_at"].(type) {
	case nil:
		return false, nil
	case float64:
		if v < 0 || v != math.Trunc(v) || v >= math.MaxInt64 {
			return false, invalid("expires_at", "pattern", msg)
		}
		return false, nil
	case string:
		if v == "" {
			delete(m, "expires_at")
			return true, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return false, invalid("expires_at", "pattern", msg)
		}
		m["expires_at"] = n
		return true, nil
	default:
		return false, invalid("expires_at", "pattern", msg)
	}
}

// fillIdentity fills email, plan_type and chatgpt_account_id from the
// submitted id_token where the credentials leave them empty. A masked
// id_token ("******", the stored one) is not read.
func (m credMap) fillIdentity() bool {
	idt, _ := m.str("id_token")
	if idt == "" || idt == masked {
		return false
	}
	id := parseIdentity(idt, "")
	changed := false
	fill := func(k, v string) {
		if cur, _ := m.str(k); cur == "" && v != "" {
			m[k] = v
			changed = true
		}
	}
	fill("email", id.Email)
	fill("plan_type", id.PlanType)
	fill("chatgpt_account_id", id.AccountID)
	return changed
}

// invalid is the ValidateCredentials answer refusing field.
func invalid(field, code, msg string) *pluginv1.ValidateCredentialsResponse {
	return accountform.Invalid(accountform.FieldError(field, code, msg))
}

func orEmptyObject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}
