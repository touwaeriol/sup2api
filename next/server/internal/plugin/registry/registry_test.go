package registry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/platforms"
	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

type ext struct {
	pkg        *registry.Package
	grants     registry.Grants
	noPlatform bool // plugin without platform.adapter.v1
	sched      bool // plugin whose runtime exposes SchedulerService
}

type stub struct{}

func (stub) ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return &pluginv1.ValidateCredentialsResponse{}, nil
}
func (stub) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return nil, nil
}
func (stub) BuildModelsRequest(context.Context, *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	return nil, nil
}
func (stub) BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return nil, nil
}
func (stub) OnGatewayRequest(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
	return nil, nil
}
func (stub) RunJob(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
	return nil, nil
}
func (stub) OnEvents(context.Context, *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error) {
	return nil, nil
}
func (stub) HandleHTTP(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	return nil, nil
}
func (stub) RankAccounts(context.Context, *pluginv1.RankAccountsRequest) (*pluginv1.RankAccountsResponse, error) {
	return nil, nil
}
func (stub) ResolveModel(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
	return nil, nil
}
func (stub) BuildQuotaRequest(context.Context, *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error) {
	return nil, nil
}

func (stub) ParseQuotaResponse(context.Context, *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error) {
	return nil, nil
}

func (stub) BuildBalanceRequest(context.Context, *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	return nil, nil
}

func (stub) ParseBalanceResponse(context.Context, *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	return nil, nil
}

func (e ext) Package() *registry.Package { return e.pkg }
func (e ext) Grants() registry.Grants    { return e.grants }
func (e ext) Platform() core.PlatformPlugin {
	if e.noPlatform {
		return nil
	}
	return stub{}
}
func (e ext) Hook() core.HookPlugin { return stub{} }
func (e ext) App() core.AppPlugin   { return stub{} }
func (e ext) HTTP() core.HTTPPlugin { return stub{} }
func (e ext) Scheduler() core.SchedulerPlugin {
	if !e.sched {
		return nil
	}
	return stub{}
}

func load(t *testing.T, m *manifest.Manifest) *registry.Package {
	t.Helper()
	p, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "sub2api", "official")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// Hook needs that are gjson queries survive the package and grant JSON
// unchanged and are granted when the approved scope lists them verbatim
// (CONTRACTS §20.1).
func TestGrantedFieldsGjsonQueries(t *testing.T) {
	q1, q2 := `messages|@reverse|#(role=="user")`, `[input]|#(%"*")`
	a := registrytest.Manifest("alpha", "1.0.0")
	a.Hooks[0].Needs = []string{"model", q1, q2}
	pa := load(t, a)
	scope, err := json.Marshal(map[string]any{"points": []string{"gateway.request"}, "fields": []string{"model", q1}})
	if err != nil {
		t.Fatal(err)
	}
	g := registry.New().Publish([]registry.Extension{ext{pkg: pa, grants: registry.Grants{"gateway.hook": scope}}})
	hooks := g.Hooks("gateway.request")
	if len(hooks) != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
	if f := hooks[0].GrantedFields; len(f) != 2 || f[0] != "model" || f[1] != q1 {
		t.Fatalf("granted fields = %q", f)
	}
}

