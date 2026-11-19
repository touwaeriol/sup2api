package vertex

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
)

// actions are the model methods of the upstream protocols. Streams are asked
// for as server-sent events, the only stream format the core relays.
var actions = map[string]string{
	ProtocolGenerate:       "generateContent",
	ProtocolStreamGenerate: "streamGenerateContent?alt=sse",
	ProtocolCountTokens:    "countTokens",
}

const (
	// modelPrefix is the resource prefix clients may put before a model id.
	modelPrefix = "models/"
	// contentType is the media type of every model request body.
	contentType = "application/json"
)

// apiBase is the scheme and host serving t: the global host for the express
// address and the global location, else the location's own.
func (p *Plugin) apiBase(t target) string {
	switch {
	case p.ep.API != "":
		return p.ep.API
	case t.express() || t.Location == LocationGlobal:
		return "https://" + APIHost
	}
	return "https://" + t.Location + "-" + APIHost
}

// modelURL is the address of action on a Google publisher model, and the
// model id it names. With project and location it is the full resource path;
// without (an API key's express address) the publisher path alone.
func (p *Plugin) modelURL(t target, model, action string) (address, id string, err error) {
	id = strings.TrimPrefix(strings.TrimSpace(model), modelPrefix)
	if id == "" {
		return "", "", status.Error(codes.InvalidArgument, "the request names no model")
	}
	path := "/v1"
	if !t.express() {
		path += "/projects/" + url.PathEscape(t.Project) + "/locations/" + url.PathEscape(t.Location)
	}
	return p.apiBase(t) + path + "/publishers/google/models/" + url.PathEscape(id) + ":" + action, id, nil
}

// auth is the authoritative header set of an account's requests at now:
// the API key, or the access token the core keeps renewing (with the quota
// project of user credentials). An account without a usable token (none
// fetched yet, or the stored one has expired) answers FAILED_PRECONDITION
// before anything is relayed; the gateway moves on to another account
// without penalizing this one.
func (a account) auth(now time.Time) (map[string]string, error) {
	if a.typ == TypeAPIKey {
		key := a.creds.Str(fieldAPIKey)
		if key == "" {
			return nil, status.Error(codes.FailedPrecondition, "the account has no API key / 账号没有 API Key")
		}
		return map[string]string{"x-goog-api-key": key}, nil
	}
	token := a.creds.Str(fieldAccessToken)
	if token == "" || a.tokenExpired(now) {
		return nil, status.Error(codes.FailedPrecondition, "the account has no valid access token (none yet, or expired); the next credential refresh fetches one / 账号没有有效的访问令牌（尚未获取或已过期），等待下一次凭证刷新")
	}
	h := map[string]string{"authorization": "Bearer " + token}
	if keyKinds[a.typ].quotaProject {
		h["x-goog-user-project"] = a.target.Project
	}
	return h, nil
}

// tokenExpired reports a stored access token whose expires_at has passed. A
// token without a readable expiry is not known to be expired.
func (a account) tokenExpired(now time.Time) bool {
	at, ok := expiry.ExpiresAt(a.creds)
	return ok && !at.After(now)
}

// upstreamRequest is a POST of an action on a model for an account.
type upstreamRequest struct {
	url string
	// model is the model id the URL names.
	model string
	// auth are the credential headers (account.auth).
	auth map[string]string
}

// request describes a POST of action on model for the account.
func (p *Plugin) request(acc *pluginv1.Account, model, action string) (upstreamRequest, error) {
	a, err := readAccount(acc)
	if err != nil {
		return upstreamRequest{}, err
	}
	auth, err := a.auth(p.now())
	if err != nil {
		return upstreamRequest{}, err
	}
	address, id, err := p.modelURL(a.target, model, action)
	return upstreamRequest{url: address, model: id, auth: auth}, err
}

// BuildUpstream implements pluginsdk.UpstreamBuilder. Vertex AI takes the
// Gemini API's body as it is, so the body goes upstream byte for byte; the
// model (after the account's mapping) goes into the path. Only the
// credential is authoritative; the content type and the client headers the
// core hands over (the gemini platform's passHeaders, already filtered) are
// defaults the operator may override.
func (p *Plugin) BuildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	protocol := in.GetMeta().GetProtocol()
	action, ok := actions[protocol]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported upstream protocol %q", protocol)
	}
	req, err := p.request(in.GetAccount(), in.GetMeta().GetModel(), action)
	if err != nil {
		return nil, err
	}
	return pluginsdk.NewUpstream(http.MethodPost, req.url).
		DefaultHeaderMap(in.GetInboundHeaders()).
		DefaultHeader("content-type", contentType).
		HeaderMap(req.auth).
		Body(in.GetBody()).
		Model(req.model).
		Build()
}

// BuildTestRequest implements pluginsdk.Platform: the smallest
// generateContent call. Its usage is read with the gemini.generate rules.
func (p *Plugin) BuildTestRequest(_ context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	model := strings.TrimSpace(in.GetModel())
	if model == "" {
		model = DefaultTestModel
	}
	req, err := p.request(in.GetAccount(), model, actions[ProtocolGenerate])
	if err != nil {
		return nil, err
	}
	headers := maps.Clone(req.auth)
	headers["content-type"] = contentType
	return &pluginv1.BuildTestRequestResponse{
		Method:        http.MethodPost,
		Url:           req.url,
		Headers:       headers,
		BodyJson:      gemini.GenerateContentPingBody(1),
		Model:         req.model,
		UsageProtocol: ProtocolGenerate,
	}, nil
}
