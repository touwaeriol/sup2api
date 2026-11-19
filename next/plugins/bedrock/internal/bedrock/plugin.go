// Package bedrock implements the bedrock plugin: three account types that
// reach Claude through the AWS Bedrock runtime
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §9). They serve the messages
// endpoint of the built-in anthropic platform with InvokeModel and
// InvokeModelWithResponseStream: the model goes into the path, the body
// loses "model" and "stream" and gains anthropic_version, and a streamed
// answer comes back as an AWS event stream the core decodes into SSE.
//
// No type declares the openai platform: a request in an OpenAI protocol is
// converted by the core into a Messages request and takes the same route.
// Bedrock's OpenAI-compatible endpoints (/openai/v1) are not used.
//
// The types differ only in how a request is authorized:
//
//   - bedrock_apikey: a Bedrock API key, sent as a bearer token;
//   - bedrock_aksk: a long-term access key. The plugin signs the final
//     request (SigV4) and hands the core the signature as authoritative
//     headers;
//   - bedrock_sts: temporary credentials, the same with a session token.
//     Nothing renews them: once they expire the upstream refuses the account
//     and an administrator replaces them.
//
// The plugin does no network I/O. Execute is given the whole request body,
// builds the final InvokeModel request from it (invokeBody: the top-level
// fields, the beta features, and what Claude Code adds deep inside the body
// that Bedrock refuses) and relays it through the core's HTTP client
// without waiting; the core sends it, decodes the event stream and reads
// the usage.
//
// Never log an API key, a secret access key or a session token.
package bedrock

import (
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/awseventstream"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
)

const (
	// Version is the plugin version (manifest.json "version").
	Version = "0.1.0"

	// Account types (manifest accountTypes[].id).
	TypeAPIKey    = "bedrock_apikey"
	TypeAccessKey = "bedrock_aksk"
	TypeSTS       = "bedrock_sts"

	// ProtocolMessages is the upstream protocol BuildUpstream speaks:
	// the messages endpoint of the built-in anthropic platform.
	ProtocolMessages = anthropic.ProtocolMessages

	// DefaultRegion is the region of an account that names none.
	DefaultRegion = "us-east-1"
	// SigningService is the SigV4 service name of the Bedrock runtime.
	SigningService = "bedrock"
	// AnthropicVersion is the anthropic_version InvokeModel requires in a
	// Claude request body.
	AnthropicVersion = "bedrock-2023-05-31"
	// ResponseStream names the framing of a streamed InvokeModel answer
	// (UpstreamRequest.response_stream).
	ResponseStream = awseventstream.FormatChunk

	// DefaultTestModel is the Bedrock model id BuildTestRequest invokes when
	// no model is given: the global inference profile of Claude Haiku 4.5,
	// which is the default mapping's Haiku 4.5 id behind "global.". Haiku 4.5
	// has no bare model id on bedrock-runtime ("requires a geo or global
	// inference profile ID",
	// https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-4-5.html),
	// so the id names its profile itself and is sent as it is whatever the
	// account's prefix, "none" included.
	DefaultTestModel = "global.anthropic.claude-haiku-4-5-20251001-v1:0"
)

// Endpoints are the addresses the plugin builds requests for. The zero value
// means the production address; tests point it at a fake server.
type Endpoints struct {
	// API is the scheme and host of every runtime request, without a
	// trailing slash ("" = https://bedrock-runtime.<region>.amazonaws.com).
	API string
}

// Plugin is the bedrock plugin. It implements pluginsdk.Platform and
// pluginsdk.UpstreamBuilder. It lists
// no models: the console keeps the manifest defaults.
type Plugin struct {
	ep  Endpoints
	now func() time.Time
}

// New returns the plugin building requests for the production addresses.
func New() *Plugin { return NewWithEndpoints(Endpoints{}) }

// NewWithEndpoints returns the plugin building requests for ep.
func NewWithEndpoints(ep Endpoints) *Plugin {
	ep.API = strings.TrimRight(ep.API, "/")
	return &Plugin{ep: ep, now: time.Now}
}

// ServiceDomain is the domain of every host the plugin builds requests for.
const ServiceDomain = "amazonaws.com"

// RuntimeHost is the Bedrock runtime host of a region.
func RuntimeHost(region string) string { return "bedrock-runtime." + region + "." + ServiceDomain }

// apiBase is the scheme and host serving region.
func (p *Plugin) apiBase(region string) string {
	if p.ep.API != "" {
		return p.ep.API
	}
	return "https://" + RuntimeHost(region)
}
