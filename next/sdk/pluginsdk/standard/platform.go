package standard

import (
	"context"
	"net/http"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
)

// Platform serves the account types it was made with. It implements
// pluginsdk.Platform (the SDK runs its Execute with pluginsdk.ExecuteDefault),
// pluginsdk.UpstreamBuilder, pluginsdk.ModelLister and
// pluginsdk.BalanceReader, and is safe for concurrent use.
type Platform struct {
	types []Type
}

var (
	_ pluginsdk.Platform        = (*Platform)(nil)
	_ pluginsdk.UpstreamBuilder = (*Platform)(nil)
	_ pluginsdk.ModelLister     = (*Platform)(nil)
	_ pluginsdk.BalanceReader   = (*Platform)(nil)
)

// New returns the platform of types. A type that is not consistent (see
// Check) is a programming error: New panics with what is wrong.
func New(types ...Type) *Platform {
	for i := range types {
		types[i].Spec.AccountType = types[i].ID
	}
	if err := Check(types...); err != nil {
		panic("standard.New: " + err.Error())
	}
	return &Platform{types: types}
}

// BuildUpstream builds the final request of one attempt: the route of
// meta.protocol on the account's base URL, the key (where the route's Auth,
// else the type's, says) as the authoritative header, every other header as
// a default header, and
// the body with the type's FilterFields and the route's Edit applied.
func (p *Platform) BuildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	t, cfg, err := p.account(in.GetAccount())
	if err != nil {
		return nil, err
	}
	route, err := t.route(in.GetMeta().GetProtocol())
	if err != nil {
		return nil, err
	}
	model, err := route.model(in)
	if err != nil {
		return nil, err
	}
	r := &Request{Account: in.GetAccount(), Meta: in.GetMeta(), APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Model: model,
		Stream: pluginsdk.RequestStream(in), Client: clientHeaders(in.GetInboundHeaders())}
	body, err := t.edit(in.GetBody(), route, r)
	if err != nil {
		return nil, err
	}
	headers := t.headers(route.Headers, r)
	if route.WebSocket {
		delete(headers, "content-type") // a handshake has no body
	}
	u := pluginsdk.NewUpstream(route.method(), upstreamURL(route.Path(r), route.WebSocket, r)).
		Body(body).
		Model(model)
	for name, value := range headers {
		u.DefaultHeader(name, value)
	}
	for name, value := range credential(t.auth(route.Auth), r) {
		u.Header(name, value)
	}
	return u.Build()
}

// ValidateCredentials checks and normalizes the api_key and base_url of an
// account (apikey.Spec.Validate).
func (p *Platform) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return p.typeOf(in.GetAccountType()).Spec.Validate(in), nil
}

// ClassifyError classifies an upstream error with the policy of the route
// of meta.protocol, else the account type's, at the current time.
func (p *Platform) ClassifyError(_ context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return p.ClassifyAt(time.Now(), in), nil
}

// ClassifyAt is ClassifyError at the time now.
func (p *Platform) ClassifyAt(now time.Time, in *pluginv1.ClassifyErrorRequest) *pluginv1.ClassifyErrorResponse {
	return p.typeOf(in.GetAccount().GetType()).policy(in.GetMeta().GetProtocol()).Classify(now, in)
}

// BuildTestRequest is the console's account test: the route of the test of
// the first protocol in.protocols names that the type has a test on (Test,
// else Tests), else of Test, with the test body, the console's model
// normalized like the route's (the test's model when it names none), no
// client headers. usage_protocol is the test's protocol.
func (p *Platform) BuildTestRequest(_ context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	t, cfg, err := p.account(in.GetAccount())
	if err != nil {
		return nil, err
	}
	if t.Test == nil {
		return nil, status.Error(codes.Unimplemented, "the account type has no test request")
	}
	test := t.test(in.GetProtocols())
	if test == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "the account type has no test request for protocols %v", in.GetProtocols())
	}
	route := t.Routes[test.Protocol]
	model := normalizeModel(in.GetModel(), route.Model.TrimPrefix)
	if model == "" {
		model = test.Model
	}
	r := &Request{Account: in.GetAccount(), APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Model: model}
	headers := t.headers(route.Headers, r)
	for name, value := range credential(t.auth(route.Auth), r) {
		headers[name] = value
	}
	return &pluginv1.BuildTestRequestResponse{
		Method:        route.method(),
		Url:           upstreamURL(route.Path(r), false, r),
		Headers:       headers,
		BodyJson:      test.Body(model),
		Model:         model,
		UsageProtocol: test.Protocol,
	}, nil
}

