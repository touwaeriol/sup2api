// Package vertex implements the vertex plugin: three account types for the
// built-in gemini platform that reach Gemini through Google Cloud Vertex AI
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §7). They differ only in how a
// request is authorized:
//
//   - vertex_sa: a service account key file, exchanged for a one-hour access
//     token with a signed JWT;
//   - vertex_authorized_user: an application default credentials file of a
//     user (gcloud auth application-default login), exchanged with its
//     refresh token;
//   - vertex_apikey: a Vertex AI API key, sent as it is.
//
// The plugin does no network I/O. Execute builds the final upstream request
// (the client's Gemini body unchanged, the credential as an authoritative
// header) and relays it through the core without waiting; the core sends it
// through the account's proxy and reads the usage. The token exchange is
// described the same way and the core renews the token before it expires
// (CONTRACTS §48).
//
// Never log a key file, a private key, a refresh token, an access token or
// an API key.
package vertex

import (
	"strings"
	"time"
)

const (
	// Version is the plugin version (manifest.json "version").
	Version = "0.1.0"

	// Account types (manifest accountTypes[].id).
	TypeServiceAccount = "vertex_sa"
	TypeAuthorizedUser = "vertex_authorized_user"
	TypeAPIKey         = "vertex_apikey"

	// PlatformID is the built-in platform every type serves.
	PlatformID = "gemini"
	// Upstream protocols Execute speaks (the endpoints of the
	// gemini platform, sdk/platforms/gemini.json).
	ProtocolGenerate       = "gemini.generate"
	ProtocolStreamGenerate = "gemini.stream_generate"
	ProtocolCountTokens    = "gemini.count_tokens"

	// TokenURL is Google's OAuth token endpoint. It is the only token_uri a
	// key file may name and the audience of the signed assertion: the core
	// sends the exchange for the plugin, so a pasted file must not be able
	// to point it elsewhere.
	TokenURL = "https://oauth2.googleapis.com/token"
	// APIHost is the global Vertex AI host; a region's is "<region>-" + it.
	APIHost = "aiplatform.googleapis.com"
	// LocationGlobal is the location served by APIHost itself.
	LocationGlobal = "global"

	// DefaultTestModel is used by BuildTestRequest when no model is given.
	DefaultTestModel = "gemini-2.5-flash"
)

// Endpoints are the addresses the plugin builds requests for. The zero value
// of a field means the production address; tests point them at fake servers.
type Endpoints struct {
	// Token is the token endpoint ("" = TokenURL).
	Token string
	// API is the scheme and host of every model request, without a trailing
	// slash ("" = https://<host of the account's location>).
	API string
}

// Plugin is the vertex plugin. It implements pluginsdk.Platform,
// UpstreamBuilder and CredentialRefresher. It lists no models: the console
// keeps the manifest defaults.
type Plugin struct {
	ep  Endpoints
	now func() time.Time
}

// New returns the plugin building requests for the production addresses.
func New() *Plugin { return NewWithEndpoints(Endpoints{}) }

// NewWithEndpoints returns the plugin building requests for ep.
func NewWithEndpoints(ep Endpoints) *Plugin {
	if ep.Token == "" {
		ep.Token = TokenURL
	}
	ep.API = strings.TrimRight(ep.API, "/")
	return &Plugin{ep: ep, now: time.Now}
}
