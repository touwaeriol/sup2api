// Package registry builds immutable generations of the extensions contributed
// by the plugin instances active on this node, and switches them atomically.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"mime"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/platforms"
	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// Extension is one running plugin version that contributes to a generation.
// Capability accessors return nil when the plugin does not implement the
// capability.
type Extension interface {
	Package() *Package
	Grants() Grants
	Platform() core.PlatformPlugin
	Hook() core.HookPlugin
	App() core.AppPlugin
	HTTP() core.HTTPPlugin
	Scheduler() core.SchedulerPlugin
}

// Grants maps approved host permission ids to their approved scope JSON.
type Grants map[string]json.RawMessage

func (g Grants) Has(permission string) bool {
	_, ok := g[permission]
	return ok
}

// Scope decodes the approved scope of a permission (empty map when none).
func (g Grants) Scope(permission string) map[string]any {
	out := map[string]any{}
	if raw, ok := g[permission]; ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// List returns scope[field] as a string list; ok=false when the field is
// absent (meaning "not restricted").
func (g Grants) List(permission, field string) (list []string, ok bool) {
	v, present := g.Scope(permission)[field]
	if !present {
		return nil, false
	}
	arr, _ := v.([]any)
	for _, x := range arr {
		if s, isStr := x.(string); isStr {
			list = append(list, s)
		}
	}
	return list, true
}

// Info builds the core.PluginInfo of a package.
func Info(p *Package) core.PluginInfo {
	return core.PluginInfo{
		Key:       p.Key,
		Version:   p.Version,
		Manifest:  p.Manifest,
		Publisher: p.Publisher,
		Trust:     p.Trust,
		AssetBase: p.AssetBase(),
	}
}

// Registry implements core.PluginRegistry.
type Registry struct {
	cur  atomic.Pointer[generation]
	seq  atomic.Uint64
	conv core.ProtocolConverters

	mu        sync.Mutex // serializes Publish and listener changes
	listeners map[int]func(core.Generation)
	nextID    int
}

// Option configures a Registry.
type Option func(*Registry)

// WithConverters gives the registry the core's protocol converters, which
// an account type's adapted endpoints must have (CONTRACTS §121.1). Without
// them no account type listing adapted endpoints is bound.
func WithConverters(conv core.ProtocolConverters) Option {
	return func(r *Registry) { r.conv = conv }
}

func New(opts ...Option) *Registry {
	r := &Registry{listeners: map[int]func(core.Generation){}}
	for _, o := range opts {
		o(r)
	}
	r.cur.Store(build(0, nil, r.conv))
	return r
}

func (r *Registry) Current() core.Generation { return r.cur.Load() }

func (r *Registry) OnChange(fn func(core.Generation)) (cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextID
	r.nextID++
	r.listeners[id] = fn
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.listeners, id)
	}
}

// Publish builds a new generation from exts (at most one per plugin key) and
// switches to it atomically, then notifies listeners in registration order.
func (r *Registry) Publish(exts []Extension) core.Generation {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := build(r.seq.Add(1), exts, r.conv)
	r.cur.Store(g)
	ids := make([]int, 0, len(r.listeners))
	for id := range r.listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		r.listeners[id](g)
	}
	return g
}

// ------------------------------------------------------------------ generation

type generation struct {
	number    uint64
	plugins   []core.PluginInfo
	byKey     map[string]core.PluginInfo
	packages  map[string]*Package
	endpoints []core.EndpointBinding
	platforms []core.PlatformBinding // built-in (by id), then plugin platforms
	platByID  map[string]core.PlatformBinding
	byProto   map[string]core.PlatformBinding
	accTypes  []core.AccountTypeBinding // sorted by plugin key, type id
	atByKey   map[core.AccountTypeKey]core.AccountTypeBinding
	atByPlat  map[string][]core.AccountTypeBinding // platform id -> types, same order
	hooks     map[string][]core.HookBinding
	scheds    map[string]core.SchedulerPlugin
	rankers   []core.AccountRankerBinding // sorted by rank order, then plugin key
	routes    map[string][]core.RouteBinding
	jobs      []core.JobBinding
	runners   map[string]core.AppPlugin
	settings  map[string]core.SettingsValidator
	subs      []core.SubscriptionBinding
	observers []core.ObservationBinding
	channels  []core.NotificationChannelBinding
	payments  []core.PaymentGatewayBinding
	assets    sync.Map // key + "\x00" + path -> asset
	// conv are the core's converters an account type's adapted endpoints
	// need (unservedAdapted).
	conv core.ProtocolConverters
}

