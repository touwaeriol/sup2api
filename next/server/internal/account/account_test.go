package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// ---------------------------------------------------------------- fakes

type fakePlatform struct {
	testURL string
	// testModel and usageProtocol are reported by BuildTestRequest.
	testModel     string
	usageProtocol string
	// classify is the answer of ClassifyError; nil fails the call.
	classify *pluginv1.ClassifyErrorResponse
	// quotaURL is where BuildQuotaRequest points (empty: Unimplemented);
	// quotaCalls counts the requests built.
	quotaURL   string
	quotaCalls int
	// refreshURL is where BuildRefreshRequest points (empty: Unimplemented).
	refreshURL string
	// pluginCalls counts BuildRefreshRequest, ParseRefreshResponse and
	// ParseQuotaResponse calls by method name.
	pluginCalls map[string]int
	mu          sync.Mutex
	calls       []*pluginv1.ValidateCredentialsRequest
	tests       []*pluginv1.BuildTestRequestRequest
	models      []*pluginv1.BuildModelsRequestRequest
	classes     []*pluginv1.ClassifyErrorRequest
	// balance is the balance query's plugin side (balance_test.go); nil
	// answers Unimplemented.
	balance *fakeBalance
	// deadline is set when BuildTestRequest or BuildModelsRequest got a
	// context with a deadline.
	deadline bool
}

// noteDeadline records whether ctx carries a deadline (deadline).
func (p *fakePlatform) noteDeadline(ctx context.Context) {
	_, ok := ctx.Deadline()
	p.mu.Lock()
	p.deadline = p.deadline || ok
	p.mu.Unlock()
}

func (p *fakePlatform) ValidateCredentials(_ context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, in)
	p.mu.Unlock()
	if gjson.Get(in.CredentialsJson, "api_key").String() == "sk-bad-key" {
		return &pluginv1.ValidateCredentialsResponse{Errors: []*pluginv1.FieldError{{Field: "api_key", Code: "rejected", Message: "key rejected"}}}, nil
	}
	resp := &pluginv1.ValidateCredentialsResponse{}
	if bu := gjson.Get(in.SettingsJson, "base_url").String(); strings.HasSuffix(bu, "/") {
		resp.NormalizedSettingsJson = fmt.Sprintf(`{"base_url":%q}`, strings.TrimSuffix(bu, "/"))
	}
	return resp, nil
}

func (*fakePlatform) ResolveModel(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
	return nil, core.ErrInternal
}

// BuildQuotaRequest asks the fake upstream's /quota for accounts of a type
// declaring quota.query; quotaURL empty answers Unimplemented.
func (p *fakePlatform) BuildQuotaRequest(_ context.Context, in *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error) {
	p.mu.Lock()
	u := p.quotaURL
	if u != "" {
		p.quotaCalls++
	}
	p.mu.Unlock()
	if u == "" {
		return nil, status.Error(codes.Unimplemented, "not implemented")
	}
	key := gjson.Get(in.Account.CredentialsJson, "api_key").String()
	return &pluginv1.BuildQuotaRequestResponse{Url: u, Headers: map[string]string{"x-api-key": key}}, nil
}

// ParseQuotaResponse reads {"5h": <percent>, "reset": <unix>} and reports a
// non-200 answer as transient (401 as auth_rejected).
func (p *fakePlatform) ParseQuotaResponse(_ context.Context, in *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error) {
	p.count("ParseQuotaResponse")
	switch {
	case in.GetStatus() == http.StatusUnauthorized:
		return &pluginv1.QuotaResult{ErrorType: pluginv1.QuotaResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "token revoked"}, nil
	case in.GetStatus() != http.StatusOK:
		return &pluginv1.QuotaResult{ErrorType: pluginv1.QuotaResult_ERROR_TYPE_TRANSIENT,
			ErrorMessage: fmt.Sprintf("status %d %s", in.GetStatus(), in.GetTransportError())}, nil
	}
	return &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "5h",
		Utilization: gjson.GetBytes(in.Body, "5h").Float(), ResetsAtUnix: gjson.GetBytes(in.Body, "reset").Int(), Status: "allowed"}}}, nil
}

func (p *fakePlatform) ClassifyError(_ context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	p.mu.Lock()
	p.classes = append(p.classes, in)
	cls := p.classify
	p.mu.Unlock()
	if cls == nil {
		return nil, core.ErrInternal
	}
	return cls, nil
}

// BuildRefreshRequest posts the account's refresh_token to refreshURL;
// refreshURL empty answers Unimplemented.
func (p *fakePlatform) BuildRefreshRequest(_ context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	p.count("BuildRefreshRequest")
	p.mu.Lock()
	u := p.refreshURL
	p.mu.Unlock()
	if u == "" {
		return nil, status.Error(codes.Unimplemented, "not implemented")
	}
	rt := gjson.Get(in.Account.CredentialsJson, "refresh_token").String()
	return &pluginv1.BuildRefreshRequestResponse{Url: u, BodyJson: fmt.Sprintf(`{"refresh_token":%q}`, rt)}, nil
}

// ParseRefreshResponse reads {"access_token","refresh_token","expires_in"};
// 400 is auth_rejected (invalid_grant), anything else non-200 transient.
func (p *fakePlatform) ParseRefreshResponse(_ context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	p.count("ParseRefreshResponse")
	switch {
	case in.GetStatus() == http.StatusBadRequest:
		return &pluginv1.RefreshResult{ErrorType: pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "invalid_grant"}, nil
	case in.GetStatus() != http.StatusOK:
		return &pluginv1.RefreshResult{ErrorType: pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT,
			ErrorMessage: fmt.Sprintf("status %d %s", in.GetStatus(), in.GetTransportError())}, nil
	}
	patch := map[string]any{
		"access_token": gjson.GetBytes(in.Body, "access_token").String(),
		"expires_at":   time.Now().Unix() + gjson.GetBytes(in.Body, "expires_in").Int(),
		"scratch":      nil, // removed from the stored credentials
	}
	if rt := gjson.GetBytes(in.Body, "refresh_token").String(); rt != "" {
		patch["refresh_token"] = rt
	}
	b, _ := json.Marshal(patch)
	return &pluginv1.RefreshResult{CredentialsPatchJson: string(b)}, nil
}

// count records one call of method.
func (p *fakePlatform) count(method string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pluginCalls == nil {
		p.pluginCalls = map[string]int{}
	}
	p.pluginCalls[method]++
}

// called is the number of calls of method.
func (p *fakePlatform) called(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pluginCalls[method]
}

// BuildModelsRequest lists models from the fake upstream's /v1/models; the
// "vkey" account type is not a ModelLister. Each call is kept in models.
func (p *fakePlatform) BuildModelsRequest(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	p.noteDeadline(ctx)
	p.mu.Lock()
	p.models = append(p.models, in)
	p.mu.Unlock()
	if in.GetAccount().GetType() == "vkey" {
		return nil, status.Error(codes.Unimplemented, "no")
	}
	key := gjson.Get(in.Account.CredentialsJson, "api_key").String()
	return &pluginv1.BuildModelsRequestResponse{Method: "GET", Url: strings.TrimSuffix(p.testURL, "/v1/messages") + "/v1/models",
		Headers: map[string]string{"x-api-key": key}, IdsPath: "data.#.id", StripPrefix: "models/"}, nil
}

func (p *fakePlatform) BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	p.noteDeadline(ctx)
	p.mu.Lock()
	p.tests = append(p.tests, in)
	p.mu.Unlock()
	key := gjson.Get(in.Account.CredentialsJson, "api_key").String()
	return &pluginv1.BuildTestRequestResponse{Method: "POST", Url: p.testURL,
		Headers: map[string]string{"x-api-key": key}, BodyJson: fmt.Sprintf(`{"model":%q}`, in.Model),
		Model: p.testModel, UsageProtocol: p.usageProtocol}, nil
}

type fakeGen struct {
	plugins []core.PluginInfo
	types   []core.AccountTypeBinding
	plats   []core.PlatformBinding
}

