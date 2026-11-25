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
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

const (
	// Version is the plugin version (manifest.json "version"); it is part of
	// the User-Agent every request sends.
	Version = "0.2.3"
	// UserAgent and Originator name sup2api as the client, truthfully: the
	// built-in values of the client headers (agent.go).
	UserAgent  = "sup2api-codex/" + Version
	Originator = "sup2api"

	// Account types (manifest accountTypes[].id).
	TypeDevice = "codex_device"
	TypeOAuth  = "codex_oauth"

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