// AccountRankers collects only plugins that both declare manifest
// scheduler.rank and the scheduler.rank.v1 capability, and orders them by the
// declared order with ties broken by plugin key.
func TestAccountRankersCollectionAndOrder(t *testing.T) {
	withRank := func(key string, order int, caps ...manifest.Capability) *registry.Package {
		m := registrytest.Manifest(key, "1.0.0")
		m.Capabilities = append(m.Capabilities, caps...)
		m.Scheduler = &manifest.Scheduler{Rank: &manifest.SchedulerRank{
			Order:     order,
			Match:     manifest.HookMatch{Protocols: []string{"p_" + key + ".test"}},
			TimeoutMs: 120,
		}}
		return load(t, m)
	}
	rank := manifest.Capability{ID: manifest.CapSchedulerRank}

	// delta and bravo share order 10 (bravo wins on key), alpha runs last.
	pBravo := withRank("bravo", 10, rank)
	pDelta := withRank("delta", 10, rank)
	pAlpha := withRank("alpha", 30, rank)
	// echo declares scheduler.rank without the capability; fox declares the
	// capability without manifest scheduler.rank; gamma has both but its
	// runtime exposes no SchedulerService at all.
	pEcho := withRank("echo", 1)
	mFox := registrytest.Manifest("fox", "1.0.0")
	mFox.Capabilities = append(mFox.Capabilities, rank)
	pFox := load(t, mFox)
	pGamma := withRank("gamma", 0, rank)

	g := registry.New().Publish([]registry.Extension{
		ext{pkg: pAlpha, grants: registry.Grants{}, sched: true},
		ext{pkg: pBravo, grants: registry.Grants{}, sched: true},
		ext{pkg: pDelta, grants: registry.Grants{}, sched: true},
		ext{pkg: pEcho, grants: registry.Grants{}, sched: true},
		ext{pkg: pFox, grants: registry.Grants{}, sched: true},
		ext{pkg: pGamma, grants: registry.Grants{}},
	})
	rankers := g.AccountRankers()
	var keys []string
	for _, r := range rankers {
		keys = append(keys, r.Plugin.Key)
	}
	if want := []string{"bravo", "delta", "alpha"}; strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("rankers = %v, want %v", keys, want)
	}
	if r := rankers[0]; r.Rank.Order != 10 || r.Rank.TimeoutMs != 120 || r.Client == nil {
		t.Fatalf("binding = %+v", r)
	}
	if ps := rankers[0].Rank.Match.Protocols; len(ps) != 1 || ps[0] != "p_bravo.test" {
		t.Fatalf("match protocols = %v", ps)
	}
	// The plugins are still reachable as schedulers for affinity keys.
	if _, ok := g.Scheduler("echo"); !ok {
		t.Fatal("echo must still be a scheduler")
	}
	if _, ok := g.Scheduler("gamma"); ok {
		t.Fatal("gamma exposes no SchedulerService")
	}
}

func TestGenerationBuildAndSwitch(t *testing.T) {
	a := registrytest.Manifest("alpha", "1.0.0")
	a.Hooks = append(a.Hooks, manifest.Hook{ID: "late", Point: "gateway.request", Order: 50, Needs: []string{"prompt_text"}})
	a.Jobs = []manifest.Job{{ID: "rollup", Schedule: "@every 5m"}}
	a.Events = &manifest.Events{Subscribe: []string{"usage.recorded", "account.created"}}
	b := registrytest.Manifest("beta", "2.0.0")
	b.Hooks[0].Order = 20

	reg := registry.New()
	var seen []uint64
	cancel := reg.OnChange(func(g core.Generation) { seen = append(seen, g.Number()) })
	defer cancel()
	if len(reg.Current().Plugins()) != 0 {
		t.Fatal("initial generation must be empty")
	}

	pa, pb := load(t, a), load(t, b)
	grantsA := registry.Grants{
		"gateway.hook":  []byte(`{"points":["gateway.request"],"fields":["model"]}`),
		"routes.admin":  []byte(`{}`),
		"routes.public": []byte(`{}`),
		"jobs":          []byte(`{}`),
		"events":        []byte(`{"subscribe":["usage.recorded"]}`),
	}
	g := reg.Publish([]registry.Extension{ext{pkg: pb, grants: registry.Grants{}}, ext{pkg: pa, grants: grantsA}})
	if g.Number() != 1 || len(seen) != 1 || reg.Current() != g {
		t.Fatalf("generation %d, listeners %v", g.Number(), seen)
	}
	if ps := g.Plugins(); len(ps) != 2 || ps[0].Key != "alpha" || ps[1].Key != "beta" {
		t.Fatalf("plugins = %+v", ps)
	}
	info, _ := g.Plugin("alpha")
	if info.AssetBase != "/plugin-ui/alpha/1.0.0-"+pa.Hash8() || info.Trust != "official" {
		t.Fatalf("info = %+v", info)
	}

	// Hooks: only alpha has the gateway.hook grant; needs are narrowed to the
	// granted fields; order is respected.
	hooks := g.Hooks("gateway.request")
	if len(hooks) != 2 || hooks[0].Hook.ID != "0" || hooks[1].Hook.ID != "late" {
		t.Fatalf("hooks = %+v", hooks)
	}
	if f := hooks[0].GrantedFields; len(f) != 1 || f[0] != "model" {
		t.Fatalf("granted fields = %v", f)
	}
	if len(hooks[1].GrantedFields) != 0 {
		t.Fatalf("prompt_text must not be granted: %v", hooks[1].GrantedFields)
	}

	// Routes are filtered by routes.<scope> grants.
	if rs := g.Routes("alpha"); len(rs) != 3 {
		t.Fatalf("alpha routes = %d", len(rs))
	}
	if rs := g.Routes("beta"); len(rs) != 0 {
		t.Fatalf("beta routes without grants = %d", len(rs))
	}
	if len(g.Jobs()) != 1 {
		t.Fatalf("jobs = %d", len(g.Jobs()))
	}
	if subs := g.Subscriptions(); len(subs) != 1 || len(subs[0].Events.Subscribe) != 1 || subs[0].Events.Subscribe[0] != "usage.recorded" {
		t.Fatalf("subscriptions = %+v", subs)
	}

	// Platforms and account types.
	for _, key := range []string{"alpha", "beta"} {
		pb, ok := g.PlatformForProtocol("p_" + key + ".test")
		if !ok || pb.Builtin || pb.Plugin.Key != key || pb.Platform.ID != "p_"+key {
			t.Fatalf("platform for protocol of %s = %+v %v", key, pb, ok)
		}
	}
	at, ok := g.AccountType("alpha", "apikey")
	if !ok || at.Client == nil || at.Key() != (core.AccountTypeKey{PluginKey: "alpha", Type: "apikey"}) {
		t.Fatalf("account type = %+v", at)
	}
	if _, ok := g.AccountType("p_alpha", "apikey"); ok {
		t.Fatal("account types are keyed by plugin, not platform")
	}

	// Assets.
	data, ct, err := g.ReadAsset("alpha", "ui/index.html")
	if err != nil || ct != "text/html; charset=utf-8" || len(data) == 0 {
		t.Fatalf("asset: %v %s", err, ct)
	}
	if _, _, err := g.ReadAsset("alpha", "../../etc/passwd"); err == nil {
		t.Fatal("path escape must fail")
	}
	if _, _, err := g.ReadAsset("gamma", "ui/index.html"); err == nil {
		t.Fatal("unknown plugin must fail")
	}

	// Switching keeps the old generation intact for pinned readers.
	g2 := reg.Publish([]registry.Extension{ext{pkg: pb, grants: registry.Grants{}}})
	if g2.Number() != 2 || len(seen) != 2 {
		t.Fatal("second publish")
	}
	if _, ok := g.Plugin("alpha"); !ok {
		t.Fatal("old generation must stay immutable")
	}
	if _, ok := reg.Current().Plugin("alpha"); ok {
		t.Fatal("alpha must be gone from the current generation")
	}
}