func (g *generation) Observers() []core.ObservationBinding { return g.observers }

func (g *generation) NotificationChannels() []core.NotificationChannelBinding { return g.channels }

type asset struct {
	data []byte
	ct   string
}

// maxCachedAsset bounds the size of a single cached asset.
const maxCachedAsset = 4 << 20

// addPlatform registers a platform and its endpoints unless its id, one of
// its protocols or one of its endpoints is already taken, or a plugin
// endpoint path is one the core reserves (validation rejects these at
// upload, approval, enable and upgrade; this is the fallback for a newer core
// that reserved more, or packages approved concurrently). Returns the reason
// on refusal.
func (g *generation) addPlatform(b core.PlatformBinding) string {
	p := b.Platform
	if p.ID == "" {
		return "empty platform id"
	}
	if !b.Builtin {
		for _, e := range p.Endpoints {
			if route, ok := manifest.CoreRouteOf(e.Path); ok {
				return fmt.Sprintf("endpoint %s %s is under the core route %s", e.Method, e.Path, route)
			}
			if route, ok := manifest.CoreGatewayRouteOf(e.Path); ok {
				return fmt.Sprintf("endpoint %s %s is under %s, served by the core", e.Method, e.Path, route)
			}
			if route, ok := manifest.ConsoleRouteOf(e.Method, e.Path); ok {
				return fmt.Sprintf("endpoint %s %s would replace the console path %s", e.Method, e.Path, route)
			}
		}
	}
	if other, taken := g.platByID[p.ID]; taken {
		return fmt.Sprintf("platform id %q is already registered by %s", p.ID, platformOwner(other))
	}
	protos := map[string]bool{}
	for i, e := range p.Endpoints {
		if other, taken := g.byProto[e.Protocol]; taken {
			return fmt.Sprintf("protocol %q is already registered by %s", e.Protocol, platformOwner(other))
		}
		protos[e.Protocol] = true
		for _, eb := range g.endpoints {
			if pluginpkg.EndpointsConflict(eb.Endpoint, e) {
				return fmt.Sprintf("endpoint %s %s conflicts with %s %s of platform %q",
					e.Method, e.Path, eb.Endpoint.Method, eb.Endpoint.Path, eb.Platform)
			}
		}
		for _, o := range p.Endpoints[:i] {
			if pluginpkg.EndpointsConflict(o, e) {
				return fmt.Sprintf("endpoint %s %s conflicts with %s %s of the same platform", e.Method, e.Path, o.Method, o.Path)
			}
		}
	}
	g.platforms = append(g.platforms, b)
	g.platByID[p.ID] = b
	for proto := range protos {
		g.byProto[proto] = b
	}
	for _, e := range p.Endpoints {
		g.endpoints = append(g.endpoints, core.EndpointBinding{Plugin: b.Plugin, Platform: p.ID, Endpoint: e})
	}
	return ""
}

func platformOwner(b core.PlatformBinding) string {
	if b.Builtin {
		return "the core (built-in)"
	}
	return fmt.Sprintf("plugin %q", manifest.DisplayID(b.Plugin.Key))
}

// unknownProtocols returns the protocols of a declarative match that no
// platform of this generation declares. Glob patterns are left alone.
func (g *generation) unknownProtocols(protos []string) []string {
	var out []string
	for _, proto := range protos {
		if _, ok := g.byProto[proto]; ok || strings.ContainsAny(proto, "*?") {
			continue
		}
		out = append(out, proto)
	}
	return out
}

func hasCapability(m *manifest.Manifest, capID string) bool {
	for _, c := range m.Capabilities {
		if c.ID == capID {
			return true
		}
	}
	return false
}

