package gateway

import (
	"maps"
	"net/url"
	"slices"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// meta describes the request to plugins. Protocol is the endpoint protocol
// and Environment has no account layer; attempts override both with the
// upstream protocol and the attempt's environment (metaFor).
func (c *call) meta() *pluginv1.RequestMeta {
	if !c.metaMapsDone {
		c.metaPathParams = validMeta(c.params)
		c.metaQuery = validMeta(c.declaredQuery())
		c.metaMapsDone = true
	}
	m := &pluginv1.RequestMeta{
		RequestId: c.rid, Protocol: c.ep.Protocol, ClientProtocol: c.ep.Protocol,
		Model: c.model, Stream: c.stream, ClientIp: c.c.ClientIP(),
		PathParams: c.metaPathParams, Query: c.metaQuery,
		Endpoint: c.ep.Path, SessionFingerprint: c.sessionFP, Environment: c.environment(nil),
	}
	if p := c.principal; p != nil {
		m.UserId, m.ApiKeyId, m.GroupId = p.UserID, p.KeyID, p.Group.ID
	}
	return m
}

// declaredQuery returns the query parameters the endpoint listed in
// request.queryParams, and nothing else. The query string can carry
// credentials — the endpoint's auth.query parameter, and whatever the next
// upstream invents — so the host never hands over the whole thing and never
// tries to guess which names are secret: an allow-list is wrong in the safe
// direction, a deny-list in the unsafe one.
//
// Matching is case-insensitive in both directions, because url.Values is a
// case-sensitive map and HTTP clients are not: "?Alt=sse" must satisfy a
// declared "alt", and "?Key=sk-..." must still be excluded by auth.query
// "key". Install-time validation guarantees no declared name equals
// auth.query under EqualFold, so the exclusion can never be overridden here.
func (c *call) declaredQuery() map[string]string {
	names := c.ep.Request.QueryParams
	if len(names) == 0 || c.c.Request == nil || c.c.Request.URL == nil {
		return nil
	}
	values := c.c.Request.URL.Query()
	if len(values) == 0 {
		return nil
	}
	// Sorted, so a client sending both "?alt=" and "?Alt=" gets the same
	// answer on every node and every attempt.
	sent := slices.Sorted(maps.Keys(values))
	out := make(map[string]string, len(names))
	for _, name := range names {
		if auth := c.ep.Auth.Query; auth != "" && strings.EqualFold(name, auth) {
			continue
		}
		if k, ok := sentAs(values, sent, name); ok {
			out[k] = values[k][0]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sentAs is the query parameter of sent (sorted) that is name, ignoring
// case, with a value: the key is the name as the client sent it; only the
// first value of a repeated parameter travels.
func sentAs(values url.Values, sent []string, name string) (string, bool) {
	for _, k := range sent {
		if strings.EqualFold(k, name) && len(values[k]) > 0 {
			return k, true
		}
	}
	return "", false
}

// validMeta is a RequestMeta path_params or query map as the client sent
// it, every entry of any size. It returns nil for an empty map: the field
// is optional and a nil map costs no allocation on the hot path.
//
// The map comes straight off the wire, so invalid UTF-8 is dropped: proto3
// string fields must be valid UTF-8 and marshalling a RequestMeta with a
// stray byte would fail every plugin call of the request.
func validMeta(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		name := strings.ToValidUTF8(k, "")
		if name == "" {
			continue
		}
		out[name] = strings.ToValidUTF8(v, "")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// metaFor is meta for an upstream attempt on rt.
func (c *call) metaFor(rt *typeRoute) *pluginv1.RequestMeta {
	m := c.meta()
	m.Protocol, m.Environment = rt.upstream, c.env
	if c.rec != nil && c.rec.AccountID != nil {
		m.AccountId = *c.rec.AccountID
	}
	return m
}

func (c *call) passHeaders(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v := c.c.GetHeader(n); v != "" {
			out[headerLower(n)] = v
		}
	}
	return out
}
