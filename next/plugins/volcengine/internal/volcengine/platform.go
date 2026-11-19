// Package volcengine implements the "Volcengine Ark" plugin: the apikey
// account type for the core's built-in openai platform (ARCHITECTURE 6.6),
// i.e. credential validation, upstream request construction and error
// classification for Volcengine Ark (火山方舟 / 豆包).
//
// Ark speaks the OpenAI protocol, but its compatible API lives under
// /api/v3 instead of /v1, so this plugin exists mainly to rewrite the
// upstream path and to read Ark's own error codes. For chat, responses and
// embeddings it declares no endpoint of its own: the client keeps calling
// /v1/chat/completions, /v1/responses and /v1/embeddings and Ark accounts
// are scheduled next to plain OpenAI accounts in the same group.
//
// Image generation (Seedream) has no built-in platform, so the manifest
// declares the "volcengine" platform with its endpoint, POST
// /api/v3/images/generations (Ark's own path; also served under
// /doubao/api/v3). It declares no price: the images fact
// (usage.generated_images) and the output tokens are the metering keys an
// administrator writes a price expression against (ARCHITECTURE 7.3).
//
// Scope so far (docs/PLUGIN-VOLCENGINE-ARK.md §6): chat, responses,
// embeddings (stage one), synchronous images (stage two) and the asset
// library (stage three: assets.go, arkapi.go, store.go, routes.go - the
// plugin's own admin routes over the Ark asset OpenAPI, with a local index in
// plg_volcengine), and video generation (Seedance) as a host-managed task
// (video.go).
package volcengine

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
	sdkanthropic "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	openaiprovider "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	sdkvolcengine "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/volcengine"
)

// PlatformID is the built-in platform the apikey account type serves.
const PlatformID = "openai"

// PlatformVolcengine is the platform this plugin declares itself
// (manifest platforms[]): Ark capabilities that have no built-in platform.
const PlatformVolcengine = "volcengine"

// PlatformAnthropic is the second built-in platform the apikey account type
// serves. Ark exposes an Anthropic-Messages-compatible surface, and the core
// registers no protocol converters at all (gateway/convert: the list is
// empty), so a client speaking Anthropic can only reach an Ark account if the
// account type declares this platform and the plugin forwards the body as it
// arrived - protocol in, same protocol out.
const PlatformAnthropic = "anthropic"

// Protocol ids of the built-in openai platform's endpoints
// (server/internal/platforms/openai.json).
const (
	ProtocolChat       = "openai.chat"
	ProtocolResponses  = "openai.responses"
	ProtocolEmbeddings = "openai.embeddings"
)

// ProtocolMessages is the protocol id of the built-in anthropic platform's
// messages endpoint (sdk/platforms/anthropic.json), an alias of the SDK
// provider constant, so the plugin cannot drift from what classify.go and the
// core compare against. It is the only endpoint of that platform the account
// types serve (manifest "endpoints": ["messages"]): Ark documents no
// token-counting endpoint, so count_tokens is left to the core's estimate.
const ProtocolMessages = sdkvolcengine.ProtocolMessages

// Protocol ids of the volcengine platform declared in manifest.json.
const (
	// ProtocolImages is synchronous image generation (Seedream), served to
	// clients at POST /api/v3/images/generations and its alias
	// /doubao/api/v3/images/generations. Text-to-image and
	// image-to-image share this one upstream path.
	ProtocolImages = "volcengine.images"
)

// Protocols lists the protocols of the built-in openai platform that
// BuildUpstream supports, in that platform's endpoint order.
var Protocols = []string{ProtocolChat, ProtocolResponses, ProtocolEmbeddings}

// OwnProtocols lists the protocols of the plugin's own volcengine platform,
// in manifest endpoint order.
var OwnProtocols = []string{ProtocolImages, ProtocolVideoSubmit, ProtocolVideoQuery}