func (g *fakeGen) Number() uint64             { return 1 }
func (g *fakeGen) Plugins() []core.PluginInfo { return g.plugins }
func (g *fakeGen) Plugin(key string) (core.PluginInfo, bool) {
	for _, p := range g.plugins {
		if p.Key == key {
			return p, true
		}
	}
	return core.PluginInfo{}, false
}
func (g *fakeGen) Endpoints() []core.EndpointBinding {
	var out []core.EndpointBinding
	for _, p := range g.plats {
		for _, e := range p.Platform.Endpoints {
			out = append(out, core.EndpointBinding{Plugin: p.Plugin, Platform: p.Platform.ID, Endpoint: e})
		}
	}
	return out
}
func (g *fakeGen) Platforms() []core.PlatformBinding { return g.plats }
func (g *fakeGen) Platform(id string) (core.PlatformBinding, bool) {
	for _, p := range g.plats {
		if p.Platform.ID == id {
			return p, true
		}
	}
	return core.PlatformBinding{}, false
}
func (g *fakeGen) PlatformForProtocol(protocol string) (core.PlatformBinding, bool) {
	for _, p := range g.plats {
		for _, e := range p.Platform.Endpoints {
			if e.Protocol == protocol {
				return p, true
			}
		}
	}
	return core.PlatformBinding{}, false
}
func (g *fakeGen) AccountTypes() []core.AccountTypeBinding { return g.types }
func (g *fakeGen) AccountType(pluginKey, typ string) (core.AccountTypeBinding, bool) {
	for _, t := range g.types {
		if t.Plugin.Key == pluginKey && t.Type.ID == typ {
			return t, true
		}
	}
	return core.AccountTypeBinding{}, false
}
func (g *fakeGen) AccountTypesForPlatform(platformID string) []core.AccountTypeBinding {
	var out []core.AccountTypeBinding
	for _, t := range g.types {
		if _, ok := t.Supports(platformID); ok {
			out = append(out, t)
		}
	}
	return out
}
func (g *fakeGen) Hooks(string) []core.HookBinding                  { return nil }
func (g *fakeGen) Scheduler(string) (core.SchedulerPlugin, bool)    { return nil, false }
func (g *fakeGen) AccountRankers() []core.AccountRankerBinding      { return nil }
func (g *fakeGen) Routes(string) []core.RouteBinding                { return nil }
func (g *fakeGen) Jobs() []core.JobBinding                          { return nil }
func (g *fakeGen) JobRunner(string) (core.AppPlugin, bool)          { return nil, false }
func (g *fakeGen) Subscriptions() []core.SubscriptionBinding        { return nil }
func (g *fakeGen) ReadAsset(string, string) ([]byte, string, error) { return nil, "", core.ErrNotFound }

type fakeRegistry struct {
	mu  sync.Mutex
	gen core.Generation
}

func (r *fakeRegistry) Current() core.Generation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}
func (r *fakeRegistry) set(g core.Generation) {
	r.mu.Lock()
	r.gen = g
	r.mu.Unlock()
}
func (r *fakeRegistry) OnChange(func(core.Generation)) func() { return func() {} }

// dbEvents writes events into the outbox table inside the caller's tx.
type dbEvents struct{}

