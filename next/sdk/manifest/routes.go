package manifest

import (
	"regexp"
	"strings"
)

// Core routes. The core owns these path prefixes and nothing else: /api/v1
// (the console and admin API, including plugin routes under /api/v1/p), the
// bare /api, /plugin-ui (native plugin UI modules) and /healthz (the liveness
// probe registered in server/internal/app). Everything else is free for
// gateway endpoints (ARCHITECTURE 6.4) - /api/v3/... included.
const (
	RouteAPI        = "api"
	RouteAPIVersion = "v1"
	RoutePluginUI   = "plugin-ui"
	RouteHealthz    = "healthz"
)

// CoreRoutePrefixes lists the path prefixes reserved by the core, as
// segment lists. It is the single source of truth: manifest/check rejects
// gateway endpoints that could match one of them (ReservedPath), and the
// core's own router and console handler derive their paths from it. The bare
// /api is reserved as well (see ReservedPath).
var CoreRoutePrefixes = [][]string{{RouteAPI, RouteAPIVersion}, {RoutePluginUI}, {RouteHealthz}}

// ReservedPath reports whether the request or endpoint path p falls under a
// core route. The comparison is on whole segments, case-insensitively, like a
// router match: "/apifoo" and "/api/v3/x" are free, "/api", "/api/v1" and
// "/API/V1/x" are reserved. A parameter (":x") or catch-all ("*x") segment
// matches any literal, so "/api/:version/x" is reserved too: it would serve
// /api/v1/x.
func ReservedPath(p string) bool {
	_, ok := CoreRouteOf(p)
	return ok
}

// CoreRouteOf returns the core route ("/api", "/api/v1", "/plugin-ui",
// "/healthz") the path p falls under, by the rules of ReservedPath.
func CoreRouteOf(p string) (string, bool) {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, prefix := range CoreRoutePrefixes {
		if prefixMatches(prefix, segs) {
			return "/" + strings.Join(prefix, "/"), true
		}
	}
	if strings.EqualFold(segs[0], RouteAPI) && (len(segs) == 1 || segs[1] == "") {
		return "/" + RouteAPI, true
	}
	return "", false
}

// CoreGatewayPrefixes are gateway paths the core serves itself, ahead of
// plugin endpoints: the provider resource APIs (server/internal/gateway,
// resource_http.go) and an API key's own usage (key_usage.go). A plugin
// endpoint under them would never be reached.
var CoreGatewayPrefixes = [][]string{{"v1", "files"}, {"v1", "skills"}, {"v1", "usage"}}

// CoreGatewayRouteOf returns the core gateway route ("/v1/files",
// "/v1/skills") the endpoint path p could match, by the rules of
// ReservedPath.
func CoreGatewayRouteOf(p string) (string, bool) {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, prefix := range CoreGatewayPrefixes {
		if prefixMatches(prefix, segs) {
			return "/" + strings.Join(prefix, "/"), true
		}
	}
	return "", false
}

// CoreGatewayPath reports whether the request path p is served by the
// core's own gateway routes: exactly a CoreGatewayPrefixes path or below it.
func CoreGatewayPath(p string) bool {
	for _, prefix := range CoreGatewayPrefixes {
		route := "/" + strings.Join(prefix, "/")
		if p == route || strings.HasPrefix(p, route+"/") {
			return true
		}
	}
	return false
}

// ConsoleSegments are the first path segments of the console: its static
// files and the roots of its pages (web/src/router, web/public, the build's
// assets/). Plugin gateway endpoints are dispatched before the console, so a
// GET or HEAD endpoint under one of them would replace a console page.
// server/internal/webui tests keep the list in step with the console.
var ConsoleSegments = []string{
	"assets", "index.html", "favicon.svg", "LOBE-ICONS-LICENSE.txt",
	"login", "forbidden", "dashboard", "me", "usage", "account-usage", "ledger", "users", "segments", "groups", "roles",
	"accounts", "api-keys", "proxies", "platforms", "prices", "sticky", "settings",
	"plugins", "p", "market", "publishers", "nodes", "system", "topup", "promo-codes", "redeem-codes",
}

// ConsoleRouteOf returns the console path ("/login", "/assets", ...) a GET
// or HEAD endpoint at p could replace. Other methods never reach the
// console.
func ConsoleRouteOf(method, p string) (string, bool) {
	if !strings.EqualFold(method, "GET") && !strings.EqualFold(method, "HEAD") {
		return "", false
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, s := range ConsoleSegments {
		if prefixMatches([]string{s}, segs) {
			return "/" + s, true
		}
	}
	return "", false
}

// prefixMatches reports whether path segments segs start with prefix,
// treating a parameter or catch-all segment of segs as matching anything (a
// catch-all also matches every segment after it).
func prefixMatches(prefix, segs []string) bool {
	for i, want := range prefix {
		if i >= len(segs) {
			return false
		}
		s := segs[i]
		switch {
		case strings.HasPrefix(s, "*"):
			return true
		case strings.HasPrefix(s, ":"):
		case !strings.EqualFold(s, want):
			return false
		}
	}
	return true
}

// routeRefRe matches a manifest route reference such as "GET /models" or
// "/models" (method defaulting to the caller's). It is the Go mirror of the
// console's parseRouteRef(), which turns an account action's route into the
// request; the two must agree on what parses, or the host would accept a
// reference the console cannot call.
var routeRefRe = regexp.MustCompile(`^\s*(?i:(GET|POST|PUT|PATCH|DELETE)\s+)?(/\S*)\s*$`)

// ParseRouteRef parses a manifest route reference ("GET /models", "/models")
// into an upper-case method and a path. defaultMethod is used when the
// reference names none (POST for an account action, matching the console).
func ParseRouteRef(ref, defaultMethod string) (method, path string, ok bool) {
	m := routeRefRe.FindStringSubmatch(ref)
	if m == nil {
		return "", "", false
	}
	method = strings.ToUpper(m[1])
	if method == "" {
		method = strings.ToUpper(defaultMethod)
	}
	return method, m[2], true
}

// NormalizeRoutePath replaces gin parameter names so "/v1/:a" and "/v1/:b"
// compare equal.
func NormalizeRoutePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		switch {
		case strings.HasPrefix(s, ":"):
			segs[i] = ":"
		case strings.HasPrefix(s, "*"):
			segs[i] = "*"
		}
	}
	return strings.TrimSuffix(strings.Join(segs, "/"), "/")
}

// RouteSignature identifies a plugin route by method and NormalizeRoutePath,
// so "/assets/:id" and "/assets/:asset" are the same route (gin binds by
// position).
func RouteSignature(method, path string) string {
	return strings.ToUpper(method) + " " + NormalizeRoutePath(path)
}
