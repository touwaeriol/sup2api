package check

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// platforms validates the plugin's own platforms and their endpoints
// (ARCHITECTURE 6.4, 6.6).
func (v *validator) platforms() {
	pfs := v.m.Platforms
	if len(pfs) == 0 {
		return
	}
	v.needPerm("platforms", "gateway.endpoint", "platforms")
	v.needPerm("platforms", "platform.register", "platforms")
	var own []ownEndpoint
	ids := map[string]bool{}
	for i, p := range pfs {
		f := fmt.Sprintf("platforms[%d]", i)
		switch {
		case !keyRe.MatchString(p.ID):
			v.add(f+".id", "invalid_format", "platform id %q must match %s", p.ID, keyRe.String())
		case platforms.IsBuiltin(p.ID):
			v.add(f+".id", "builtin_platform", "platform id %q is a built-in platform", p.ID)
		case ids[p.ID]:
			v.add(f+".id", "duplicate", "platform %q declared twice", p.ID)
		default:
			for _, o := range v.opt.OtherPlatforms {
				if o.PluginID != v.m.ID() && o.ID == p.ID {
					v.add(f+".id", "platform_conflict", "platform %q is already declared by plugin %q", p.ID, manifest.DisplayID(o.PluginID))
					break
				}
			}
		}
		ids[p.ID] = true
		if len(p.Endpoints) == 0 {
			v.add(f+".endpoints", "required", "at least one endpoint is required")
		}
		epIDs := map[string]bool{}
		for j, e := range p.Endpoints {
			ef := fmt.Sprintf("%s.endpoints[%d]", f, j)
			if !idRe.MatchString(e.ID) {
				v.add(ef+".id", "invalid_format", "endpoint id %q is invalid", e.ID)
			} else if epIDs[e.ID] {
				v.add(ef+".id", "duplicate", "endpoint id %q declared twice", e.ID)
			}
			epIDs[e.ID] = true
			if !v.endpoint(ef, p, e) {
				continue
			}
			v.endpointConflicts(ef, e, &own)
		}
		v.usageRules(f+".usage", p.Usage, ownerPlatform, anyPluginUsage(p))
		if len(p.StickyRules) > 0 {
			v.add(f+".stickyRules", "sticky_not_for_plugins",
				"a plugin may not declare sticky rules: the core and the administrator own sticky sessions")
		}
	}
}

// ownEndpoint is one endpoint this manifest already declared, with the field
// path to report against when a later endpoint collides with it.
type ownEndpoint struct {
	field string
	ep    manifest.Endpoint
}

// endpointConflicts reports every collision of e with the endpoints declared
// earlier in this manifest (own), with a built-in platform, or with another
// installed plugin, and then records e as one of this manifest's own.
// Callers pass endpoints that passed endpoint(), in declaration order.
func (v *validator) endpointConflicts(ef string, e manifest.Endpoint, own *[]ownEndpoint) {
	for _, o := range *own {
		if EndpointsConflict(o.ep, e) {
			v.add(ef+".path", "duplicate", "endpoint %s %s conflicts with %s (%s %s)",
				strings.ToUpper(e.Method), e.Path, o.field, strings.ToUpper(o.ep.Method), o.ep.Path)
			break
		}
	}
	*own = append(*own, ownEndpoint{field: ef, ep: e})
	for _, bp := range platforms.Builtin() {
		for _, be := range bp.Endpoints {
			if EndpointsConflict(be, e) {
				v.add(ef+".path", "endpoint_conflict", "endpoint %s %s conflicts with built-in platform %q (%s %s)",
					strings.ToUpper(e.Method), e.Path, bp.ID, be.Method, be.Path)
			}
		}
	}
	for _, o := range v.opt.OtherEndpoints {
		if o.PluginID == v.m.ID() {
			continue
		}
		if EndpointsConflict(manifest.Endpoint{Method: o.Method, Path: o.Path}, e) {
			v.add(ef+".path", "endpoint_conflict", "endpoint %s %s conflicts with plugin %q (%s %s)",
				strings.ToUpper(e.Method), e.Path, manifest.DisplayID(o.PluginID), o.Method, o.Path)
		}
	}
}

// EndpointTests are the accepted values of endpoint.test ("" means the
// protocol is tested like any other).
var EndpointTests = []string{manifest.EndpointTestExplicit, manifest.EndpointTestNever}