func (dbEvents) Emit(ctx context.Context, tx *gorm.DB, evs ...core.Event) error {
	for _, e := range evs {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		if _, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO events (type, payload) VALUES ($1, $2::jsonb)`, e.Type, string(b)); err != nil {
			return err
		}
	}
	return nil
}

type fakeBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
	sent []string
}

func (b *fakeBus) Publish(_ context.Context, ch string, p []byte) error {
	b.mu.Lock()
	b.sent = append(b.sent, ch)
	hs := append([]func([]byte){}, b.subs[ch]...)
	b.mu.Unlock()
	for _, h := range hs {
		h(p)
	}
	return nil
}

func (b *fakeBus) Subscribe(ch string, h func([]byte)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[string][]func([]byte){}
	}
	b.subs[ch] = append(b.subs[ch], h)
	return func() {}
}

func (b *fakeBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}

type fakeSlots struct{}

func (fakeSlots) Acquire(context.Context, string, int64, int, string) (func(), bool, error) {
	return func() {}, true, nil
}
func (fakeSlots) InUse(_ context.Context, kind string, id int64) (int, error) {
	if strings.HasPrefix(kind, core.ModelSlots("")) {
		return 1, nil
	}
	if kind != "account" {
		return 0, fmt.Errorf("kind %s", kind)
	}
	return int(id % 7), nil
}
func (fakeSlots) InUseMany(_ context.Context, kind string, ids []int64) (map[int64]int, error) {
	if kind != "account" {
		return nil, fmt.Errorf("kind %s", kind)
	}
	result := make(map[int64]int, len(ids))
	for _, id := range ids {
		result[id] = int(id % 7)
	}
	return result, nil
}

type fakeTokens struct{ testutil.StepUpPassed }

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "u")
}

// VerifyAccessKey reads "sak_<uid>" as an access key acting for uid.
func (fakeTokens) VerifyAccessKey(_ context.Context, raw, _ string) (core.AccessKeyIdentity, error) {
	uid, err := strconv.ParseInt(strings.TrimPrefix(raw, core.AccessKeyPrefix), 10, 64)
	return core.AccessKeyIdentity{UserID: uid, KeyID: 1}, err
}

// fakeAuthz grants each user a fixed key set; a nil map allows everything.
// A check of a key in undecided cannot be decided (503).
type fakeAuthz struct {
	keys      map[int64][]string
	undecided map[string]bool
}

func (a fakeAuthz) Can(ctx context.Context, uid int64, key string) (bool, error) {
	if a.undecided[key] {
		return false, core.ErrUnavailable
	}
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(key), err
}

func (a fakeAuthz) PermissionSet(ctx context.Context, uid int64) (core.PermissionSet, error) {
	if a.keys == nil {
		return core.PermissionSet{Superuser: true}, nil
	}
	return core.RestrictToAccessKey(ctx, uid, core.PermissionSetOf(a.keys[uid]...)), nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

// fakeConverters converts openai.chat requests to anthropic.messages,
// openai.images to openai.responses (for a type declaring it adapted), and
// (unused, the platform is unavailable) openai.embeddings to ghost.embed.
type fakeConverters struct{}

func (f fakeConverters) Conversion(client, upstream string) (core.Conversion, bool) {
	return fairConversion(client, upstream, f.canConvert(client, upstream))
}

func (fakeConverters) canConvert(client, upstream string) bool {
	return client == "openai.chat" && upstream == "anthropic.messages" ||
		client == "openai.images" && upstream == "openai.responses" ||
		client == "openai.embeddings" && upstream == "ghost.embed"
}

// fairConversion is a one-hop fair conversion when ok.
func fairConversion(client, upstream string, ok bool) (core.Conversion, bool) {
	if !ok {
		return core.Conversion{}, false
	}
	return core.Conversion{Rank: 2, Grade: "fair", Hops: []string{client, upstream}}, true
}

func ep(method, path, protocol, billing string) manifest.Endpoint {
	return manifest.Endpoint{Method: method, Path: path, Protocol: protocol, Billing: billing}
}

// testGen is a generation with the built-in anthropic and openai platforms, a
// plugin platform "aivideo", and account types: anthropic/apikey (anthropic;
// base_url guarded to the official address, CONTRACTS §21.3), relay/relay_key
// (anthropic and the unavailable plugin platform "ghost"; unguarded) and
// video/vkey (aivideo). openai endpoints are only reachable through conversion.
func testGen(plat core.PlatformPlugin) *fakeGen {
	anthropic := core.PluginInfo{Key: "anthropic", Version: "0.1.0", AssetBase: "/plugin-ui/anthropic/0.1.0-abc",
		Trust: "official", Manifest: &manifest.Manifest{Name: manifest.LocalizedText{"en": "Anthropic"}}}
	relay := core.PluginInfo{Key: "relay", Version: "1.0.0", AssetBase: "/plugin-ui/relay/1.0.0-def", Trust: "community",
		Manifest: &manifest.Manifest{Name: manifest.LocalizedText{"en": "Relay"}}}
	video := core.PluginInfo{Key: "video", Version: "0.2.0", Trust: "community",
		Manifest: &manifest.Manifest{Name: manifest.LocalizedText{"en": "Video"}}}
	return &fakeGen{
		plugins: []core.PluginInfo{anthropic, relay, video},
		types: []core.AccountTypeBinding{{
			Plugin: anthropic,
			Type: manifest.AccountType{ID: "apikey", Label: manifest.LocalizedText{"en": "API Key", "zh": "API Key"},
				Form: manifest.Form{Mode: "native", Component: "AnthropicKeyForm"}, SensitiveFields: []string{"api_key"},
				SettingsFields:  []string{"base_url"},
				GuardedSettings: []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{officialBaseURL}}},
				UpstreamFields:  []string{"base_url"},
				Platforms:       []manifest.AccountPlatform{{Platform: "anthropic"}}},
			Client: plat,
		}, {
			Plugin: relay,
			Type: manifest.AccountType{ID: "relay_key", Label: manifest.LocalizedText{"en": "Relay key"},
				Form: manifest.Form{Mode: "native", Component: "RelayKeyForm"}, SensitiveFields: []string{"api_key"},
				SettingsFields: []string{"base_url"},
				UpstreamFields: []string{"base_url"},
				Platforms:      []manifest.AccountPlatform{{Platform: "anthropic"}, {Platform: "ghost"}}},
			Client: plat,
		}, {
			Plugin: video,
			Type: manifest.AccountType{ID: "vkey", Label: manifest.LocalizedText{"en": "Video key"},
				Form: manifest.Form{Mode: "native", Component: "VideoKeyForm"}, UpstreamFields: []string{},
				Platforms: []manifest.AccountPlatform{{Platform: "aivideo"}}},
			Client: plat,
		}},
		// Plugin platform first: /platforms must still list built-ins first.
		plats: []core.PlatformBinding{
			{Plugin: video, Platform: manifest.Platform{ID: "aivideo", Label: manifest.LocalizedText{"en": "AI Video"},
				Endpoints: []manifest.Endpoint{ep("POST", "/v1/video/generations", "aivideo.gen", "usage")}}},
			{Builtin: true, Platform: manifest.Platform{ID: "openai", Label: manifest.LocalizedText{"en": "OpenAI"},
				Endpoints: []manifest.Endpoint{ep("POST", "/v1/chat/completions", "openai.chat", "usage"),
					ep("POST", "/v1/embeddings", "openai.embeddings", "usage")}}},
			{Builtin: true, Platform: manifest.Platform{ID: "anthropic", Label: manifest.LocalizedText{"en": "Anthropic"},
				Endpoints: []manifest.Endpoint{ep("POST", "/v1/messages", "anthropic.messages", "usage"),
					ep("POST", "/v1/messages/count_tokens", "anthropic.count_tokens", "free")}}},
		},
	}
}

// endpointList renders endpoints as "METHOD path platform grade".
func endpointList(eps []EndpointView) string {
	var s []string
	for _, e := range eps {
		if e.Native != (e.Grade == "native") {
			return "native flag disagrees with grade " + e.Grade
		}
		s = append(s, e.Method+" "+e.Path+" "+e.Platform+" "+e.Grade)
	}
	return strings.Join(s, "; ")
}

// platformList renders type platforms as "id:label:builtin:available".
func platformList(ps []TypePlatformView) string {
	var s []string
	for _, p := range ps {
		s = append(s, fmt.Sprintf("%s:%s:%v:%v", p.ID, p.Label["en"], p.Builtin, p.Available))
	}
	return strings.Join(s, "; ")
}

func TestTypeViewEndpoints(t *testing.T) {
	g := testGen(&fakePlatform{})
	v := typeView(g, g.types[0], fakeConverters{})
	if v.PluginKey != "anthropic" || v.Type != "apikey" || v.Trust != "official" || v.PluginName["en"] != "Anthropic" ||
		v.AssetBase == "" {
		t.Fatalf("view: %+v", v)
	}
	if got := platformList(v.Platforms); got != "anthropic:Anthropic:true:true" {
		t.Fatalf("platforms: %s", got)
	}
	want := "POST /v1/messages anthropic native; POST /v1/messages/count_tokens anthropic native; " +
		"POST /v1/chat/completions openai fair"
	if got := endpointList(v.Endpoints); got != want {
		t.Fatalf("endpoints:\n got %s\nwant %s", got, want)
	}
	// Without converters only the endpoints of supported platforms are
	// listed; an unavailable platform has no label and serves nothing (even
	// when a converter targets it).
	v = typeView(g, g.types[1], nil)
	if got := endpointList(v.Endpoints); got != "POST /v1/messages anthropic native; POST /v1/messages/count_tokens anthropic native" {
		t.Fatalf("relay endpoints: %s", got)
	}
	if got := platformList(v.Platforms); got != "anthropic:Anthropic:true:true; ghost::false:false" {
		t.Fatalf("relay platforms: %s", got)
	}
	if v.Platforms[1].Label != nil {
		t.Fatalf("unavailable platform label: %v", v.Platforms[1].Label)
	}
	if v.PluginName["en"] != "Relay" || len(v.SensitiveFields) != 1 {
		t.Fatalf("relay view: %+v", v)
	}
	v = typeView(g, g.types[1], fakeConverters{})
	if got := endpointList(v.Endpoints); got != "POST /v1/messages anthropic native; POST /v1/messages/count_tokens anthropic native; "+
		"POST /v1/chat/completions openai fair" {
		t.Fatalf("relay endpoints with converters: %s", got)
	}
	// A plugin platform.
	v = typeView(g, g.types[2], fakeConverters{})
	if got := endpointList(v.Endpoints); got != "POST /v1/video/generations aivideo native" {
		t.Fatalf("video endpoints: %s", got)
	}
	if got := platformList(v.Platforms); got != "aivideo:AI Video:false:true" {
		t.Fatalf("video platforms: %s", got)
	}
	// No manifest name: falls back to the key; nil slices become [].
	b := core.AccountTypeBinding{Plugin: core.PluginInfo{Key: "x"}, Type: manifest.AccountType{ID: "t"}}
	v = typeView(g, b, fakeConverters{})
	if v.PluginName["en"] != "x" || v.SensitiveFields == nil || v.Platforms == nil || v.Endpoints == nil || len(v.Endpoints) != 0 {
		t.Fatalf("empty view: %+v", v)
	}
	raw, _ := json.Marshal(typeView(g, g.types[1], nil))
	if gjson.GetBytes(raw, "protocols").Exists() || gjson.GetBytes(raw, "platforms.1.label").Raw != "null" ||
		gjson.GetBytes(raw, "platforms.0.label.en").String() != "Anthropic" || gjson.GetBytes(raw, "endpoints.0.platform").String() != "anthropic" {
		t.Fatalf("json: %s", raw)
	}
}

func TestPlatformViews(t *testing.T) {
	g := testGen(&fakePlatform{})
	vs := platformViews(g)
	var got []string
	for _, v := range vs {
		var types, eps []string
		for _, at := range v.AccountTypes {
			types = append(types, at.PluginKey+"/"+at.Type+"="+at.Label["en"])
		}
		for _, e := range v.Endpoints {
			eps = append(eps, e.Method+" "+e.Path+" "+e.Protocol+" "+e.Billing)
		}
		pk := "-"
		if v.PluginKey != nil {
			pk = *v.PluginKey
		}
		got = append(got, fmt.Sprintf("%s(%s,%v,%s) [%s] [%s]", v.ID, v.Label["en"], v.Builtin, pk,
			strings.Join(eps, ", "), strings.Join(types, ", ")))
	}
	want := []string{
		"anthropic(Anthropic,true,-) [POST /v1/messages anthropic.messages usage, POST /v1/messages/count_tokens anthropic.count_tokens free] " +
			"[anthropic/apikey=API Key, relay/relay_key=Relay key]",
		"openai(OpenAI,true,-) [POST /v1/chat/completions openai.chat usage, POST /v1/embeddings openai.embeddings usage] []",
		"aivideo(AI Video,false,video) [POST /v1/video/generations aivideo.gen usage] [video/vkey=Video key]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("platforms:\n got %s\nwant %s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	raw, _ := json.Marshal(vs)
	if gjson.GetBytes(raw, "0.plugin_key").Raw != "null" || gjson.GetBytes(raw, "2.plugin_key").String() != "video" ||
		gjson.GetBytes(raw, "1.account_types").Raw != "[]" {
		t.Fatalf("json: %s", raw)
	}
	if out := platformViews(nil); out == nil || len(out) != 0 {
		t.Fatalf("nil generation: %v", out)
	}
	if out := platformViews(&fakeGen{}); out == nil || len(out) != 0 {
		t.Fatalf("empty generation: %v", out)
	}
}

func TestTypeViewEndpointSubset(t *testing.T) {
	g := testGen(&fakePlatform{})
	// Give the fixture platform-local IDs just like the real platform catalog.
	for i := range g.plats {
		for j := range g.plats[i].Platform.Endpoints {
			e := &g.plats[i].Platform.Endpoints[j]
			e.ID = strings.TrimPrefix(e.Protocol, g.plats[i].Platform.ID+".")
		}
	}
	b := g.types[0]
	b.Type.Platforms = []manifest.AccountPlatform{{Platform: "anthropic", Endpoints: []string{"count_tokens"}}}
	got := endpointList(typeView(g, b, fakeConverters{}).Endpoints)
	if got != "POST /v1/messages/count_tokens anthropic native" {
		t.Fatalf("excluded native/conversion endpoints leaked: %s", got)
	}
	b.Type.Platforms[0].Endpoints = []string{"messages"}
	got = endpointList(typeView(g, b, fakeConverters{}).Endpoints)
	// count_tokens left out of the subset is estimated locally (CONVERT-MATRIX §8).
	if got != "POST /v1/messages anthropic native; POST /v1/messages/count_tokens anthropic local; POST /v1/chat/completions openai fair" {
		t.Fatalf("selected endpoint and its conversion: %s", got)
	}
}

func TestPlatformsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := &fakeRegistry{}
	reg.set(testGen(&fakePlatform{}))
	s := New(Deps{Registry: reg, Converters: fakeConverters{}})
	engine := gin.New()
	s.RegisterRoutes(httpapi.NewRouter(engine, fakeTokens{}, fakeAuthz{}))
	for path, n := range map[string]int{"/api/v1/platforms": 3, "/api/v1/account-types": 3} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer u1")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != 200 || len(gjson.Get(w.Body.String(), "data").Array()) != n {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestCandidatesFilterByType(t *testing.T) {
	s := New(Deps{})
	refs := []core.AccountRef{
		{ID: 1, PluginKey: "anthropic", Type: "apikey"},
		{ID: 2, PluginKey: "relay", Type: "relay_key"},
		{ID: 3, PluginKey: "anthropic", Type: "oauth"},
		{ID: 4, PluginKey: "relay", Type: "apikey"},
	}
	s.groups[7] = groupSnap{at: time.Now(), refs: refs}
	ids := func(types ...core.AccountTypeKey) string {
		t.Helper()
		out, err := s.Candidates(context.Background(), 7, types)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range out {
			ids = append(ids, strconv.FormatInt(r.ID, 10))
		}
		return strings.Join(ids, ",")
	}
	if got := ids(core.AccountTypeKey{PluginKey: "anthropic", Type: "apikey"}, core.AccountTypeKey{PluginKey: "relay", Type: "relay_key"}); got != "1,2" {
		t.Fatalf("mixed types: %s", got)
	}
	if got := ids(core.AccountTypeKey{PluginKey: "relay", Type: "apikey"}); got != "4" {
		t.Fatalf("same type id, other plugin: %s", got)
	}
	if got := ids(); got != "1,2,3,4" {
		t.Fatalf("all types: %s", got)
	}
	if got := ids(core.AccountTypeKey{PluginKey: "openai", Type: "apikey"}); got != "" {
		t.Fatalf("no match: %s", got)
	}
}

func TestPayloads(t *testing.T) {
	until := time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC)
	b, _ := json.Marshal(statusPayload(5, "relay", "relay_key", "n", "cooldown", "429", &until))
	if string(b) != `{"account_id":5,"cooldown_until":"2026-09-24T01:02:03Z","name":"n","plugin_key":"relay","reason":"429","status":"cooldown","type":"relay_key"}` {
		t.Fatalf("status payload: %s", b)
	}
	b, _ = json.Marshal(basicPayload(5, "relay", "relay_key", "n"))
	if string(b) != `{"account_id":5,"name":"n","plugin_key":"relay","type":"relay_key"}` {
		t.Fatalf("basic payload: %s", b)
	}
}

// ---------------------------------------------------------------- harness

// officialBaseURL is the only base_url the guarded anthropic/apikey type
// accepts from callers without account:settings:custom.
const officialBaseURL = "https://api.anthropic.com"

type env struct {
	t        *testing.T
	db       *store.DB
	svc      *Service
	prx      *proxy.Service
	h        http.Handler
	mr       *miniredis.Miniredis
	bus      *fakeBus
	reg      *fakeRegistry
	gen      *fakeGen
	plat     *fakePlatform
	upstream *httptest.Server
	uid      int64
}

// setup builds the harness with an admin user allowed to do everything.
func setup(t *testing.T) *env {
	return setupWith(t, fakeAuthz{})
}

// setupWith builds the harness with the given authorizer (shared by the
// router and the service). The admin user is created first; tests add more
// users with addUser and call as them with doAs.
func setupWith(t *testing.T, az fakeAuthz) *env {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cipher, _ := secret.New(key)

	e := &env{t: t, db: db, mr: mr, bus: &fakeBus{}, reg: &fakeRegistry{}}
	e.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good-key-123" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid x-api-key"}`))
			return
		}
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-5"},{"id":"models/claude-opus-4-1"},{"id":"bad model"},{"id":"claude-sonnet-4-5"},{"id":""}]}`))
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(e.upstream.Close)
	e.plat = &fakePlatform{testURL: e.upstream.URL + "/v1/messages"}
	e.gen = testGen(e.plat)
	e.reg.set(e.gen)
	e.uid = e.addUser("admin@x.com")
	// The real proxy service serves as directory and resolver so proxy_url
	// tests exercise FindOrCreate / AuditAutoCreate end to end.
	e.prx = proxy.New(db, cipher, e.bus, proxy.Options{AllowPrivate: true})
	e.svc = New(Deps{DB: db, Redis: rdb, Cipher: cipher, Registry: e.reg, Proxies: e.prx, Resolver: e.prx, Authorizer: az, ScopeGrants: authz.New(authz.Deps{}),
		Events: dbEvents{}, Slots: fakeSlots{}, Limiter: NewLimiter(rdb), Bus: e.bus, Converters: fakeConverters{}, AllowPrivateUpstream: true})
	// Run warms the quota mirror, which reads the account types: wait for that
	// first read so it does not race the types a test adds.
	warmed := make(chan struct{})
	var warmOnce sync.Once
	e.svc.quotaMirror = newQuotaMirror(e.svc.readQuotas, func(ctx context.Context) (*store.QuotaRead, error) {
		defer warmOnce.Do(func() { close(warmed) })
		return e.svc.readAvailabilityQuotas(ctx)
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.svc.Run(ctx)
	select {
	case <-warmed:
	case <-time.After(5 * time.Second):
		t.Fatal("quota mirror not warmed")
	}
	engine := gin.New()
	e.svc.RegisterRoutes(httpapi.NewRouter(engine, fakeTokens{}, az))
	e.h = engine
	return e
}

func (e *env) addUser(email string) int64 {
	e.t.Helper()
	return e.exec1(`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email)
}

func (e *env) exec1(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	if err := e.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// do calls the API as the admin user.
func (e *env) do(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	return e.doAs(e.uid, method, path, body)
}

// doAs calls the API as uid and decodes the JSON envelope.
func (e *env) doAs(uid int64, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	code, raw := e.doRaw(uid, method, path, body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return code, out
}

// doRaw calls the API as uid and returns the raw body.
func (e *env) doRaw(uid int64, method, path string, body any) (int, []byte) {
	e.t.Helper()
	return e.callRaw(fmt.Sprintf("u%d", uid), method, path, body)
}

// callRaw calls the API with bearer as the token and returns the raw body.
func (e *env) callRaw(bearer, method, path string, body any) (int, []byte) {
	e.t.Helper()
	return e.callCtx(context.Background(), bearer, method, path, body)
}

// callCtx is callRaw with the request context ctx (canceling it is the
// client leaving).
func (e *env) callCtx(ctx context.Context, bearer, method, path string, body any) (int, []byte) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequestWithContext(ctx, method, "/api/v1"+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func fieldsOf(out map[string]any) []string {
	var fs []string
	err, _ := out["error"].(map[string]any)
	det, _ := err["details"].(map[string]any)
	list, _ := det["fields"].([]any)
	for _, f := range list {
		fs = append(fs, f.(map[string]any)["field"].(string)+":"+f.(map[string]any)["code"].(string))
	}
	return fs
}

func (e *env) eventTypes() []string {
	rows, err := e.db.SQL.QueryContext(context.Background(), `SELECT type FROM events ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	ts, err := testutil.Column[string](rows)
	if err != nil {
		e.t.Fatal(err)
	}
	return ts
}

// ---------------------------------------------------------------- tests

func TestAccountTypes(t *testing.T) {
	e := setup(t)
	code, out := e.do("GET", "/account-types", nil)
	if code != 200 {
		t.Fatalf("types: %d %v", code, out)
	}
	list := out["data"].([]any)
	ty := list[0].(map[string]any)
	if len(list) != 3 || ty["plugin_key"] != "anthropic" || ty["type"] != "apikey" || ty["form"].(map[string]any)["mode"] != "native" ||
		ty["plugin_name"].(map[string]any)["en"] != "Anthropic" || ty["sensitive_fields"].([]any)[0] != "api_key" ||
		ty["trust"] != "official" || ty["protocols"] != nil || ty["platform"] != nil || ty["custom_upstream"] != false ||
		ty["platforms"].([]any)[0].(map[string]any)["id"] != "anthropic" {
		t.Fatalf("type view: %v", ty)
	}
	eps := ty["endpoints"].([]any)
	if len(eps) != 3 || eps[2].(map[string]any)["path"] != "/v1/chat/completions" || eps[2].(map[string]any)["native"] != false ||
		eps[2].(map[string]any)["platform"] != "openai" || eps[0].(map[string]any)["native"] != true {
		t.Fatalf("endpoints: %v", eps)
	}
	if rl := list[1].(map[string]any); rl["plugin_key"] != "relay" || rl["type"] != "relay_key" || rl["custom_upstream"] != true {
		t.Fatalf("relay type: %v", rl)
	}
	// Forms are the plugins' own components: the core serves no form schema.
	if code, _ = e.do("GET", "/account-types/anthropic/apikey/form", nil); code != 404 {
		t.Fatalf("form schema endpoint: %d", code)
	}
	e.reg.set(nil)
	if _, out = e.do("GET", "/account-types", nil); len(out["data"].([]any)) != 0 {
		t.Fatalf("nil generation: %v", out)
	}
}

func TestAccountLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	g1 := e.exec1(`INSERT INTO groups (name) VALUES ('default') RETURNING id`)
	g2 := e.exec1(`INSERT INTO groups (name) VALUES ('vip') RETURNING id`)

	base := map[string]any{"name": "claude-main", "plugin_key": "anthropic", "type": "apikey",
		"group_ids": []int64{g1, g2}, "priority": 1, "max_concurrency": 5}
	with := func(creds map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["credentials"] = creds
		return m
	}

	// Plugin validation, the only check of the credentials.
	code, out := e.do("POST", "/accounts", with(map[string]any{"api_key": "sk-bad-key"}))
	if code != 400 || fmt.Sprint(fieldsOf(out)) != "[credentials.api_key:rejected]" {
		t.Fatalf("plugin validation: %d %v", code, out)
	}
	// Unknown type / group.
	bad := with(map[string]any{"api_key": "sk-good-key-123"})
	bad["type"] = "oauth"
	if code, _ = e.do("POST", "/accounts", bad); code != 400 {
		t.Fatalf("unknown type: %d", code)
	}
	bad = with(map[string]any{"api_key": "sk-good-key-123"})
	bad["plugin_key"] = "openai" // the type id exists, but for another plugin
	if code, _ = e.do("POST", "/accounts", bad); code != 400 {
		t.Fatalf("type of another plugin: %d", code)
	}
	bad = with(map[string]any{"api_key": "sk-good-key-123"})
	delete(bad, "plugin_key")
	if code, out = e.do("POST", "/accounts", bad); code != 400 || fmt.Sprint(fieldsOf(out)) != "[type:required]" {
		t.Fatalf("missing plugin_key: %d %v", code, out)
	}
	bad = with(map[string]any{"api_key": "sk-good-key-123"})
	bad["group_ids"] = []int64{999}
	if code, _ = e.do("POST", "/accounts", bad); code != 400 {
		t.Fatalf("unknown group: %d", code)
	}

	// Success, with normalized settings.
	sent := e.bus.count()
	code, out = e.do("POST", "/accounts", with(map[string]any{"api_key": "sk-good-key-123",
		"base_url": "https://api.example.com/", "org": "acme"}))
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	acc := out["data"].(map[string]any)
	id := int64(acc["id"].(float64))
	creds := acc["credentials"].(map[string]any)
	if creds["api_key"] != core.SecretMask || creds["org"] != "acme" || creds["base_url"] != "https://api.example.com" {
		t.Fatalf("masked creds: %v", creds)
	}
	if acc["settings"].(map[string]any)["base_url"] != "https://api.example.com" || len(acc["group_ids"].([]any)) != 2 ||
		acc["plugin_key"] != "anthropic" || acc["type"] != "apikey" || acc["type_label"].(map[string]any)["en"] != "API Key" ||
		acc["platform"] != nil {
		t.Fatalf("create view: %v", acc)
	}
	var enc, settings []byte
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT credentials_enc, settings FROM accounts WHERE id = $1`, id).Scan(&enc, &settings)
	if bytes.Contains(enc, []byte("sk-good")) || bytes.Contains(settings, []byte("api_key")) {
		t.Fatal("secret leaked into plain columns")
	}
	if e.bus.count() != sent+1 {
		t.Fatal("account:changed not published")
	}
	last := e.plat.calls[len(e.plat.calls)-1]
	if gjson.Get(last.CredentialsJson, "base_url").Exists() || !gjson.Get(last.SettingsJson, "base_url").Exists() {
		t.Fatalf("plugin got wrong split: %v", last)
	}

	// PATCH keeping the masked secret.
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{
		"credentials": map[string]any{"api_key": core.SecretMask, "base_url": "https://b.example.com", "org": "acme2"},
		"group_ids":   []int64{g2}, "status": "disabled"})
	if code != 200 {
		t.Fatalf("patch: %d %v", code, out)
	}
	acc = out["data"].(map[string]any)
	if acc["status"] != "disabled" || len(acc["group_ids"].([]any)) != 1 || acc["credentials"].(map[string]any)["org"] != "acme2" {
		t.Fatalf("patch view: %v", acc)
	}
	code, out = e.do("POST", fmt.Sprintf("/accounts/%d/credentials/reveal", id), nil)
	if code != 200 {
		t.Fatalf("reveal: %d %v", code, out)
	}
	plain := out["data"].(map[string]any)["credentials"].(map[string]any)
	if plain["api_key"] != "sk-good-key-123" || plain["base_url"] != "https://b.example.com" {
		t.Fatalf("reveal: %v", plain)
	}
	key := core.AccessKeyPrefix + strconv.FormatInt(e.uid, 10)
	// An access key with the permission reveals too, after the step-up
	// (passed here; iam tests the proofs).
	if code, raw := e.callRaw(key, "POST", fmt.Sprintf("/accounts/%d/credentials/reveal", id), nil); code != 200 {
		t.Fatalf("access key reveal: %d %s", code, raw)
	}
	var audits int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'account.credentials.reveal'`).Scan(&audits)
	if audits != 2 {
		t.Fatal("reveal not audited")
	}
	// Resubmitting the same credentials keeps the stored ciphertext; a real
	// change re-encrypts it.
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT credentials_enc FROM accounts WHERE id = $1`, id).Scan(&enc)
	same := map[string]any{"credentials": map[string]any{"api_key": core.SecretMask, "base_url": "https://b.example.com", "org": "acme2"}, "name": "same credentials"}
	if code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), same); code != 200 {
		t.Fatalf("patch same credentials: %d %v", code, out)
	}
	var after []byte
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT credentials_enc FROM accounts WHERE id = $1`, id).Scan(&after)
	if !bytes.Equal(enc, after) {
		t.Fatal("unchanged credentials were re-encrypted")
	}
	same["credentials"].(map[string]any)["org"] = "acme3"
	if code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), same); code != 200 {
		t.Fatalf("patch changed credentials: %d %v", code, out)
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT credentials_enc FROM accounts WHERE id = $1`, id).Scan(&after)
	if bytes.Equal(enc, after) {
		t.Fatal("changed credentials kept the old ciphertext")
	}
	// PATCH other fields only; proxy null clears; unknown proxy rejected.
	if code, _ = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"proxy_id": 12345}); code != 400 {
		t.Fatalf("unknown proxy: %d", code)
	}
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"status": "active", "proxy_id": nil, "name": "renamed"})
	if code != 200 || out["data"].(map[string]any)["name"] != "renamed" || out["data"].(map[string]any)["status_reason"] != "" {
		t.Fatalf("patch 2: %d %v", code, out)
	}

	// List: in_use, cooldown, filters.
	if err := e.svc.SetCooldown(ctx, id, time.Now().Add(time.Minute), "429"); err != nil {
		t.Fatal(err)
	}
	code, out = e.do("GET", fmt.Sprintf("/accounts?group_id=%d&plugin_key=anthropic&type=apikey&q=ren", g2), nil)
	if code != 200 || out["page"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	item := out["data"].([]any)[0].(map[string]any)
	if item["in_use"].(float64) != float64(id%7) || item["cooldown_until"] == nil || item["cooldown_reason"] != "429" ||
		item["orphaned"] != false || item["credentials"] != nil || item["type_label"].(map[string]any)["en"] != "API Key" {
		t.Fatalf("list item: %v", item)
	}
	if _, out = e.do("GET", fmt.Sprintf("/accounts?group_id=%d", g1), nil); out["page"].(map[string]any)["total"].(float64) != 0 {
		t.Fatalf("group filter: %v", out)
	}
	if _, out = e.do("GET", "/accounts?plugin_key=relay&type=relay_key", nil); out["page"].(map[string]any)["total"].(float64) != 0 {
		t.Fatalf("type filter: %v", out)
	}
	// An account type is (plugin key, type id): type alone is refused.
	if code, out = e.do("GET", "/accounts?type=apikey", nil); code != 400 {
		t.Fatalf("type without plugin_key: %d %v", code, out)
	}
	// ids= narrows to the given accounts, ANDed with the other filters.
	if code, out = e.do("GET", fmt.Sprintf("/accounts?ids=%d,%d", id, id+1000), nil); code != 200 || out["page"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("ids filter: %d %v", code, out)
	}
	if _, out = e.do("GET", fmt.Sprintf("/accounts?ids=%d&group_id=%d", id, g1), nil); out["page"].(map[string]any)["total"].(float64) != 0 {
		t.Fatalf("ids AND group filter: %v", out)
	}
	for _, q := range []string{"ids=1,x", "ids=" + strings.Repeat("1,", 500) + "1"} {
		if code, out = e.do("GET", "/accounts?"+q, nil); code != 400 {
			t.Fatalf("%.30s: %d %v", q, code, out)
		}
	}

	// Orphaned when the plugin leaves the generation; credentials fully masked
	// and cannot be changed.
	e.reg.set(&fakeGen{})
	_, out = e.do("GET", fmt.Sprintf("/accounts/%d", id), nil)
	acc = out["data"].(map[string]any)
	if acc["orphaned"] != true || acc["credentials"].(map[string]any)["org"] != core.SecretMask || acc["type_label"] != nil {
		t.Fatalf("orphaned view: %v", acc)
	}
	if code, _ = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"credentials": map[string]any{"api_key": "sk-good-key-456"}}); code != 503 {
		t.Fatalf("orphaned patch: %d", code)
	}
	// Hidden from the list by default; orphaned=true / all show it.
	if _, out = e.do("GET", "/accounts", nil); len(out["data"].([]any)) != 0 {
		t.Fatalf("orphaned account listed by default: %v", out["data"])
	}
	for _, q := range []string{"orphaned=true", "orphaned=all"} {
		_, out = e.do("GET", "/accounts?"+q, nil)
		if items := out["data"].([]any); len(items) != 1 || items[0].(map[string]any)["orphaned"] != true {
			t.Fatalf("%s: %v", q, out["data"])
		}
	}
	e.reg.set(e.gen)
	if _, out = e.do("GET", "/accounts?orphaned=true", nil); len(out["data"].([]any)) != 0 {
		t.Fatalf("orphaned=true with the plugin back: %v", out["data"])
	}

	// Delete.
	if code, _ = e.do("DELETE", fmt.Sprintf("/accounts/%d", id), nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = e.do("GET", fmt.Sprintf("/accounts/%d", id), nil); code != 404 {
		t.Fatalf("get deleted: %d", code)
	}
	if e.mr.Exists(cooldownKey(id)) {
		t.Fatal("cooldown kept after delete")
	}
	want := "[account.created account.updated account.status_changed account.updated account.updated account.updated account.status_changed account.status_changed account.deleted]"
	if got := fmt.Sprint(e.eventTypes()); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
	var payload string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT payload::text FROM events WHERE type = 'account.created'`).Scan(&payload)
	if gjson.Get(payload, "plugin_key").String() != "anthropic" || gjson.Get(payload, "account_id").Int() != id ||
		gjson.Get(payload, "type").String() != "apikey" || gjson.Get(payload, "platform").Exists() {
		t.Fatalf("payload: %s", payload)
	}
}