func build(number uint64, exts []Extension, conv core.ProtocolConverters) *generation {
	g := &generation{
		number:   number,
		conv:     conv,
		byKey:    map[string]core.PluginInfo{},
		packages: map[string]*Package{},
		platByID: map[string]core.PlatformBinding{},
		byProto:  map[string]core.PlatformBinding{},
		atByKey:  map[core.AccountTypeKey]core.AccountTypeBinding{},
		atByPlat: map[string][]core.AccountTypeBinding{},
		hooks:    map[string][]core.HookBinding{},
		scheds:   map[string]core.SchedulerPlugin{},
		routes:   map[string][]core.RouteBinding{},
		settings: map[string]core.SettingsValidator{},
	}
	// Built-in platforms always exist (ARCHITECTURE 6.6).
	for _, p := range platforms.Builtin() {
		if reason := g.addPlatform(core.PlatformBinding{Builtin: true, Platform: p}); reason != "" {
			slog.Error("plugin registry: built-in platform skipped", "platform", p.ID, "reason", reason)
		}
	}
	// The first claimant of an exclusive resource keeps it: plugins are added
	// in the order their keys were first approved (unknown last), then by key.
	for _, ext := range sortedExtensions(exts) {
		pkg := ext.Package()
		if _, dup := g.byKey[pkg.Key]; dup {
			continue
		}
		g.registerPlugin(ext, pkg)
	}
	g.finish()
	return g
}

// sortedExtensions orders plugins by approval time (unknown last), then key.
func sortedExtensions(exts []Extension) []Extension {
	sorted := append([]Extension(nil), exts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Package(), sorted[j].Package()
		if a.ClaimedAt.IsZero() != b.ClaimedAt.IsZero() {
			return !a.ClaimedAt.IsZero()
		}
		if !a.ClaimedAt.Equal(b.ClaimedAt) {
			return a.ClaimedAt.Before(b.ClaimedAt)
		}
		return a.Key < b.Key
	})
	return sorted
}

// registerPlugin adds one plugin's resources to the generation. Each resource
// type checks its own grant; a conflicting platform marks the plugin
// abnormal (PluginInfo.Conflicts) instead of skipping the plugin.
func (g *generation) registerPlugin(ext Extension, pkg *Package) {
	info := Info(pkg)
	m := pkg.Manifest
	grants := ext.Grants()
	for permission := range grants {
		info.GrantedPermissions = append(info.GrantedPermissions, permission)
	}
	sort.Strings(info.GrantedPermissions)
	g.plugins = append(g.plugins, info)
	g.byKey[pkg.Key] = info
	g.packages[pkg.Key] = pkg

	pf := ext.Platform()
	conflicts := g.addPluginPlatforms(info, pkg, pf)
	g.addAccountTypes(info, pkg, pf)
	g.addHooks(ext, info, m, grants)
	g.addRanker(ext, info, pkg, m)
	g.addRoutes(ext, info, pkg, grants)
	g.addObserver(ext, info, m, grants)
	g.addJobsAndEvents(ext, info, m, grants)
	g.addSettingsValidator(ext, info)
	conflicts = append(conflicts, g.addNotificationChannels(ext, info, m)...)
	conflicts = append(conflicts, g.addPaymentGateways(ext, info, m, grants)...)
	if len(conflicts) > 0 {
		info.Conflicts = conflicts
		g.plugins[len(g.plugins)-1] = info
		g.byKey[pkg.Key] = info
	}
}

// addPluginPlatforms registers the platforms m declares. Plugin platforms:
// ids are unique across built-in and plugin platforms and endpoints never
// overlap; a conflicting platform is skipped as a whole (the first claimant
// wins) and the plugin is abnormal (PluginInfo.Conflicts). Client is the
// declaring plugin's PlatformService, nil when it does not implement
// platform.adapter.v1 (built-in platforms always have nil).
func (g *generation) addPluginPlatforms(info core.PluginInfo, pkg *Package, pf core.PlatformPlugin) []core.ResourceConflict {
	var conflicts []core.ResourceConflict
	for _, p := range pkg.Manifest.Platforms {
		if reason := g.addPlatform(core.PlatformBinding{Plugin: info, Platform: p, Client: pf}); reason != "" {
			slog.Error("plugin registry: platform skipped, the plugin is abnormal", "plugin", pkg.Key, "version", pkg.Version,
				"platform", p.ID, "reason", reason)
			conflicts = append(conflicts, core.ResourceConflict{Resource: "platform", ID: p.ID, Reason: reason})
		}
	}
	return conflicts
}