const (
	// AccountTypeAPIKey is the official account type: a key of Ark itself, at
	// one of Ark's own endpoints, serving Ark's own path layout.
	AccountTypeAPIKey = "apikey"
	// AccountTypeRelay is the Ark-compatible relay account type, named for
	// what people use it for: Doubao video through an aggregator. Its base URL
	// is free text and its paths are configurable, because a relay mounts the
	// Ark shapes wherever it likes.
	//
	// The two types exist so that "official or relay" is DECLARED rather than
	// guessed. An earlier version had one type and derived the layout from the
	// host of base_url; that works until it does not, and it made every field
	// of both cases share one form. The same split already exists one level up
	// in this repo - the anthropic plugin guards base_url to api.anthropic.com
	// while the relay plugin's relay_key type leaves it free - and for the same
	// reason: a relay's address cannot be expressed as an allow-list, so it
	// cannot share an account type with an upstream whose address is pinned.
	AccountTypeRelay = "relay"

	// DefaultBaseURL is used when an apikey account has no base_url: the
	// official Chinese Ark endpoint. The overseas BytePlus endpoint
	// (BytePlusBaseURL) is the other value guardedSettings allows.
	DefaultBaseURL = "https://ark.cn-beijing.volces.com"
	// BytePlusBaseURL is Ark's overseas (BytePlus) endpoint.
	BytePlusBaseURL = "https://ark.ap-southeast.bytepluses.com"

	// APIPrefix is the root of Ark's own OpenAI-compatible API, and the whole
	// layout of an apikey account: not configurable there, because Ark serves
	// one layout and an account of that type is by definition on Ark.
	APIPrefix = "/api/v3"

	// RelayAPIPrefix is the default layout of a relay account: the standard
	// OpenAI and Anthropic paths (/v1/chat/completions, /v1/messages), which is
	// what every Ark-compatible relay measured so far serves.
	RelayAPIPrefix = "/v1"

	// FieldAPIPrefix overrides a relay's text-surface layout, for a relay that
	// mirrors Ark's own paths under its own host. FieldVideoAPIPrefix is
	// separate because Ark's native video tasks are the thing a relay most
	// often mounts in a namespace of its own. Neither is offered on the apikey
	// type.
	FieldAPIPrefix      = "api_prefix"
	FieldVideoAPIPrefix = "video_api_prefix"

	// MaxPrefixLen bounds a prefix. Long enough for a namespaced relay path
	// ("/doubao/api/v3"), short enough that the field cannot carry a payload.
	MaxPrefixLen = 128

	// DefaultTestModel is used by BuildTestRequest when the operator names
	// no model. doubao-seed-1-6-250615 is Ark's long-standing general chat
	// model: a dated snapshot id (so it cannot drift under us), enabled by
	// default on accounts created through the normal console flow, and the
	// id the official quick-start uses. The console test dialog can always
	// override it (CONTRACTS §15.9), which is the escape hatch for accounts
	// that only opened other models.
	DefaultTestModel = "doubao-seed-1-6-250615"
	// testMaxTokens keeps the probe as cheap as possible while staying
	// above the minimum a thinking-capable Doubao model accepts: the
	// default test model may spend its budget on reasoning tokens, and a
	// max_tokens of 1 is rejected by some Ark models. 16 output tokens are
	// negligible and the test only needs a 2xx.
	testMaxTokens = 16

	// BotModelPrefix marks an Ark "bot" (application) id, which is served
	// by a different path than a plain model. Ark ids look like
	// "bot-20250101...".
	BotModelPrefix = "bot"

	// DefaultAnthropicVersion is sent on Anthropic-protocol requests whose
	// client sent none. Anthropic's API requires the header; the value is the
	// one its own docs and every current SDK use, and the same default the
	// anthropic plugin applies (the SDK provider constant).
	DefaultAnthropicVersion = sdkanthropic.DefaultAPIVersion
)

// spec describes the apikey (official Ark) account type. base_url is saved as
// written; /api/v3 is its one version: operators paste the Ark SDK base URL
// ("https://ark.cn-beijing.volces.com/api/v3"), FromAccount leaves the trailing
// /api/v3 out and the paths below add their own prefix to the root. The
// manifest's guardedSettings declare the same versions, so the core compares
// the root against the bare host (CONTRACTS §21.3).
var spec = apikey.Spec{AccountType: AccountTypeAPIKey, DefaultBaseURL: DefaultBaseURL, Versions: []string{APIPrefix}, Fields: assetKeys}