func TestAccountTestAction(t *testing.T) {
	e := setup(t)
	mk := func(key string) int64 {
		code, out := e.do("POST", "/accounts", map[string]any{"name": key, "plugin_key": "anthropic", "type": "apikey",
			"credentials": map[string]any{"api_key": key}})
		if code != 201 {
			t.Fatalf("create: %d %v", code, out)
		}
		return int64(out["data"].(map[string]any)["id"].(float64))
	}
	good, bad := mk("sk-good-key-123"), mk("sk-other-key-1")
	code, out := e.do("POST", fmt.Sprintf("/accounts/%d/test", good), map[string]any{"model": "claude-x"})
	res := out["data"].(map[string]any)
	if code != 200 || res["ok"] != true || res["status"].(float64) != 200 {
		t.Fatalf("test good: %d %v", code, out)
	}
	_, out = e.do("POST", fmt.Sprintf("/accounts/%d/test", bad), nil)
	res = out["data"].(map[string]any)
	if res["ok"] != false || res["status"].(float64) != 401 || !strings.Contains(res["message"].(string), "invalid x-api-key") {
		t.Fatalf("test bad: %v", res)
	}
	// Private address guard.
	e.svc.d.AllowPrivateUpstream = false
	_, out = e.do("POST", fmt.Sprintf("/accounts/%d/test", good), nil)
	res = out["data"].(map[string]any)
	if res["ok"] != false || !strings.Contains(res["message"].(string), "private") {
		t.Fatalf("guard: %v", res)
	}
	// Plugin gone.
	e.reg.set(&fakeGen{})
	if code, _ = e.do("POST", fmt.Sprintf("/accounts/%d/test", good), nil); code != 503 {
		t.Fatalf("no plugin: %d", code)
	}
}