// Account types are declared at the top level by any plugin and served by
// the declaring plugin; the platform index lists the types declaring it.
func TestAccountTypesManyToMany(t *testing.T) {
	platform := registrytest.Manifest("anth", "1.0.0")
	platform.AccountTypes = append(platform.AccountTypes, manifest.AccountType{
		ID: "oauth", Label: manifest.LocalizedText{"en": "OAuth"}, Form: manifest.Form{Mode: "native", Component: "X"},
		Platforms: []manifest.AccountPlatform{{Platform: "p_anth"}, {Platform: manifest.PlatformAnthropic}},
	})
	// A relay plugin declaring only account types (no platform, no endpoints).
	relay := registrytest.Manifest("relay", "1.0.0")
	relay.Platforms = nil
	relay.AccountTypes = []manifest.AccountType{
		{ID: "zkey", Label: manifest.LocalizedText{"en": "Z"}, Form: manifest.Form{Mode: "native", Component: "Z"},
			Platforms: []manifest.AccountPlatform{{Platform: "p_anth"}, {Platform: "p_anth"}}},
		{ID: "akey", Label: manifest.LocalizedText{"en": "A"}, Form: manifest.Form{Mode: "native", Component: "A"},
			Platforms: []manifest.AccountPlatform{{Platform: manifest.PlatformAnthropic}}},
	}
	// Without platform.adapter.v1 the account types are not registered.
	noAdapter := registrytest.Manifest("noadapter", "1.0.0")

	reg := registry.New()
	// No grant is needed: the plugin receives the accounts of the types it
	// declares (CONTRACTS §124).
	noGrant := registrytest.Manifest("nogrant", "1.0.0")
	g := reg.Publish([]registry.Extension{
		ext{pkg: load(t, relay)}, ext{pkg: load(t, noAdapter), noPlatform: true},
		ext{pkg: load(t, platform)}, ext{pkg: load(t, noGrant)},
	})

	var keys []string
	for _, b := range g.AccountTypes() {
		keys = append(keys, b.Plugin.Key+"/"+b.Type.ID)
		if b.Client == nil {
			t.Fatalf("%v: client is nil", b.Key())
		}
	}
	if want := "anth/apikey anth/oauth nogrant/apikey relay/zkey relay/akey"; strings.Join(keys, " ") != want {
		t.Fatalf("account types = %v, want %s", keys, want)
	}
	if _, ok := g.AccountType("noadapter", "apikey"); ok {
		t.Fatal("account types of a plugin without platform.adapter.v1 must not be registered")
	}
	if _, ok := g.AccountType("nogrant", "apikey"); !ok {
		t.Fatal("account types of a plugin holding no grant must be registered")
	}
	if b, ok := g.AccountType("relay", "zkey"); !ok || b.Plugin.Key != "relay" {
		t.Fatalf("relay/zkey = %+v %v", b, ok)
	}
	if _, ok := g.Platform("p_relay"); ok {
		t.Fatal("relay declares no platform")
	}
	// Platforms of plugins whose account types are not registered still
	// exist (a platform needs no adapter).
	if _, ok := g.Platform("p_noadapter"); !ok {
		t.Fatal("p_noadapter must exist")
	}

	typesFor := func(platformID string) string {
		var out []string
		for _, b := range g.AccountTypesForPlatform(platformID) {
			out = append(out, b.Plugin.Key+"/"+b.Type.ID)
		}
		return strings.Join(out, " ")
	}
	if got, want := typesFor("p_anth"), "anth/apikey anth/oauth relay/zkey"; got != want {
		t.Fatalf("p_anth types = %v, want %s", got, want)
	}
	// Account types supporting the built-in anthropic platform.
	if got, want := typesFor(manifest.PlatformAnthropic), "anth/oauth relay/akey"; got != want {
		t.Fatalf("anthropic types = %v, want %s", got, want)
	}
	oauth := g.AccountTypesForPlatform(manifest.PlatformAnthropic)[0]
	if p, ok := oauth.Supports(manifest.PlatformAnthropic); !ok || p.Platform != manifest.PlatformAnthropic {
		t.Fatal("binding platform lookup")
	}
	if _, ok := oauth.Supports("p_relay"); ok {
		t.Fatal("oauth does not support p_relay")
	}
	if got := typesFor("nope"); got != "" {
		t.Fatalf("unknown platform = %v", got)
	}
}