// assetKeys are the asset library keys both account forms send besides
// api_key and base_url; validateAssetFields checks them. A spec's Fields
// also refuse every other key (code "unknown").
var assetKeys = fieldcheck.Fields{
	FieldAccessKey:    fieldcheck.Any(),
	FieldSecretKey:    fieldcheck.Any(),
	FieldAssetBaseURL: fieldcheck.Any(),
	FieldAssetRegion:  fieldcheck.Any(),
}

// relaySpec describes the relay account type. Two deliberate differences from
// spec:
//
//   - No DefaultBaseURL. A relay has no default address, so an account with no
//     base_url is a misconfiguration rather than an official account, and
//     relayBaseURL below says so instead of quietly sending a relay's key to
//     Ark.
//   - No Versions. On the apikey type, leaving a trailing /api/v3 out undoes
//     a paste of Ark's documented SDK base URL. Here the base URL is the
//     relay's root and the layout is named by api_prefix, so leaving anything
//     out would silently move an operator's request: "https://r/api/v3" with
//     the default prefix would become "https://r/v1/...". validateRelayBaseURL
//     refuses that shape with a message pointing at api_prefix, which is the
//     value that expresses it.
//
// Its form also sends the relay's endpoints and the legacy prefixes
// (checked by validatePrefixFields and the endpoint checks).
var relaySpec = apikey.Spec{AccountType: AccountTypeRelay, Fields: relayFields()}

func relayFields() fieldcheck.Fields {
	fields := maps.Clone(assetKeys)
	for _, k := range []string{FieldVideoEndpoint, FieldAssetEndpoint, FieldAPIPrefix, FieldVideoAPIPrefix} {
		fields[k] = fieldcheck.Any()
	}
	return fields
}

// specFor returns the credential spec of an account type. An empty type means
// the official one: that is what an account row carried before the split, and
// what a caller that did not fill Account.type means.
func specFor(accountType string) (apikey.Spec, error) {
	switch accountType {
	case AccountTypeAPIKey, "":
		return spec, nil
	case AccountTypeRelay:
		return relaySpec, nil
	default:
		return apikey.Spec{}, status.Errorf(codes.FailedPrecondition, "unsupported account type %q", accountType)
	}
}

// accountConfig reads an account of either type: its credentials through the
// matching spec, and the upstream path layout the type implies.
func accountConfig(acc *pluginv1.Account) (*apikey.Config, prefixes, error) {
	sp, err := specFor(acc.GetType())
	if err != nil {
		return nil, prefixes{}, err
	}
	cfg, err := sp.FromAccount(acc)
	if err != nil {
		return nil, prefixes{}, err
	}
	if acc.GetType() == AccountTypeRelay && cfg.BaseURL == "" {
		return nil, prefixes{}, status.Errorf(codes.FailedPrecondition,
			"account %d: a %s account needs a base_url - there is no default relay address", acc.GetId(), AccountTypeRelay)
	}
	px, err := prefixesOf(acc.GetType(), acc.GetSettingsJson())
	if err != nil {
		return nil, prefixes{}, status.Errorf(codes.FailedPrecondition, "account %d settings: %v", acc.GetId(), err)
	}
	return cfg, px, nil
}

// Plugin is the volcengine plugin. It implements pluginsdk.Platform and,
// since stage three, pluginsdk.HTTP (the asset library routes in routes.go)
// plus the lifecycle interfaces they need.
//
// It deliberately does not implement pluginsdk.ModelLister: Ark documents no
// API-key-authenticated "list models" endpoint, so BuildModelsRequest
// answers UNIMPLEMENTED (CONTRACTS §19) instead of pointing the core at a
// guessed URL.
type Plugin struct {
	*pluginsdk.Router

	now  func() time.Time
	host pluginsdk.Host
	log  *slog.Logger

	// ark is how the asset library reaches upstream: through the host
	// (SendAccountRequest), set by Init.
	ark *arkAPI
}