// BuildModelsRequest lists the account's models: GET on the listing path
// with the type's headers and the key, without content-type.
func (p *Platform) BuildModelsRequest(_ context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	t, cfg, err := p.account(in.GetAccount())
	if err != nil {
		return nil, err
	}
	if t.Models == nil {
		return nil, status.Error(codes.Unimplemented, "the account type has no model list")
	}
	r := &Request{Account: in.GetAccount(), APIKey: cfg.APIKey, BaseURL: cfg.BaseURL}
	out := apikey.ModelListRequest(cfg, *t.Models, t.getHeaders(t.Auth, r))
	out.Url = upstreamURL(t.Models.Path, false, r)
	return out, nil
}

// typeOf is the type id names; the first type for an unknown id, whose
// Spec and policy then answer for it (Spec.Validate reports the type).
func (p *Platform) typeOf(id string) *Type {
	for i := range p.types {
		if p.types[i].ID == id {
			return &p.types[i]
		}
	}
	return &p.types[0]
}

// account resolves the type and the api_key / base_url of acc. An account
// without a type is the only type's; with several types it is refused.
func (p *Platform) account(acc *pluginv1.Account) (*Type, *apikey.Config, error) {
	t := p.typeOf(acc.GetType())
	if acc != nil && t.ID != acc.GetType() && (acc.GetType() != "" || len(p.types) > 1) {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "account %d: unsupported account type %q", acc.GetId(), acc.GetType())
	}
	cfg, err := t.Spec.FromAccount(acc)
	if err != nil {
		return nil, nil, err
	}
	return t, cfg, nil
}

// test is the test of the first of protocols the type has one on; Test
// for no protocols (no preference; Check: Tests needs Test); nil when the
// type has a test for none of them (CONTRACTS §122.3).
func (t *Type) test(protocols []string) *Test {
	for _, protocol := range protocols {
		if t.Test.Protocol == protocol {
			return t.Test
		}
		if i := slices.IndexFunc(t.Tests, func(x Test) bool { return x.Protocol == protocol }); i >= 0 {
			return &t.Tests[i]
		}
	}
	if len(protocols) > 0 {
		return nil
	}
	return t.Test
}

// policy is the error policy of protocol: its route's, else the type's.
func (t *Type) policy(protocol string) *classify.Policy {
	if r, ok := t.Routes[protocol]; ok && r.Classify != nil {
		return r.Classify
	}
	return &t.Classify
}

// auth is a, else the type's.
func (t *Type) auth(a *Auth) Auth {
	if a != nil {
		return *a
	}
	return t.Auth
}

// route is the route of protocol, the default protocol's for "".
func (t *Type) route(protocol string) (*Route, error) {
	name := protocol
	if name == "" {
		name = t.DefaultProtocol
	}
	if r, ok := t.Routes[name]; ok {
		return &r, nil
	}
	return nil, status.Errorf(codes.InvalidArgument, "unsupported upstream protocol %q", protocol)
}

// model is the route's upstream model of in.
func (r *Route) model(in *pluginv1.ExecuteRequest) (string, error) {
	name := pluginsdk.RequestModel(in)
	if r.Model.FromMeta {
		name = in.GetMeta().GetModel()
	}
	if r.Model.TrimPrefix != "" {
		name = normalizeModel(name, r.Model.TrimPrefix)
	}
	if r.Model.Required && name == "" {
		msg := r.Model.Missing
		if msg == "" {
			msg = "model is required"
		}
		return "", status.Error(codes.InvalidArgument, msg)
	}
	return name, nil
}

func (r *Route) method() string {
	if r.WebSocket {
		return http.MethodGet
	}
	return http.MethodPost
}