// Built-in platforms exist in every generation, even the initial one; plugin
// platforms and their endpoints exist only while the plugin is enabled.
func TestBuiltinAndPluginPlatforms(t *testing.T) {
	builtin := platforms.Builtin()
	if len(builtin) == 0 {
		t.Fatal("no built-in platforms")
	}
	var builtinEndpoints int
	for _, p := range builtin {
		builtinEndpoints += len(p.Endpoints)
	}
	check := func(g core.Generation, plugins ...string) {
		t.Helper()
		ps := g.Platforms()
		if len(ps) != len(builtin)+len(plugins) {
			t.Fatalf("platforms = %d, want %d", len(ps), len(builtin)+len(plugins))
		}
		for i, bp := range builtin {
			b, ok := g.Platform(bp.ID)
			if !ok || !b.Builtin || b.Plugin.Key != "" || ps[i].Platform.ID != bp.ID {
				t.Fatalf("built-in %s = %+v %v", bp.ID, b, ok)
			}
			for _, proto := range bp.Protocols() {
				if pb, ok := g.PlatformForProtocol(proto); !ok || pb.Platform.ID != bp.ID {
					t.Fatalf("protocol %s -> %+v %v", proto, pb, ok)
				}
			}
		}
		eps := g.Endpoints()
		if len(eps) != builtinEndpoints+len(plugins) {
			t.Fatalf("endpoints = %d, want %d", len(eps), builtinEndpoints+len(plugins))
		}
		for _, eb := range eps[:builtinEndpoints] {
			if eb.Plugin.Key != "" || !platforms.IsBuiltin(eb.Platform) {
				t.Fatalf("built-in endpoint = %+v", eb)
			}
		}
		for i, key := range plugins {
			eb := eps[builtinEndpoints+i]
			if eb.Plugin.Key != key || eb.Platform != "p_"+key || eb.Endpoint.Path != "/p_"+key+"/v1/test" {
				t.Fatalf("plugin endpoint = %+v", eb)
			}
			pb, ok := g.Platform("p_" + key)
			if !ok || pb.Builtin || pb.Plugin.Key != key {
				t.Fatalf("platform p_%s = %+v %v", key, pb, ok)
			}
		}
	}

	reg := registry.New()
	check(reg.Current())
	if _, ok := reg.Current().Platform(manifest.PlatformAnthropic); !ok {
		t.Fatal("anthropic must be built in")
	}

	a := ext{pkg: load(t, registrytest.Manifest("alpha", "1.0.0")), grants: registry.Grants{}}
	b := ext{pkg: load(t, registrytest.Manifest("beta", "1.0.0")), grants: registry.Grants{}}
	check(reg.Publish([]registry.Extension{b, a}), "alpha", "beta")
	// Disabling beta removes its platform and endpoint.
	g := reg.Publish([]registry.Extension{a})
	check(g, "alpha")
	if _, ok := g.Platform("p_beta"); ok {
		t.Fatal("p_beta must be gone")
	}
	if _, ok := g.PlatformForProtocol("p_beta.test"); ok {
		t.Fatal("p_beta.test must be gone")
	}
	check(reg.Publish(nil))
}