// New returns the plugin.
func New() *Plugin {
	p := &Plugin{Router: pluginsdk.NewRouter(), now: time.Now, log: slog.Default()}
	p.routes()
	return p
}

// Init implements pluginsdk.Initializer: the asset library's requests are
// sent by the host, for the account, through its proxy
// (pluginsdk.SendAccountRequest); the plugin opens no connection.
func (p *Plugin) Init(_ context.Context, h pluginsdk.Host) error {
	p.host = h
	p.log = h.Logger()
	p.ark = newArkAPI(func(ctx context.Context, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return pluginsdk.SendAccountRequest(ctx, h, in)
	})
	return nil
}

// ValidateCredentials implements pluginsdk.Platform: api_key is required,
// base_url is optional and, when given, must be an absolute http(s) URL, and
// the asset library fields are checked on top (assets.go): the AK/SK pair has
// to be complete or completely absent. The normalized values come back in
// normalized_{credentials,settings}_json.
func (p *Plugin) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return p.validateWithAssets(in), nil
}

// chatPath returns the chat completions path for an upstream model: Ark
// serves bots (applications) under <prefix>/bots/chat/completions and plain
// models under <prefix>/chat/completions.
func chatPath(prefix, model string) string {
	if strings.HasPrefix(model, BotModelPrefix) {
		return prefix + "/bots/chat/completions"
	}
	return prefix + "/chat/completions"
}

// upstreamPath maps the upstream protocol (RequestMeta.protocol, which may
// differ from the client endpoint's protocol when the core converts the
// request) and the upstream model to the Ark path. An empty protocol means
// chat completions.
//
// The image path is the same for text-to-image and image-to-image: Ark
// decides from the request body, not the URL. The client-facing path
// (/api/v3/images/generations, or the /doubao/api/v3 alias) never reaches
// this function: upstream it is always the account's own
// <prefix>/images/generations, whichever prefix the client used.
func upstreamPath(px prefixes, protocol, model string) (string, error) {
	switch protocol {
	case ProtocolChat, "":
		return chatPath(px.api, model), nil
	case ProtocolResponses:
		return px.api + "/responses", nil
	case ProtocolEmbeddings:
		return px.api + "/embeddings", nil
	case ProtocolImages:
		return px.api + "/images/generations", nil
	case ProtocolMessages:
		// Forwarded as it arrived: the core has no Anthropic-to-OpenAI
		// converter, so the body reaching us is already Anthropic-shaped and
		// the only decision is where to send it.
		//
		// Official Ark serves /api/compatible/v1/messages. Relays keep their
		// configured path; neither requires protocol conversion.
		if px.anthropic == "" {
			return "", status.Errorf(codes.InvalidArgument,
				"account has no configured upstream path for %q", protocol)
		}
		return px.anthropic + "/messages", nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "unsupported upstream protocol %q", protocol)
	}
}

// upstreamTarget maps the upstream protocol and model to the Ark URL. Every
// request is a POST: to the account's prefix, or for a video submission to
// the account's task collection (videoTaskURL), the same one Monitor polls.
// Task queries never reach the plugin: the core answers them from its own
// task snapshot.
func upstreamTarget(base string, px prefixes, meta *pluginv1.RequestMeta, model string) (string, error) {
	if meta.GetProtocol() == ProtocolVideoSubmit {
		return videoTaskURL(base, px, "")
	}
	path, err := upstreamPath(px, meta.GetProtocol(), model)
	if err != nil {
		return "", err
	}
	return upstreamURL(base, path)
}

// credentialHeader is the header carrying the account key. Bearer remains the compatible relay/default authentication form; the
// official native Messages surface takes x-api-key.
func credentialHeader(accountType, protocol, apiKey string) (name, value string) {
	if protocol == ProtocolMessages && accountType != AccountTypeRelay {
		return "x-api-key", apiKey
	}
	return "authorization", "Bearer " + apiKey
}