// addAccountTypes registers the account types the plugin declares. Each is
// served by the declaring plugin, which implements platform.adapter.v1
// (ARCHITECTURE 6.6) and receives the accounts of its types (CONTRACTS
// §124); without a platform client they are not offered or scheduled.
func (g *generation) addAccountTypes(info core.PluginInfo, pkg *Package, pf core.PlatformPlugin) {
	for i, at := range pkg.Manifest.AccountTypes {
		key := core.AccountTypeKey{PluginKey: pkg.Key, Type: at.ID}
		if _, dup := g.atByKey[key]; dup {
			continue
		}
		if pf == nil {
			continue
		}
		atb := core.AccountTypeBinding{Plugin: info, Type: at, DeclIndex: i, Client: pf}
		g.atByKey[key] = atb
		g.accTypes = append(g.accTypes, atb)
	}
}

// addHooks registers the plugin's hooks, restricted to the granted points and
// fields.
func (g *generation) addHooks(ext Extension, info core.PluginInfo, m *manifest.Manifest, grants Grants) {
	hk := ext.Hook()
	if hk == nil || !grants.Has("gateway.hook") {
		return
	}
	points, pointsLimited := grants.List("gateway.hook", "points")
	fields, fieldsLimited := grants.List("gateway.hook", "fields")
	for i, h := range m.Hooks {
		if pointsLimited && !contains(points, h.Point) {
			continue
		}
		if h.ID == "" {
			h.ID = strconv.Itoa(i)
		}
		granted := make([]string, 0, len(h.Needs))
		for _, n := range h.Needs {
			if !fieldsLimited || contains(fields, n) {
				granted = append(granted, n)
			}
		}
		g.hooks[h.Point] = append(g.hooks[h.Point], core.HookBinding{Plugin: info, Hook: h, GrantedFields: granted, Client: hk})
	}
}

// addRanker registers the scheduler, and the rank hook when the manifest
// declares one and the plugin holds scheduler.rank.
func (g *generation) addRanker(ext Extension, info core.PluginInfo, pkg *Package, m *manifest.Manifest) {
	sc := ext.Scheduler()
	if sc == nil {
		return
	}
	g.scheds[pkg.Key] = sc
	// Rewriting the scheduling parameters of candidate accounts needs both the
	// manifest declaration and the capability; the scheduler.rank grant is
	// checked by install-time validation.
	if m.Scheduler != nil && m.Scheduler.Rank != nil && hasCapability(m, manifest.CapSchedulerRank) {
		g.rankers = append(g.rankers, core.AccountRankerBinding{Plugin: info, Rank: *m.Scheduler.Rank, Client: sc})
	}
}

// addRoutes registers the plugin's HTTP routes it holds grants for.
func (g *generation) addRoutes(ext Extension, info core.PluginInfo, pkg *Package, grants Grants) {
	hx := ext.HTTP()
	if hx == nil {
		return
	}
	for _, rt := range pkg.Manifest.Routes {
		if !grants.Has("routes." + rt.Scope) {
			continue
		}
		// Routes account actions post to are checked as if they declared
		// accounts, so packages installed before the field are covered too.
		rt.Accounts = manifest.EffectiveRouteAccounts(pkg.Manifest, rt)
		g.routes[pkg.Key] = append(g.routes[pkg.Key], core.RouteBinding{Plugin: info, Route: rt, Client: hx})
	}
}

// addObserver registers the observation contract, restricted to the granted
// account scope.
func (g *generation) addObserver(ext Extension, info core.PluginInfo, m *manifest.Manifest, grants Grants) {
	source, ok := ext.(interface{ Observer() core.ObservationPlugin })
	if !ok || m.Observations == nil || !hasCapability(m, manifest.CapGatewayObserve) || !grants.Has("gateway.observe") {
		return
	}
	types, _ := grants.Scope("gateway.observe")["types"].(string)
	if client := source.Observer(); client != nil && (types == "own" || types == "all") {
		g.observers = append(g.observers, core.ObservationBinding{Plugin: info, Declaration: *m.Observations, AllAccounts: types == "all", Client: client})
	}
}