// Conflicting plugin platforms are skipped as a whole (install-time
// validation normally rejects them first).
func TestPlatformConflictsSkipped(t *testing.T) {
	// Same id as a built-in platform.
	builtinID := registrytest.Manifest("aaa", "1.0.0")
	builtinID.Platforms[0].ID = manifest.PlatformAnthropic
	// Endpoint overlapping a built-in endpoint.
	overlap := registrytest.Manifest("bbb", "1.0.0")
	ep := platforms.Builtin()[0].Endpoints[0]
	overlap.Platforms[0].Endpoints[0].Method = ep.Method
	overlap.Platforms[0].Endpoints[0].Path = "/" + strings.Split(strings.TrimPrefix(ep.Path, "/"), "/")[0] + "/*rest"
	// Same id as the platform of an earlier plugin (by key): ccc wins.
	first := registrytest.Manifest("ccc", "1.0.0")
	dupID := registrytest.Manifest("ddd", "1.0.0")
	dupID.Platforms[0].ID = "p_ccc"
	dupID.Platforms[0].Endpoints[0].Path = "/p_ddd/other"
	// Endpoint overlapping an earlier plugin's endpoint (parameter segment).
	dupPath := registrytest.Manifest("eee", "1.0.0")
	dupPath.Platforms[0].Endpoints[0].Path = "/p_ccc/:ver/test"
	// Two endpoints of one platform overlapping each other.
	selfOverlap := registrytest.Manifest("fff", "1.0.0")
	e2 := registrytest.TestEndpoint("fff")
	e2.ID, e2.Path, e2.Protocol = "t2", "/p_fff/v1/:x", "p_fff.t2"
	selfOverlap.Platforms[0].Endpoints = append(selfOverlap.Platforms[0].Endpoints, e2)
	// A second platform of a plugin can still register when the first one
	// is skipped.
	partial := registrytest.Manifest("ggg", "1.0.0")
	partial.Platforms = append([]manifest.Platform{{ID: "p_ccc", Endpoints: []manifest.Endpoint{e2}}}, partial.Platforms...)

	var exts []registry.Extension
	for _, m := range []*manifest.Manifest{builtinID, overlap, first, dupID, dupPath, selfOverlap, partial} {
		exts = append(exts, ext{pkg: load(t, m), grants: registry.Grants{}})
	}
	g := registry.New().Publish(exts)

	anth, ok := g.Platform(manifest.PlatformAnthropic)
	if !ok || !anth.Builtin {
		t.Fatalf("anthropic = %+v", anth)
	}
	for _, id := range []string{"p_bbb", "p_ddd", "p_eee", "p_fff"} {
		if _, ok := g.Platform(id); ok {
			t.Fatalf("%s must be skipped", id)
		}
	}
	if pb, ok := g.Platform("p_ccc"); !ok || pb.Plugin.Key != "ccc" {
		t.Fatalf("p_ccc = %+v %v", pb, ok)
	}
	if pb, ok := g.Platform("p_ggg"); !ok || pb.Plugin.Key != "ggg" {
		t.Fatalf("p_ggg = %+v %v", pb, ok)
	}
	var plugins []string
	for _, eb := range g.Endpoints() {
		if eb.Plugin.Key != "" {
			plugins = append(plugins, eb.Plugin.Key+":"+eb.Platform)
		}
	}
	if got, want := strings.Join(plugins, " "), "ccc:p_ccc ggg:p_ggg"; got != want {
		t.Fatalf("plugin endpoints = %s, want %s", got, want)
	}
	// Skipped platforms do not affect the rest of the plugin, which is
	// reported abnormal with the platform it does not hold.
	if len(g.Plugins()) != 7 {
		t.Fatalf("plugins = %d", len(g.Plugins()))
	}
	for key, platform := range map[string]string{"aaa": manifest.PlatformAnthropic, "bbb": "p_bbb", "ddd": "p_ccc", "eee": "p_eee", "fff": "p_fff", "ggg": "p_ccc"} {
		info, _ := g.Plugin(key)
		if len(info.Conflicts) != 1 || info.Conflicts[0].Resource != "platform" || info.Conflicts[0].ID != platform || info.Conflicts[0].Reason == "" {
			t.Fatalf("%s conflicts = %+v", key, info.Conflicts)
		}
	}
	if info, _ := g.Plugin("ccc"); len(info.Conflicts) != 0 {
		t.Fatalf("ccc conflicts = %+v", info.Conflicts)
	}
}