func TestDirectory(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	g := e.exec1(`INSERT INTO groups (name) VALUES ('default') RETURNING id`)
	mk := func(name string, prio int, pluginKey, typ string) int64 {
		code, out := e.do("POST", "/accounts", map[string]any{"name": name, "plugin_key": pluginKey, "type": typ,
			"group_ids": []int64{g}, "priority": prio, "credentials": map[string]any{"api_key": "sk-good-key-123"}})
		if code != 201 {
			t.Fatalf("create: %d %v", code, out)
		}
		return int64(out["data"].(map[string]any)["id"].(float64))
	}
	a1 := mk("a1", 1, "anthropic", "apikey")
	a2 := mk("a2", 2, "anthropic", "apikey")
	a3 := mk("a3", 2, "relay", "relay_key")

	ids := func(refs []core.AccountRef) string {
		var s []string
		for _, r := range refs {
			s = append(s, strconv.FormatInt(r.ID, 10))
		}
		return strings.Join(s, ",")
	}
	apikey := []core.AccountTypeKey{{PluginKey: "anthropic", Type: "apikey"}}
	refs, err := e.svc.Candidates(ctx, g, apikey)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(refs), fmt.Sprintf("%d,%d", a2, a1); got != want {
		t.Fatalf("candidates %s want %s", got, want)
	}
	// A group mixes account types of different plugins.
	mixed := append(apikey, core.AccountTypeKey{PluginKey: "relay", Type: "relay_key"})
	if refs, _ = e.svc.Candidates(ctx, g, mixed); ids(refs) != fmt.Sprintf("%d,%d,%d", a2, a3, a1) {
		t.Fatalf("mixed candidates: %s", ids(refs))
	}
	if refs[1].PluginKey != "relay" || refs[1].Type != "relay_key" {
		t.Fatalf("ref: %+v", refs[1])
	}
	if refs, _ = e.svc.Candidates(ctx, g, nil); len(refs) != 3 {
		t.Fatalf("all types: %d", len(refs))
	}
	// GroupHasType: any account of the types, whatever its status.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET status = 'disabled' WHERE id = $1`, a3); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		types []core.AccountTypeKey
		want  bool
	}{{apikey, true}, {[]core.AccountTypeKey{{PluginKey: "relay", Type: "relay_key"}}, true}, {[]core.AccountTypeKey{{PluginKey: "absent", Type: "managed"}}, false}, {nil, false}} {
		if got, err := e.svc.GroupHasType(ctx, g, c.types); err != nil || got != c.want {
			t.Fatalf("GroupHasType %v = %v %v", c.types, got, err)
		}
	}
	if got, _ := e.svc.GroupHasType(ctx, g+1000, apikey); got {
		t.Fatal("GroupHasType: another group")
	}
	// Unschedulable: what Candidates does not offer, and why. a3 is disabled,
	// a2 cools down, a1 is offered.
	// The key is written directly: SetCooldown would emit a status event.
	until := time.Now().Add(time.Minute)
	cooling := "cooldown:account:" + strconv.FormatInt(a2, 10)
	if err := e.mr.Set(cooling, "fixture"); err != nil {
		t.Fatal(err)
	}
	e.mr.SetTTL(cooling, time.Minute)
	off, err := e.svc.Unschedulable(ctx, g)
	if err != nil || len(off) != 2 {
		t.Fatalf("Unschedulable: %+v %v", off, err)
	}
	for _, u := range off {
		switch {
		case u.ID == a3 && u.Reason == "disabled" && u.Until.IsZero():
		case u.ID == a2 && u.Reason == "cooldown" && u.Until.After(time.Now()) && !u.Until.After(until.Add(time.Second)):
		default:
			t.Fatalf("Unschedulable entry %+v", u)
		}
	}
	e.mr.Del(cooling)
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET status = 'active' WHERE id = $1`, a3); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET schedulable = false WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	if off, err := e.svc.Unschedulable(ctx, g); err != nil || len(off) != 1 || off[0].ID != a1 || off[0].Reason != "unschedulable" {
		t.Fatalf("Unschedulable after re-enabling: %+v %v", off, err)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET schedulable = true WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}

	// Cooldown excludes; expiry restores.
	if err := e.svc.SetCooldown(ctx, a2, time.Now().Add(2*time.Second), "rate limited"); err != nil {
		t.Fatal(err)
	}
	if cool, _ := e.svc.IsCoolingDown(ctx, a2); !cool {
		t.Fatal("not cooling")
	}
	if refs, _ = e.svc.Candidates(ctx, g, apikey); ids(refs) != strconv.FormatInt(a1, 10) {
		t.Fatalf("cooldown not excluded: %s", ids(refs))
	}
	if v, _ := e.mr.Get(cooldownKey(a2)); v != "rate limited" {
		t.Fatalf("cooldown value %q", v)
	}
	// Extending does not emit a second event; shorter cooldown is ignored.
	_ = e.svc.SetCooldown(ctx, a2, time.Now().Add(time.Second), "short")
	if v, _ := e.mr.Get(cooldownKey(a2)); v != "rate limited" {
		t.Fatal("shorter cooldown replaced longer one")
	}
	e.mr.FastForward(3 * time.Second)
	if refs, _ = e.svc.Candidates(ctx, g, apikey); len(refs) != 2 {
		t.Fatalf("cooldown not expired: %s", ids(refs))
	}

	// Snapshot is cached: a direct DB change is invisible until invalidated.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET schedulable = false WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	if refs, _ = e.svc.Candidates(ctx, g, apikey); len(refs) != 2 {
		t.Fatal("snapshot not cached")
	}
	_ = e.bus.Publish(ctx, core.ChannelAccountChanged, []byte(`{}`))
	if refs, _ = e.svc.Candidates(ctx, g, apikey); ids(refs) != strconv.FormatInt(a2, 10) {
		t.Fatalf("not invalidated by broadcast: %s", ids(refs))
	}

	// Load decrypts.
	acc, err := e.svc.Load(ctx, a2)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(acc.Credentials, "api_key").String() != "sk-good-key-123" || acc.Status != "active" ||
		acc.PluginKey != "anthropic" || acc.Type != "apikey" {
		t.Fatalf("load: %+v", acc)
	}
	if _, err := e.svc.Load(ctx, 99999); core.AsError(err).Code != "not_found" {
		t.Fatalf("load missing: %v", err)
	}

	// AutoDisable removes it from candidates and emits one event.
	for _, reason := range []string{"401 invalid credentials", "401 invalid credentials"} {
		if ok, err := e.svc.AutoDisable(ctx, a2, reason); err != nil || !ok {
			t.Fatalf("auto disable: %v %v", ok, err)
		}
	}
	if refs, _ = e.svc.Candidates(ctx, g, apikey); len(refs) != 0 {
		t.Fatalf("disabled still candidate: %s", ids(refs))
	}
	acc, _ = e.svc.Load(ctx, a2)
	if acc.Status != "auto_disabled" {
		t.Fatalf("load after disable: %s", acc.Status)
	}
	var n int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type = 'account.status_changed'`).Scan(&n)
	if n != 2 { // one cooldown + one disable
		t.Fatalf("status events: %d", n)
	}
	var payload string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT payload::text FROM events WHERE type = 'account.status_changed' ORDER BY id LIMIT 1`).Scan(&payload)
	if gjson.Get(payload, "status").String() != "cooldown" || !gjson.Get(payload, "cooldown_until").Exists() ||
		gjson.Get(payload, "plugin_key").String() != "anthropic" || gjson.Get(payload, "type").String() != "apikey" ||
		gjson.Get(payload, "name").String() != "a2" || gjson.Get(payload, "platform").Exists() {
		t.Fatalf("cooldown payload: %s", payload)
	}

	// An account that opted out of automatic disabling is left alone
	// (CONTRACTS §42.3).
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET auto_disable = false WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.svc.AutoDisable(ctx, a1, "401 invalid credentials"); err != nil || ok {
		t.Fatalf("opted-out account disabled: %v %v", ok, err)
	}
	var a1Status string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = $1`, a1).Scan(&a1Status)
	if a1Status != "active" {
		t.Fatalf("opted-out account status %q", a1Status)
	}

	// TouchLastUsed batching.
	e.svc.TouchLastUsed(ctx, a1)
	e.svc.TouchLastUsed(ctx, a3)
	if err := e.svc.FlushLastUsed(ctx); err != nil {
		t.Fatal(err)
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE last_used_at IS NOT NULL`).Scan(&n)
	if n != 2 {
		t.Fatalf("last_used_at rows: %d", n)
	}
}

func TestCredentialHelpers(t *testing.T) {
	all := fields{"api_key": json.RawMessage(`"k"`), "base_url": json.RawMessage(`"u"`), "nested": json.RawMessage(`{"token":"t","x":1}`)}
	sec, st := split(all, []string{"base_url"})
	if len(sec) != 2 || len(st) != 1 {
		t.Fatalf("split: %v %v", sec, st)
	}
	masked := mask(mustJSON(all), []string{"api_key", "nested.token", "missing"})
	if gjson.GetBytes(masked, "api_key").String() != core.SecretMask || gjson.GetBytes(masked, "nested.token").String() != core.SecretMask ||
		gjson.GetBytes(masked, "nested.x").Int() != 1 || gjson.GetBytes(masked, "missing").Exists() {
		t.Fatalf("mask: %s", masked)
	}
	back, restored := unmask(masked, mustJSON(all), []string{"api_key", "nested.token"})
	if gjson.GetBytes(back, "api_key").String() != "k" || gjson.GetBytes(back, "nested.token").String() != "t" ||
		!slices.Equal(restored, []string{"api_key", "nested.token"}) {
		t.Fatalf("unmask: %s %v", back, restored)
	}
}

func TestTypeViewAccountIcon(t *testing.T) {
	g := testGen(&fakePlatform{})
	b := g.types[0]
	b.Plugin.Manifest.Icon = "assets/provider.svg"
	if got := typeView(g, b, nil).Icon; got != "assets/provider.svg" {
		t.Fatalf("fallback icon: %q", got)
	}
	b.Type.Icon = "assets/product.svg"
	if got := typeView(g, b, nil).Icon; got != "assets/product.svg" {
		t.Fatalf("account icon: %q", got)
	}
}

// The plugin's default models reach the console unchanged, and a type that
// declares none answers [] / {} rather than null (CONTRACTS §41).
func TestTypeViewDefaultModels(t *testing.T) {
	g := testGen(&fakePlatform{})
	b := g.types[0]
	raw, _ := json.Marshal(typeView(g, b, nil))
	if !strings.Contains(string(raw), `"default_models":[]`) || !strings.Contains(string(raw), `"default_model_mapping":{}`) {
		t.Fatalf("empty defaults: %s", raw)
	}
	b.Type.DefaultModels = []string{"m-1", "m-2"}
	b.Type.DefaultModelMapping = map[string]string{"m-1": "m-1-20260101"}
	v := typeView(g, b, nil)
	if !slices.Equal(v.DefaultModels, []string{"m-1", "m-2"}) || v.DefaultModelMapping["m-1"] != "m-1-20260101" {
		t.Fatalf("defaults = %v %v", v.DefaultModels, v.DefaultModelMapping)
	}
}

// Header passthrough rules (CONTRACTS §81) are normalized on save, read back,
// replaced by PATCH and reach the gateway through Load; a header that can
// never pass is a field error.
func TestAccountHeaderPassthroughRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	code, out := e.do("POST", "/accounts", map[string]any{"name": "passthrough", "plugin_key": "anthropic", "type": "apikey",
		"credentials":        map[string]any{"api_key": "sk-good-key-123"},
		"header_passthrough": []string{"*", " X-Claude-Code-Agent-Id ", "re:^x-stainless-", "*"}})
	require.Equal(t, 201, code, fmt.Sprint(out))
	id := int64(data(out)["id"].(float64))
	require.Equal(t, []any{"*", "x-claude-code-agent-id", "regex:^x-stainless-"}, data(out)["header_passthrough"])

	acc, err := e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{"*", "x-claude-code-agent-id", "regex:^x-stainless-"}, acc.HeaderPassthrough)

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"header_passthrough": []string{"X-Forwarded-For", "re:("}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"header_passthrough:blocked", "header_passthrough:invalid"}, fieldsOf(out))

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"header_passthrough": []string{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, []any{}, data(out)["header_passthrough"])

	code, out = e.do("POST", "/accounts", map[string]any{"name": "plain", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-456"}})
	require.Equal(t, 201, code, fmt.Sprint(out))
	require.Equal(t, []any{}, data(out)["header_passthrough"])
}

// A request parameter override (CONTRACTS §84) is validated on save, stored
// as given (extra members included), read back in the list and detail,
// replaced or cleared by PATCH and compiled into the account by Load.
func TestAccountParamOverride(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	override := map[string]any{"operations": []any{
		map[string]any{"mode": "set", "path": "temperature", "value": 0.2, "description": "cooler"},
		map[string]any{"mode": "set_header", "path": "anthropic-beta", "value": map[string]any{"$append": []any{"context-1m-2025-08-07"}}},
	}}
	code, out := e.do("POST", "/accounts", map[string]any{"name": "override", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}, "param_override": override})
	require.Equal(t, 201, code, fmt.Sprint(out))
	id := int64(data(out)["id"].(float64))
	require.Equal(t, override, data(out)["param_override"])

	acc, err := e.svc.Load(ctx, id)
	require.NoError(t, err)
	res, err := acc.ParamOverride.Apply([]byte(`{"model":"m"}`), nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"m","temperature":0.2}`, string(res.Body))
	require.Equal(t, "context-1m-2025-08-07", res.Headers.Set["anthropic-beta"])

	_, out = e.do("GET", "/accounts", nil)
	require.Equal(t, override, out["data"].([]any)[0].(map[string]any)["param_override"])

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"param_override": map[string]any{"operations": []any{
		map[string]any{"mode": "set"}, map[string]any{"mode": "set_header", "path": "x-api-key", "value": "k"}, map[string]any{"mode": "bogus", "path": "a"}}}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"param_override:required", "param_override:required", "param_override:blocked", "param_override:unsupported"}, fieldsOf(out))

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"name": "renamed"})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, override, data(out)["param_override"], "absent keeps the override")

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"param_override": map[string]any{"temperature": 0.5}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, map[string]any{"temperature": 0.5}, data(out)["param_override"])

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"param_override": map[string]any{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, data(out)["param_override"])
	acc, err = e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Nil(t, acc.ParamOverride)

	code, out = e.do("POST", "/accounts", map[string]any{"name": "plain", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-456"}, "param_override": nil})
	require.Equal(t, 201, code, fmt.Sprint(out))
	require.Nil(t, data(out)["param_override"])
}

// A request header override (CONTRACTS §108) is validated on save, stored as
// given, read back in the list and detail, replaced or cleared by PATCH and
// compiled into the account by Load.
func TestAccountHeaderOverride(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	override := map[string]any{"*": "", "User-Agent": "my-client/1.0", "x-trace": "{client_header:x-request-id}", "x-drop": "{unset}"}
	code, out := e.do("POST", "/accounts", map[string]any{"name": "headers", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}, "header_override": override})
	require.Equal(t, 201, code, fmt.Sprint(out))
	id := int64(data(out)["id"].(float64))
	require.Equal(t, override, data(out)["header_override"])

	acc, err := e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{"*"}, acc.HeaderOverride.Passthrough())
	res, err := acc.HeaderOverride.Resolve(http.Header{"X-Request-Id": {"r1"}})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"user-agent": "my-client/1.0", "x-trace": "r1"}, res.Set)
	require.Equal(t, []string{"x-drop"}, res.Delete)

	_, out = e.do("GET", "/accounts", nil)
	require.Equal(t, override, out["data"].([]any)[0].(map[string]any)["header_override"])

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"header_override": map[string]any{
		"Authorization": "Bearer x", "x-key": "{api_key}", "x-n": 1}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.ElementsMatch(t, []string{"header_override:blocked", "header_override:unsupported", "header_override:invalid"}, fieldsOf(out))

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"name": "renamed"})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, override, data(out)["header_override"], "absent keeps the override")

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"header_override": map[string]any{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, data(out)["header_override"])
	acc, err = e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Nil(t, acc.HeaderOverride)

	code, out = e.do("POST", "/accounts", map[string]any{"name": "plain", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-456"}})
	require.Equal(t, 201, code, fmt.Sprint(out))
	require.Nil(t, data(out)["header_override"])
}

// The preview runs an override on a sample request without saving it.
func TestAccountParamOverridePreview(t *testing.T) {
	e := setup(t)
	code, out := e.do("POST", "/accounts/param-override/preview", map[string]any{
		"param_override": map[string]any{"operations": []any{
			map[string]any{"mode": "set", "path": "model", "value": "up-2", "conditions": []any{map[string]any{"path": "model", "mode": "full", "value": "client-1"}}},
			map[string]any{"mode": "copy_header", "from": "x-trace", "to": "x-trace-up"},
			map[string]any{"mode": "delete_header", "path": "x-drop"},
		}},
		"body": map[string]any{"messages": []any{}}, "model": "client-1", "headers": map[string]string{"X-Trace": "t1"}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, map[string]any{"body": map[string]any{"messages": []any{}, "model": "up-2"}, "set_headers": map[string]any{"x-trace-up": "t1"},
		"delete_headers": []any{"x-drop"}, "return_error": nil, "error": nil}, data(out))

	code, out = e.do("POST", "/accounts/param-override/preview", map[string]any{
		"param_override": map[string]any{"operations": []any{map[string]any{"mode": "return_error", "value": map[string]any{"message": "no", "status": 429}}}},
		"body":           map[string]any{"model": "m"}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, map[string]any{"status": 429.0, "code": "invalid_request", "type": "invalid_request_error", "message": "no", "skip_retry": true}, data(out)["return_error"])

	code, out = e.do("POST", "/accounts/param-override/preview", map[string]any{
		"param_override": map[string]any{"operations": []any{map[string]any{"mode": "to_upper", "path": "n"}}}, "body": map[string]any{"n": 1}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Contains(t, data(out)["error"], "value is a number, not a string")
	require.Nil(t, data(out)["body"])

	// A header override seeds the run; without a param override it is the result.
	code, out = e.do("POST", "/accounts/param-override/preview", map[string]any{
		"header_override": map[string]any{"x-a": "{client_header:x-trace:extra}", "x-gone": "{unset}"},
		"param_override": map[string]any{"operations": []any{
			map[string]any{"mode": "set", "path": "seen", "value": true, "conditions": map[string]any{"header_override.x-a": "t1,extra"}}}},
		"headers": map[string]string{"X-Trace": "t1"}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, map[string]any{"body": map[string]any{"seen": true}, "set_headers": map[string]any{"x-a": "t1,extra"},
		"delete_headers": []any{"x-gone"}, "return_error": nil, "error": nil}, data(out))
	code, out = e.do("POST", "/accounts/param-override/preview", map[string]any{"header_override": map[string]any{"x-b": "v"}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, map[string]any{"x-b": "v"}, data(out)["set_headers"])

	code, out = e.do("POST", "/accounts/param-override/preview", map[string]any{
		"param_override": map[string]any{"operations": []any{map[string]any{"mode": "bogus"}}}, "body": []any{},
		"header_override": map[string]any{"cookie": "x"}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"param_override:unsupported", "header_override:blocked", "body:invalid"}, fieldsOf(out))
}

// conversion_loss_policy (CONTRACTS §88) is saved, cleared with null and
// reaches the scheduler through the group snapshot and Load; an unknown
// value is a field error.
func TestAccountConversionLossPolicy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	g := e.exec1(`INSERT INTO groups (name) VALUES ('loss') RETURNING id`)
	code, out := e.do("POST", "/accounts", map[string]any{"name": "loss", "plugin_key": "anthropic", "type": "apikey",
		"group_ids": []int64{g}, "credentials": map[string]any{"api_key": "sk-good-key-123"}, "conversion_loss_policy": "safe"})
	require.Equal(t, 201, code, fmt.Sprint(out))
	require.Equal(t, "safe", data(out)["conversion_loss_policy"])
	id := int64(data(out)["id"].(float64))
	cands, err := e.svc.Candidates(ctx, g, nil)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, "safe", cands[0].ConversionLossPolicy)

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"conversion_loss_policy": "lax"})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"conversion_loss_policy:invalid"}, fieldsOf(out))

	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"priority": 2})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, "safe", data(out)["conversion_loss_policy"], "absent keeps it")
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"conversion_loss_policy": nil})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, data(out)["conversion_loss_policy"])
	acc, err := e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "", acc.ConversionLossPolicy)
}

func (*fakePlatform) Execute(context.Context, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error) {
	return core.ExecutionResult{}, errors.New("the fake platform does not execute requests")
}

func (*fakePlatform) Monitor(context.Context, *pluginv1.PollRequest, core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
	return nil, errors.New("the fake platform does not monitor tasks")
}

func (g *fakeGen) SettingsValidator(string) (core.SettingsValidator, bool) { return nil, false }