// addNotificationChannels registers the channels the plugin delivers. A
// channel id another plugin already claimed is skipped and the plugin is
// abnormal, like a conflicting platform.
func (g *generation) addNotificationChannels(ext Extension, info core.PluginInfo, m *manifest.Manifest) []core.ResourceConflict {
	source, ok := ext.(interface {
		Notifier() core.NotificationChannelPlugin
	})
	if !ok || m.Notifications == nil || !hasCapability(m, manifest.CapNotifyChannel) {
		return nil
	}
	client := source.Notifier()
	if client == nil {
		return nil
	}
	var conflicts []core.ResourceConflict
	for _, c := range m.Notifications.Channels {
		if i := slices.IndexFunc(g.channels, func(b core.NotificationChannelBinding) bool { return b.Channel.ID == c.ID }); i >= 0 {
			reason := "notification channel already declared by plugin " + g.channels[i].Plugin.Key
			slog.Error("plugin registry: notification channel skipped, the plugin is abnormal", "plugin", info.Key, "channel", c.ID, "reason", reason)
			conflicts = append(conflicts, core.ResourceConflict{Resource: "notification_channel", ID: c.ID, Reason: reason})
			continue
		}
		g.channels = append(g.channels, core.NotificationChannelBinding{Plugin: info, Channel: c, Client: client})
	}
	return conflicts
}

// addSettingsValidator registers the plugin's check of its settings: every
// plugin process serves PluginService, so the extension offers it whether
// or not the plugin implements the call.
func (g *generation) addSettingsValidator(ext Extension, info core.PluginInfo) {
	if source, ok := ext.(interface{ SettingsValidator() core.SettingsValidator }); ok {
		g.settings[info.Key] = source.SettingsValidator()
	}
}

// addJobsAndEvents registers the plugin's jobs and event subscriptions, each
// restricted to the granted scope.
func (g *generation) addJobsAndEvents(ext Extension, info core.PluginInfo, m *manifest.Manifest, grants Grants) {
	app := ext.App()
	if app == nil {
		return
	}
	if grants.Has("jobs") {
		if g.runners == nil {
			g.runners = map[string]core.AppPlugin{}
		}
		g.runners[info.Key] = app
		for _, j := range m.Jobs {
			g.jobs = append(g.jobs, core.JobBinding{Plugin: info, Job: j, Client: app})
		}
	}
	if m.Events == nil || len(m.Events.Subscribe) == 0 || !grants.Has("events") {
		return
	}
	ev := *m.Events
	if allowed, limited := grants.List("events", "subscribe"); limited {
		ev.Subscribe = nil
		for _, s := range m.Events.Subscribe {
			if eventAllowed(allowed, s) {
				ev.Subscribe = append(ev.Subscribe, s)
			}
		}
	}
	if len(ev.Subscribe) > 0 {
		g.subs = append(g.subs, core.SubscriptionBinding{Plugin: info, Events: ev, Client: app})
	}
}

// finish sorts the collections and warns about declarations that name
// resources no plugin of this generation provides.
func (g *generation) finish() {
	for point, hs := range g.hooks {
		sort.SliceStable(hs, func(i, j int) bool {
			if hs[i].Hook.Order != hs[j].Hook.Order {
				return hs[i].Hook.Order < hs[j].Hook.Order
			}
			return hs[i].Plugin.Key < hs[j].Plugin.Key
		})
		// A hook may match a protocol declared by another plugin's platform,
		// so install order decides what exists: an unknown protocol is not an
		// install error, it only means the hook never runs. Warn once per
		// generation so a typo is visible. Patterns are left alone.
		for _, h := range hs {
			for _, proto := range g.unknownProtocols(h.Hook.Match.Protocols) {
				slog.Warn("plugin registry: hook matches a protocol no platform declares",
					"plugin", h.Plugin.Key, "hook", h.Hook.ID, "point", point, "protocol", proto)
			}
		}
	}
	// scheduler.rank runs in declared order, ties by plugin key, and its match
	// may name a protocol of another plugin's platform: same warning as hooks.
	sort.SliceStable(g.rankers, func(i, j int) bool {
		a, b := g.rankers[i], g.rankers[j]
		if a.Rank.Order != b.Rank.Order {
			return a.Rank.Order < b.Rank.Order
		}
		return a.Plugin.Key < b.Plugin.Key
	})
	for _, rk := range g.rankers {
		for _, proto := range g.unknownProtocols(rk.Rank.Match.Protocols) {
			slog.Warn("plugin registry: scheduler.rank matches a protocol no platform declares",
				"plugin", rk.Plugin.Key, "protocol", proto)
		}
	}
	sort.SliceStable(g.accTypes, func(i, j int) bool {
		a, b := g.accTypes[i], g.accTypes[j]
		if a.Plugin.Key != b.Plugin.Key {
			return a.Plugin.Key < b.Plugin.Key
		}
		return a.DeclIndex < b.DeclIndex
	})
	kept := g.accTypes[:0]
	for _, atb := range g.accTypes {
		atb = g.withoutForeignFacts(atb)
		if id := g.unservedAdapted(atb); id != "" {
			slog.Error("plugin registry: account type skipped, the core converts no adapted endpoint to its endpoints",
				"plugin", atb.Plugin.Key, "accountType", atb.Type.ID, "endpoint", id)
			delete(g.atByKey, atb.Key())
			continue
		}
		kept = append(kept, atb)
		g.atByKey[atb.Key()] = atb
		g.indexAccountType(atb)
	}
	g.accTypes = kept
}