// The first claimant of a platform keeps it, whatever the keys (audit
// 2026-10-09 P2-3), and a path a newer core reserves is not given to a
// plugin (P2-2): the plugin is abnormal instead of taking a console page.
func TestFirstClaimantWinsAndCoreReservations(t *testing.T) {
	early := registrytest.Manifest("zzz", "1.0.0")
	early.Platforms[0].ID = "shared"
	early.Platforms[0].Endpoints[0].Protocol = "shared.test"
	late := registrytest.Manifest("aaa", "1.0.0")
	late.Platforms[0].ID = "shared"
	late.Platforms[0].Endpoints[0].Protocol = "shared.test"
	late.Platforms[0].Endpoints[0].Path = "/shared/late"
	console := registrytest.Manifest("ccc", "1.0.0")
	console.Platforms[0].Endpoints[0].Method, console.Platforms[0].Endpoints[0].Path = "GET", "/dashboard/x"
	pe, pl, pc := load(t, early), load(t, late), load(t, console)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	pe.ClaimedAt, pl.ClaimedAt, pc.ClaimedAt = base, base.Add(time.Hour), base
	g := registry.New().Publish([]registry.Extension{ext{pkg: pl}, ext{pkg: pe}, ext{pkg: pc}})
	if pb, ok := g.Platform("shared"); !ok || pb.Plugin.Key != "zzz" {
		t.Fatalf("shared = %+v %v", pb.Plugin.Key, ok)
	}
	if info, _ := g.Plugin("aaa"); len(info.Conflicts) != 1 || info.Conflicts[0].ID != "shared" {
		t.Fatalf("aaa conflicts = %+v", info.Conflicts)
	}
	if _, ok := g.Platform("p_ccc"); ok {
		t.Fatal("a console path was given to a plugin")
	}
	if info, _ := g.Plugin("ccc"); len(info.Conflicts) != 1 || !strings.Contains(info.Conflicts[0].Reason, "console") {
		t.Fatalf("ccc conflicts = %+v", info.Conflicts)
	}
	// Unknown claim times come after known ones.
	pe.ClaimedAt = time.Time{}
	g = registry.New().Publish([]registry.Extension{ext{pkg: pe}, ext{pkg: pl}})
	if pb, _ := g.Platform("shared"); pb.Plugin.Key != "aaa" {
		t.Fatalf("unknown claim time won: %s", pb.Plugin.Key)
	}
}

// PlatformBinding.Client is the declaring plugin's PlatformService, so the
// core can call the plugin that owns an endpoint's platform. Built-in
// platforms and plugins without platform.adapter.v1 leave it nil.
func TestPlatformBindingClient(t *testing.T) {
	pAlpha := load(t, registrytest.Manifest("alpha", "1.0.0"))
	noAdapter := registrytest.Manifest("bravo", "1.0.0")
	noAdapter.Capabilities = nil
	pBravo := load(t, noAdapter)
	g := registry.New().Publish([]registry.Extension{
		ext{pkg: pAlpha, grants: registry.Grants{}},
		ext{pkg: pBravo, grants: registry.Grants{}, noPlatform: true},
	})

	pb, ok := g.Platform("p_alpha")
	if !ok || pb.Builtin || pb.Client == nil {
		t.Fatalf("p_alpha = %+v %v", pb, ok)
	}
	// PlatformForProtocol returns the same binding, Client included.
	if fp, ok := g.PlatformForProtocol("p_alpha.test"); !ok || fp.Client != pb.Client {
		t.Fatalf("p_alpha.test = %+v %v", fp, ok)
	}
	// Platforms() carries it too.
	for _, b := range g.Platforms() {
		if b.Platform.ID == "p_alpha" && b.Client == nil {
			t.Fatal("Platforms() dropped the client")
		}
	}
	// A plugin platform whose plugin has no platform.adapter.v1: nil client.
	if pb, ok := g.Platform("p_bravo"); !ok || pb.Client != nil {
		t.Fatalf("p_bravo = %+v %v", pb, ok)
	}
	// Built-in platforms are always nil; every caller must check.
	for _, bp := range platforms.Builtin() {
		b, ok := g.Platform(bp.ID)
		if !ok || !b.Builtin || b.Client != nil {
			t.Fatalf("built-in %s = %+v %v", bp.ID, b, ok)
		}
	}
}