// upstreamHeaders are all headers of one request: the account key, the
// content type and the client headers the core handed over. Those are the
// manifest's passHeaders of the platform entry the request was routed to:
// only the user agent on the openai and volcengine surfaces (Ark ignores
// OpenAI's org/project and x-stainless-* headers), and on the Anthropic
// surface also anthropic-version and anthropic-beta, which change how the
// upstream reads the body.
//
// Anthropic requires a version header and rejects a request without one, so an
// Anthropic-protocol request that the client sent none for gets the default.
// The client's own value wins when it sent one: it knows which beta shape its
// body is in, and we must not silently downgrade it.
func upstreamHeaders(accountType, protocol, apiKey string, inbound map[string]string) map[string]string {
	h := map[string]string{}
	if protocol == ProtocolMessages && strings.TrimSpace(inbound["anthropic-version"]) == "" {
		h["anthropic-version"] = DefaultAnthropicVersion
	}
	apikey.ForwardHeaders(h, inbound)
	name, value := credentialHeader(accountType, protocol, apiKey)
	h[name], h["content-type"] = value, "application/json"
	return h
}

// upstreamBody is the body relayed upstream: the one handed in, final, with at
// most one targeted edit. Streaming chat completions get
// stream_options.include_usage=true so the upstream reports usage in the last
// chunk (CONTRACTS 14.1; Ark is OpenAI-compatible here), the same edit and
// the same notion of a stream as the openai preset's.
func upstreamBody(in *pluginv1.ExecuteRequest) ([]byte, error) {
	body, protocol, stream := in.GetBody(), in.GetMeta().GetProtocol(), pluginsdk.RequestStream(in)
	if !stream || (protocol != ProtocolChat && protocol != "") {
		return body, nil
	}
	out, err := jsonedit.Edit(body, func(d *jsonedit.Doc) { openaiprovider.StreamUsage(d, stream) })
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "request body: %v", err)
	}
	return out, nil
}

// BuildUpstream implements pluginsdk.UpstreamBuilder: {base_url}{prefix}/...
// following meta.protocol and, for chat completions, the model (bots have
// their own path). Every header is authoritative: before forwarding v2 this
// plugin's headers were written above the operator's passthrough and
// override, and that behaviour is kept.
func (p *Plugin) BuildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	acc, meta := in.GetAccount(), in.GetMeta()
	cfg, px, err := accountConfig(acc)
	if err != nil {
		return nil, err
	}
	model := pluginsdk.RequestModel(in)
	target, err := upstreamTarget(cfg.BaseURL, px, meta, model)
	if err != nil {
		return nil, err
	}
	body, err := upstreamBody(in)
	if err != nil {
		return nil, err
	}
	return pluginsdk.NewUpstream(http.MethodPost, target).
		HeaderMap(upstreamHeaders(acc.GetType(), meta.GetProtocol(), cfg.APIKey, in.GetInboundHeaders())).
		Body(body).
		Model(model).
		Build()
}

// BuildTestRequest implements pluginsdk.Platform: a minimal chat completion.
// The response names the model really used and openai.chat as the protocol
// whose usage rules read the token counts, so the console test can show the
// token usage (account/testreq.go testUsage).
func (p *Plugin) BuildTestRequest(_ context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	cfg, px, err := accountConfig(in.GetAccount())
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(in.GetModel())
	if model == "" {
		model = DefaultTestModel
	}
	// The test must use the same prefix real traffic will, or "test account"
	// answers a question nobody asked.
	u, err := upstreamURL(cfg.BaseURL, chatPath(px.api, model))
	if err != nil {
		return nil, err
	}
	body := openaiprovider.ChatPingBody(model, testMaxTokens)
	return &pluginv1.BuildTestRequestResponse{
		Method:        "POST",
		Url:           u,
		Headers:       upstreamHeaders(in.GetAccount().GetType(), ProtocolChat, cfg.APIKey, nil),
		BodyJson:      body,
		Model:         model,
		UsageProtocol: ProtocolChat,
	}, nil
}