// unservedAdapted is the first endpoint an account type lists in adapted
// (manifest.AccountPlatform.Adapted) that no core conversion serves with an
// endpoint of its subset on the same platform, "" when there is none. The
// endpoints of a platform this generation lacks are not judged: the type
// serves nothing there.
func (g *generation) unservedAdapted(atb core.AccountTypeBinding) string {
	for _, ap := range atb.Type.Platforms {
		pb, ok := g.platByID[ap.Platform]
		if !ok {
			continue
		}
		for _, id := range slices.Sorted(maps.Keys(ap.Adapted)) {
			i := slices.IndexFunc(pb.Platform.Endpoints, func(e manifest.Endpoint) bool { return e.ID == id })
			if i < 0 {
				return id
			}
			if _, ok := core.BestConvertTarget(g, atb, g.conv, pb.Platform.Endpoints[i].Protocol, func(p manifest.AccountPlatform, _ manifest.Endpoint) bool {
				return p.Platform == ap.Platform
			}); !ok {
				return id
			}
		}
	}
	return ""
}

// withoutForeignFacts drops from the type's platforms each one whose usage
// or task facts another plugin states (manifest.AccountPlatform.
// ServesPluginFacts; a built-in platform is no plugin's): the binding is then
// the one definition of the platforms the type serves, so routing, the
// cross-platform judgement of a protocol binding and the served protocols
// all read the platforms the registry binds (forwarding v2, design 4.3).
func (g *generation) withoutForeignFacts(atb core.AccountTypeBinding) core.AccountTypeBinding {
	kept := make([]manifest.AccountPlatform, 0, len(atb.Type.Platforms))
	for _, ap := range atb.Type.Platforms {
		pb, ok := g.platByID[ap.Platform]
		if ok && !pb.Builtin && pb.Plugin.Key != atb.Plugin.Key && ap.ServesPluginFacts(pb.Platform) {
			slog.Error("plugin registry: account type skipped on a platform whose usage or task facts another plugin states",
				"plugin", atb.Plugin.Key, "accountType", atb.Type.ID, "platform", ap.Platform, "platformPlugin", pb.Plugin.Key)
			continue
		}
		kept = append(kept, ap)
	}
	if len(kept) < len(atb.Type.Platforms) {
		atb.Type.Platforms = kept
	}
	return atb
}

// indexAccountType maps an account type under every platform it serves.
func (g *generation) indexAccountType(atb core.AccountTypeBinding) {
	seen := map[string]bool{}
	for _, ap := range atb.Type.Platforms {
		if ap.Platform == "" || seen[ap.Platform] {
			continue
		}
		seen[ap.Platform] = true
		// An account type may serve a platform another plugin declares,
		// which need not be installed or enabled yet, so validation
		// deliberately does not check that the platform exists and this
		// cannot be an install error. But while it does not exist the type
		// serves no endpoint at all and every request for that platform
		// ends in "no enabled account type serves this endpoint", so warn
		// once per generation: a misspelt platform id is indistinguishable
		// from a platform whose plugin is simply not enabled yet.
		if _, ok := g.platByID[ap.Platform]; !ok {
			slog.Warn("plugin registry: account type serves a platform no platform of this generation declares",
				"plugin", atb.Plugin.Key, "accountType", atb.Type.ID, "platform", ap.Platform,
				"hint", "the plugin declaring the platform may not be installed or enabled yet, or the platform id is misspelled")
		}
		g.atByPlat[ap.Platform] = append(g.atByPlat[ap.Platform], atb)
	}
}