// endpoint validates one endpoint of platform p; ok=false when its
// method or path is malformed (no conflict checks then).
func (v *validator) endpoint(f string, p manifest.Platform, e manifest.Endpoint) (ok bool) {
	platformID := p.ID
	ok = true
	if !allowedMethods[strings.ToUpper(e.Method)] {
		v.add(f+".method", "invalid", "unsupported method %q", e.Method)
		ok = false
	}
	if msg := checkEndpointPath(e.Path); msg != "" {
		v.add(f+".path", "invalid_path", "%s", msg)
		ok = false
	} else if route, console := manifest.ConsoleRouteOf(e.Method, e.Path); console {
		v.add(f+".path", "invalid_path", "a %s endpoint must not start with %s (a console path)", strings.ToUpper(e.Method), route)
		ok = false
	}
	name, hasPrefix := strings.CutPrefix(e.Protocol, platformID+".")
	switch {
	case e.Protocol == "":
		v.add(f+".protocol", "required", "protocol is required")
	case !hasPrefix || name == "" || !protocolRe.MatchString(name):
		v.add(f+".protocol", "invalid_format", "protocol %q must be %q followed by a name", e.Protocol, platformID+".")
	}
	switch e.Kind {
	case manifest.EndpointKindProxy:
		if e.Response.Stream == manifest.ResponseWebSocket {
			v.add(f+".response.stream", "invalid", "response.stream %q is only for kind %q", manifest.ResponseWebSocket, manifest.EndpointKindWebSocket)
		}
	case manifest.EndpointKindWebSocket:
		v.webSocketEndpoint(f, e)
	default:
		v.add(f+".kind", "unsupported", "kind must be %q or %q", manifest.EndpointKindProxy, manifest.EndpointKindWebSocket)
	}
	// The gateway renders every upstream error of this endpoint in this format
	// and bills according to this mode, so neither may be left to a default.
	switch {
	case e.ErrorFormat == "":
		v.add(f+".errorFormat", "required", "errorFormat is required (%s)", strings.Join(errorFormats, ", "))
	case !slices.Contains(errorFormats, e.ErrorFormat):
		v.add(f+".errorFormat", "invalid", "errorFormat must be one of %s", strings.Join(errorFormats, ", "))
	}
	switch e.Billing {
	case "usage", "free":
	case "":
		v.add(f+".billing", "required", "billing is required (usage or free)")
	default:
		v.add(f+".billing", "invalid", "billing must be usage or free")
	}
	if len(e.BillingTypes) == 0 && e.Billing == "usage" {
		v.add(f+".billingTypes", "required", "a metered endpoint must support at least one billing type")
	}
	seenBilling := map[string]bool{}
	for _, kind := range e.BillingTypes {
		if !slices.Contains([]string{"per_request", "per_token", "expression", "video"}, kind) {
			v.add(f+".billingTypes", "invalid", "unknown billing type %q", kind)
		}
		if seenBilling[kind] {
			v.add(f+".billingTypes", "duplicate", "duplicate billing type %q", kind)
		}
		seenBilling[kind] = true
		if e.Billing == "free" || (kind == "video" && (!e.TaskSubmit() || !strings.Contains(e.Task.Kind, "video"))) {
			v.add(f+".billingTypes", "conflict", "billing type %q is incompatible with this endpoint", kind)
		}
	}
	if e.Response.NonStream == "" && !e.Request.Stream && !e.WebSocket() {
		v.add(f+".response.nonStream", "required", "response.nonStream is required unless request.stream is set")
	}
	if len(e.Auth.Headers) == 0 && e.Auth.Query == "" {
		v.add(f+".auth", "required", "auth.headers or auth.query is required")
	}
	if e.Test != "" && !slices.Contains(EndpointTests, e.Test) {
		v.add(f+".test", "invalid", "test must be one of %s (or absent)", strings.Join(EndpointTests, ", "))
	}
	switch {
	case e.Request.ModelPath == "" && e.Request.ModelParam == "" && e.Request.ModelSource == "" && !e.TaskQuery():
		v.add(f+".request", "required", "request.modelPath, request.modelParam or request.modelSource is required")
	case boolCount(e.Request.ModelPath != "", e.Request.ModelParam != "", e.Request.ModelSource != "") > 1:
		v.add(f+".request", "mutually_exclusive", "request.modelPath, request.modelParam and request.modelSource are mutually exclusive")
	case e.Request.ModelParam != "" && !slices.Contains(PathParams(e.Path), e.Request.ModelParam):
		v.add(f+".request.modelParam", "unknown_param", "path %q has no parameter %q", e.Path, e.Request.ModelParam)
	case e.Request.ModelSource != "" && e.Request.ModelSource != manifest.ModelSourcePlugin:
		v.add(f+".request.modelSource", "invalid", "modelSource must be %q", manifest.ModelSourcePlugin)
	case e.Request.ModelSource == manifest.ModelSourcePlugin:
		// ResolveModel answers the model and whether the response streams, so
		// a streamPath next to it would silently never be read.
		if e.Request.StreamPath != "" {
			v.add(f+".request.streamPath", "mutually_exclusive",
				"request.streamPath is not read when modelSource is %q (ResolveModel answers stream)", manifest.ModelSourcePlugin)
		}
		// The host calls PlatformService of the plugin declaring the platform;
		// a plugin without platform.adapter.v1 has no such service, and every
		// request to the endpoint would be a 500 (CONTRACTS §25.1). Skipped in
		// a standalone CheckPlatform: there is no manifest to read.
		if !v.standalone {
			v.needCap(f+".request.modelSource", manifest.CapPlatformAdapter)
		}
	}
	v.queryParams(f, e)
	v.modelReferences(f, p, e)
	v.taskEndpoint(f, e)
	v.usageSource(f, e)
	if e.Usage != nil {
		v.usageRules(f+".usage", *e.Usage, ownerEndpoint, e.PluginUsage() || e.TaskSubmit())
	}
	// The other half of the §25.1 rule the modelSource branch above covers:
	// an endpoint whose usage a plugin reads is served only by the Execute
	// of the plugin declaring the platform (CONTRACTS §112.8), so that
	// plugin needs platform.adapter.v1 as well. This reads the endpoint and
	// nothing else: no account type's override can change the source.
	// Skipped in a standalone CheckPlatform (and for built-in platforms):
	// there is no manifest to read.
	if !v.standalone && e.PluginUsage() {
		v.needCap(f+".usageSource", manifest.CapPlatformAdapter)
	}
	v.streamUsage(f, p, e)
	return ok
}