// An account type may name a platform another plugin declares, which need not
// be installed yet, so this stays a warning - but it must be a warning, not
// silence: a misspelt platform id otherwise costs nothing at install time and
// every request for it ends in "no enabled account type serves this endpoint".
func TestUnknownPlatformOfAccountTypeWarns(t *testing.T) {
	m := registrytest.Manifest("anth", "1.0.0")
	m.AccountTypes[0].Platforms = []manifest.AccountPlatform{
		{Platform: "p_anth"}, {Platform: manifest.PlatformAnthropic}, {Platform: "volcegnine"},
	}
	log := captureWarnings(t)
	reg := registry.New()
	g := reg.Publish([]registry.Extension{
		ext{pkg: load(t, m)},
	})
	// The binding is kept either way: the platform may arrive later.
	if len(g.AccountTypesForPlatform("volcegnine")) != 1 {
		t.Fatal("the account type must stay bound to the unknown platform")
	}
	out := log.String()
	if !strings.Contains(out, "volcegnine") || !strings.Contains(out, "account type serves a platform") {
		t.Fatalf("no warning for the unknown platform: %s", out)
	}
	// The platforms that do exist (the plugin's own and a built-in) are quiet.
	if strings.Contains(out, "p_anth") || strings.Contains(out, "\"anthropic\"") {
		t.Fatalf("known platforms must not warn: %s", out)
	}
}

// captureWarnings redirects slog for the duration of one test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func (stub) BuildRefreshRequest(context.Context, *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	return nil, nil
}

func (stub) ParseRefreshResponse(context.Context, *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	return nil, nil
}

// Another plugin's account type does not serve a platform whose usage or
// task facts the declaring plugin states (usageSource "plugin", a task
// submission): only that plugin's Execute can read them (forwarding v2,
// design 4.3). Its own types and the other platforms it serves stay.
func TestForeignFactsAccountTypeSkipped(t *testing.T) {
	video := registrytest.Manifest("video", "1.0.0")
	video.Platforms[0].Endpoints[0].UsageSource = manifest.UsageSourcePlugin
	relay := registrytest.Manifest("relay", "1.0.0")
	relay.AccountTypes[0].Platforms = append(relay.AccountTypes[0].Platforms, manifest.AccountPlatform{Platform: "p_video"})
	g := registry.New().Publish([]registry.Extension{
		ext{pkg: load(t, video)}, ext{pkg: load(t, relay)},
	})
	typesFor := func(platformID string) (out []string) {
		for _, b := range g.AccountTypesForPlatform(platformID) {
			out = append(out, b.Plugin.Key+"/"+b.Type.ID)
		}
		return out
	}
	if got := typesFor("p_video"); !slices.Equal(got, []string{"video/apikey"}) {
		t.Fatalf("p_video types = %v", got)
	}
	if got := typesFor("p_relay"); !slices.Equal(got, []string{"relay/apikey"}) {
		t.Fatalf("p_relay types = %v", got)
	}
	// The binding itself no longer lists the platform: whoever asks the type
	// what it serves (routing, the cross-platform judgement of a protocol
	// binding, the served protocols) reads what the registry bound.
	for _, b := range g.AccountTypes() {
		if _, ok := b.Supports("p_video"); ok && b.Plugin.Key == "relay" {
			t.Fatalf("relay/apikey still serves p_video: %+v", b.Type.Platforms)
		}
	}
	if b, _ := g.AccountType("relay", "apikey"); len(b.Type.Platforms) != 1 {
		t.Fatalf("AccountType keeps p_video: %+v", b.Type.Platforms)
	}
	if len(relay.AccountTypes[0].Platforms) != 2 {
		t.Fatal("the manifest was changed")
	}
}

func (stub) Execute(context.Context, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error) {
	return core.ExecutionResult{}, errors.New("stub does not execute requests")
}

func (stub) Monitor(context.Context, *pluginv1.PollRequest, core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
	return nil, errors.New("stub does not monitor tasks")
}

// chatToResponses converts openai.chat to openai.responses only.
type chatToResponses struct{}

func (chatToResponses) Conversion(client, upstream string) (core.Conversion, bool) {
	return core.Conversion{Rank: 1, Grade: "good", Hops: []string{client, upstream}}, client == "openai.chat" && upstream == "openai.responses"
}