func (g *generation) Number() uint64                            { return g.number }
func (g *generation) Plugins() []core.PluginInfo                { return g.plugins }
func (g *generation) Endpoints() []core.EndpointBinding         { return g.endpoints }
func (g *generation) AccountTypes() []core.AccountTypeBinding   { return g.accTypes }
func (g *generation) Jobs() []core.JobBinding                   { return g.jobs }
func (g *generation) Subscriptions() []core.SubscriptionBinding { return g.subs }

func (g *generation) JobRunner(key string) (core.AppPlugin, bool) {
	app, ok := g.runners[key]
	return app, ok
}

func (g *generation) SettingsValidator(key string) (core.SettingsValidator, bool) {
	v, ok := g.settings[key]
	return v, ok
}

func (g *generation) Plugin(key string) (core.PluginInfo, bool) {
	p, ok := g.byKey[key]
	return p, ok
}

func (g *generation) Platforms() []core.PlatformBinding { return g.platforms }

func (g *generation) Platform(platformID string) (core.PlatformBinding, bool) {
	b, ok := g.platByID[platformID]
	return b, ok
}

func (g *generation) PlatformForProtocol(protocol string) (core.PlatformBinding, bool) {
	b, ok := g.byProto[protocol]
	return b, ok
}

func (g *generation) AccountType(pluginKey, typeID string) (core.AccountTypeBinding, bool) {
	b, ok := g.atByKey[core.AccountTypeKey{PluginKey: pluginKey, Type: typeID}]
	return b, ok
}

// AccountTypesForPlatform lists the registered account types declaring
// platformID (whether or not the platform currently exists), sorted by
// plugin key and type id.
func (g *generation) AccountTypesForPlatform(platformID string) []core.AccountTypeBinding {
	return g.atByPlat[platformID]
}

func (g *generation) Hooks(point string) []core.HookBinding { return g.hooks[point] }

func (g *generation) Scheduler(pluginKey string) (core.SchedulerPlugin, bool) {
	s, ok := g.scheds[pluginKey]
	return s, ok
}

// AccountRankers lists the plugins declaring scheduler.rank, sorted by
// declared order then plugin key.
func (g *generation) AccountRankers() []core.AccountRankerBinding { return g.rankers }

func (g *generation) Routes(pluginKey string) []core.RouteBinding { return g.routes[pluginKey] }

// ErrAssetNotFound is returned by ReadAsset for missing files or plugins.
var ErrAssetNotFound = core.ErrNotFound.WithMessage("asset not found")

func (g *generation) ReadAsset(pluginKey, name string) ([]byte, string, error) {
	pkg, ok := g.packages[pluginKey]
	if !ok {
		return nil, "", ErrAssetNotFound
	}
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" {
		return nil, "", ErrAssetNotFound
	}
	ck := pluginKey + "\x00" + name
	if a, ok := g.assets.Load(ck); ok {
		a := a.(asset)
		return a.data, a.ct, nil
	}
	data, err := pkg.ReadFile(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", ErrAssetNotFound
		}
		return nil, "", fmt.Errorf("read asset %s/%s: %w", pluginKey, name, err)
	}
	ct := ContentType(name)
	if len(data) <= maxCachedAsset {
		g.assets.Store(ck, asset{data: data, ct: ct})
	}
	return data, ct, nil
}

// ContentType maps a package file name to its MIME type.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s || x == "*" {
			return true
		}
	}
	return false
}

// eventAllowed reports whether subscription s ("type" or "prefix.*") is
// covered by the approved list (exact, "*" or "prefix.*" entries).
func eventAllowed(allowed []string, s string) bool {
	for _, a := range allowed {
		if a == s || a == "*" {
			return true
		}
		if p, ok := strings.CutSuffix(a, "*"); ok && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Ensure the generation satisfies the contract.
var _ core.Generation = (*generation)(nil)
var _ core.PluginRegistry = (*Registry)(nil)

// HookByID finds a manifest hook by id or by its index (as a string).
func HookByID(m *manifest.Manifest, id string) (manifest.Hook, bool) {
	for i, h := range m.Hooks {
		if h.ID == id || (h.ID == "" && strconv.Itoa(i) == id) {
			return h, true
		}
	}
	return manifest.Hook{}, false
}