// streamUsage catches the shape that silently swallows usage: an endpoint that
// declares it can answer with SSE, bills for what it serves, and has no SSE
// extraction rule in effect.
//
// At runtime a streaming response is read event by event and only
// UsageRules.SSE is consulted (usagerules.Acc.ApplySSE); UsageRules.JSON is
// never applied to it. So an endpoint with response.stream, billing "usage"
// and an empty sse list extracts zero tokens from every streaming request it
// serves - the request is answered, the money is not counted, and nothing says
// so. The gateway's runtime comparison of the actual response shape against
// the declaration cannot see this case at all: the declaration is right, it is
// the usage rules that are missing.
//
// "In effect" is the override chain of CONTRACTS §13 as far as it is visible
// here: the endpoint's own usage when it has one, otherwise the platform's.
// The account type layer (AccountPlatform.usage[protocol]) overrides per
// protocol and is validated where it is declared; a platform being checked on
// its own cannot see it, and an account type that overrides usage with an
// empty SSE list is a separate hole this rule does not claim to close.
//
// This rule reads only the endpoint and its platform, never v.m, so it holds
// on the CheckPlatform path too - which is the point: the core's own built-in
// platforms are checked by exactly the same rule.
func (v *validator) streamUsage(f string, p manifest.Platform, e manifest.Endpoint) {
	if e.Response.Stream == "" || e.Billing == "free" {
		return
	}
	u, owner := p.Usage, "platforms usage"
	if e.Usage != nil {
		u, owner = *e.Usage, "this endpoint's usage"
	}
	// usageSource "plugin" is the declared way out of the gjson rules - an
	// endpoint takes it precisely because no sse map can express its usage,
	// so requiring one here would forbid the feature. The cost is
	// deliberate: a plugin answering host_usage is billed on these (then
	// empty) rules, and an empty reading is closed as unreported, never
	// billed as zero (CONTRACTS §112.7), with the usage record marked.
	if e.PluginUsage() {
		return
	}
	for _, s := range u.SSE {
		if len(s.Map) > 0 {
			return
		}
	}
	// Pointing at the endpoint's own usage even when the platform default is
	// the empty one is deliberate: the fix belongs where the exception is.
	v.add(f+".usage.sse", "required",
		"endpoint declares response.stream %q and billing %q, but %s has no non-empty sse rule: "+
			"streaming responses are metered from usage.sse only (usage.json is not applied to them), "+
			"so every streaming request would be billed as zero tokens",
		e.Response.Stream, e.Billing, owner)
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// queryParamRe is what a declared query parameter name may look like: the
// characters a client can put in a query key without percent-encoding and a
// plugin author can type. The host matches them case-insensitively.
var queryParamRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// queryParams validates request.queryParams, the allow-list of query
// parameters the host copies into RequestMeta.query.
//
// The endpoint's auth.query parameter may not be listed, and the comparison
// is case-insensitive because the host's is: it excludes auth.query with
// strings.EqualFold (url.Values is a case-sensitive map, so deleting by exact
// name would leak "?Key=sk-..." to the plugin) and matches the allow-list the
// same way. Catching the collision at install time is what makes that safe.
func (v *validator) queryParams(f string, e manifest.Endpoint) {
	seen := map[string]bool{}
	for i, name := range e.Request.QueryParams {
		qf := fmt.Sprintf("%s.request.queryParams[%d]", f, i)
		if !queryParamRe.MatchString(name) {
			v.add(qf, "invalid_format", "query parameter name %q must match %s", name, queryParamRe.String())
			continue
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			v.add(qf, "duplicate", "query parameter %q is declared twice (names are matched case-insensitively)", name)
		}
		seen[lower] = true
		if e.Auth.Query != "" && strings.EqualFold(name, e.Auth.Query) {
			v.add(qf, "credential_param",
				"query parameter %q is the endpoint's auth.query parameter and carries the API key; it is never passed to plugins", name)
		}
	}
}

func checkEndpointPath(p string) string {
	if !strings.HasPrefix(p, "/") || p == "/" {
		return "path must start with / and not be the root"
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	first := segs[0]
	if first == "" || strings.HasPrefix(first, ":") || strings.HasPrefix(first, "*") {
		return "first path segment must be a literal"
	}
	if route, ok := manifest.CoreRouteOf(p); ok {
		return fmt.Sprintf("path must not start with %s (a core route)", route)
	}
	if route, ok := manifest.CoreGatewayRouteOf(p); ok {
		return fmt.Sprintf("path must not start with %s (served by the core before plugin endpoints)", route)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "path contains an empty or relative segment"
		}
	}
	if _, err := parseEndpointPath(p); err != nil {
		return err.Error()
	}
	return ""
}

// NormalizeRoutePath is manifest.NormalizeRoutePath.
func NormalizeRoutePath(p string) string { return manifest.NormalizeRoutePath(p) }

// The scheduler.affinity extension (a plugin computing sticky-session
// values) was removed; a manifest still naming it gets a precise error.
const (
	removedAffinityCapability = "scheduler.affinity.v1"
	removedAffinityPermission = "scheduler.affinity"
)

// stickyRules checks the default sticky rules of a built-in platform
// (CheckPlatform). The session value always comes from the request itself.
func (v *validator) stickyRules(f0 string, rules []manifest.StickyRule) {
	names := map[string]bool{}
	for i, r := range rules {
		f := fmt.Sprintf("%s[%d]", f0, i)
		if r.Name == "" {
			v.add(f+".name", "required", "name is required")
		} else if names[r.Name] {
			v.add(f+".name", "duplicate", "sticky rule %q declared twice", r.Name)
		}
		names[r.Name] = true
		if len(r.KeySources) == 0 {
			v.add(f+".keySources", "required", "at least one key source is required")
		}
		for j, ks := range r.KeySources {
			switch ks.Type {
			case "body", "header", "api_key", "user":
			default:
				v.add(fmt.Sprintf("%s.keySources[%d].type", f, j), "invalid", "unknown key source type %q", ks.Type)
			}
		}
		if r.ValueRegex != "" {
			if _, err := regexp.Compile(r.ValueRegex); err != nil {
				v.add(f+".valueRegex", "invalid", "invalid regex: %v", err)
			}
		}
		if r.OnFailure != "" && r.OnFailure != "failover" && r.OnFailure != "stick" {
			v.add(f+".onFailure", "invalid", "onFailure must be failover or stick")
		}
	}
}