// An account type's adapted endpoints need a core conversion to an endpoint
// of its subset: a type listing one the core cannot convert is not bound
// (CONTRACTS §121.1); without converters none listing any is.
func TestAdaptedNeedsConverter(t *testing.T) {
	bound := func(reg *registry.Registry, adapted ...string) bool {
		m := registrytest.Manifest("cx", "1.0.0")
		ap := manifest.AccountPlatform{Platform: manifest.PlatformOpenAI, Endpoints: []string{"responses"}, Adapted: map[string]manifest.AdaptOptions{}}
		for _, id := range adapted {
			ap.Adapted[id] = manifest.AdaptOptions{}
		}
		m.AccountTypes[0].Platforms = []manifest.AccountPlatform{ap}
		g := reg.Publish([]registry.Extension{ext{pkg: load(t, m)}})
		_, ok := g.AccountType("cx", m.AccountTypes[0].ID)
		return ok && len(g.AccountTypesForPlatform(manifest.PlatformOpenAI)) == 1
	}
	conv := registry.WithConverters(chatToResponses{})
	if !bound(registry.New(conv)) || !bound(registry.New(conv), "chat_completions") {
		t.Fatal("a type whose adapted endpoints convert must be bound")
	}
	if bound(registry.New(conv), "chat_completions", "images_generations") {
		t.Fatal("a type with an adapted endpoint the core does not convert was bound")
	}
	if bound(registry.New(), "chat_completions") {
		t.Fatal("without converters a type listing adapted endpoints was bound")
	}
}

// settingsExt is an extension whose process checks settings, as every
// grpcruntime instance does.
type settingsExt struct{ ext }

func (settingsExt) SettingsValidator() core.SettingsValidator { return settingsStub{} }

type settingsStub struct{}

func (settingsStub) ValidateSettings(context.Context, *pluginv1.ValidateSettingsRequest) (*pluginv1.ValidateSettingsResponse, error) {
	return &pluginv1.ValidateSettingsResponse{}, nil
}

// Every enabled plugin whose extension offers the settings check has it in
// the generation, whatever its grants or capabilities.
func TestSettingsValidator(t *testing.T) {
	m, other := registrytest.Manifest("mailer", "1.0.0"), registrytest.Manifest("plain", "1.0.0")
	m.Platforms, other.Platforms = nil, nil
	g := registry.New().Publish([]registry.Extension{settingsExt{ext{pkg: load(t, m)}}, ext{pkg: load(t, other)}})
	if v, ok := g.SettingsValidator("mailer"); !ok || v == nil {
		t.Fatalf("mailer = %v %v", v, ok)
	}
	if _, ok := g.SettingsValidator("plain"); ok {
		t.Fatal("an extension without the check has none")
	}
	if _, ok := g.SettingsValidator("missing"); ok {
		t.Fatal("a plugin not in the generation has none")
	}
}

// oldUIManifest is an installed package of the contract before the
// declarative UI was removed: a schema account form, schema settings and
// table and form pages.
const oldUIManifest = `{
  "apiVersion": 1, "key": "oldui", "version": "0.4.0", "publisher": "sub2api", "runtime": "grpc",
  "entry": {"grpc": {"binaries": "runtimes/{os}-{arch}/plugin"}},
  "hostCompat": ">=0.1.0",
  "capabilities": [{"id": "platform.adapter.v1"}],
  "hostPermissions": [{"id": "platform.register"}],
  "accountTypes": [{"id": "relay_key", "label": {"en": "Relay key"},
    "form": {"mode": "schema", "schema": "forms/relay_key.schema.json", "uiSchema": "forms/relay_key.ui.json"},
    "platforms": [{"platform": "anthropic"}]}],
  "ui": {
    "pages": {
      "assets": {"type": "table", "title": {"en": "Assets"}, "source": "GET /assets",
        "create": {"page": "new_asset"}, "columns": [{"key": "id", "label": {"en": "#"}, "format": "text"}]},
      "new_asset": {"type": "form", "title": {"en": "New asset"}, "schema": "forms/asset.schema.json", "submit": "POST /assets"}
    },
    "settings": {"mode": "schema", "schema": "forms/settings.schema.json", "uiSchema": "forms/settings.ui.json"}
  }
}`

// An installed package with the removed declarative UI still loads: it is
// not refused as an outdated contract, the core's lenient decoding reads it
// and it takes part in a generation (only its forms and pages are not
// rendered).
func TestOldUIPackageLoads(t *testing.T) {
	raw := []byte(oldUIManifest)
	m, err := pluginpkg.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if msg, outdated := check.OutdatedPackage(m, raw, "0.1.400"); outdated {
		t.Fatalf("refused as outdated: %s", msg)
	}
	p, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), map[string][]byte{"manifest.json": raw}), "sub2api", "official")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	g := registry.New().Publish([]registry.Extension{ext{pkg: p}})
	if info, ok := g.Plugin("oldui"); !ok || len(info.Conflicts) != 0 {
		t.Fatalf("plugin = %+v %v", info, ok)
	}
	if b, ok := g.AccountType("oldui", "relay_key"); !ok || b.Type.Form.Mode != "schema" {
		t.Fatalf("account type = %+v %v", b, ok)
	}
}
