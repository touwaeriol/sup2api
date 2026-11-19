package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// HandlerFunc serves one plugin route.
type HandlerFunc func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error)

// Router dispatches HTTPService calls by method and manifest route pattern
// (HTTPRequest.route_path, falling back to path). It implements HTTP.
//
// Routes are compiled when registered: each method keeps its patterns sorted
// so that the pattern with more literal segments wins and a tie is broken by
// the pattern text, never by map iteration order. Two patterns can therefore
// never resolve differently between two runs.
type Router struct {
	byMethod map[string][]route
}

// route is one compiled pattern of a method.
type route struct {
	pattern  string
	segments []string
	literals int // segments without a parameter, higher wins
	handler  HandlerFunc
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{byMethod: map[string][]route{}}
}

// Handle registers h for method and the manifest route path, e.g.
// Handle("GET", "/models", h). Registering the same method and path again
// replaces the handler, as the previous map-backed router did. A zero-value
// Router is usable: the first Handle initialises its table.
func (r *Router) Handle(method, path string, h HandlerFunc) {
	method = strings.ToUpper(method)
	if r.byMethod == nil {
		r.byMethod = map[string][]route{}
	}
	rt := route{pattern: path, segments: splitPath(path), handler: h}
	for _, s := range rt.segments {
		if !strings.HasPrefix(s, ":") {
			rt.literals++
		}
	}
	list := r.byMethod[method]
	for i := range list {
		if list[i].pattern == path {
			list[i].handler = h
			return
		}
	}
	list = append(list, rt)
	// The most literal pattern wins; ties are broken by the pattern text, so
	// the outcome never depends on registration or map iteration order.
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].literals != list[j].literals {
			return list[i].literals > list[j].literals
		}
		return list[i].pattern < list[j].pattern
	})
	r.byMethod[method] = list
}

// HandleHTTP implements HTTP.
func (r *Router) HandleHTTP(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	method := strings.ToUpper(req.GetMethod())
	for _, p := range []string{req.GetRoutePath(), req.GetPath()} {
		if p == "" {
			continue
		}
		// A request may carry the registered pattern itself as route_path (the
		// core sends the pattern and fills path_params). That is an exact
		// match: answer it as is, without matching the pattern's own ":name"
		// segments against themselves and overwriting the parameters.
		patterns := r.byMethod[method]
		for i := range patterns {
			if patterns[i].pattern == p {
				return patterns[i].handler(ctx, req)
			}
		}
		// Otherwise match the request path against the compiled patterns, most
		// literal segments first, so an exact path (/models/list) comes before
		// a parameter one (/models/:id) that would also match.
		parts := splitPath(p)
		for _, rt := range patterns {
			params, ok := matchSegments(rt.segments, parts)
			if !ok {
				continue
			}
			// Fill path_params for Param() helper
			if len(params) > 0 {
				if req.PathParams == nil {
					req.PathParams = make(map[string]string)
				}
				for k, v := range params {
					req.PathParams[k] = v
				}
			}
			return rt.handler(ctx, req)
		}
	}
	return ErrorResponse(http.StatusNotFound, "not_found", fmt.Sprintf("no route for %s %s", method, req.GetPath())), nil
}

// splitPath splits a pattern or request path into segments.
func splitPath(p string) []string {
	return strings.Split(strings.Trim(p, "/"), "/")
}

// matchSegments matches pre-split segments. It returns ok=false on a length or
// literal mismatch; params is empty for a literal-only pattern.
func matchSegments(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		return nil, false
	}
	var params map[string]string
	for i, pp := range pattern {
		if strings.HasPrefix(pp, ":") {
			if params == nil {
				params = make(map[string]string)
			}
			params[strings.TrimPrefix(pp, ":")] = path[i]
		} else if pp != path[i] {
			return nil, false
		}
	}
	return params, true
}

// ---------------------------------------------------------------- responses (CONTRACTS §3.1)

// JSONResponse encodes v as the response body.
func JSONResponse(status int, v any) *pluginv1.HTTPResponse {
	b, err := json.Marshal(v)
	if err != nil {
		return ErrorResponse(http.StatusInternalServerError, "internal", "encode response: "+err.Error())
	}
	return &pluginv1.HTTPResponse{
		Status:  int32(status),
		Headers: map[string]*pluginv1.HeaderValues{"content-type": {Values: []string{"application/json; charset=utf-8"}}},
		Body:    b,
	}
}

// DataResponse returns 200 {"data": v}.
func DataResponse(v any) *pluginv1.HTTPResponse {
	return JSONResponse(http.StatusOK, map[string]any{"data": v})
}

// Page is the pagination block of list responses.
type Page struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// ListResponse returns 200 {"data": items, "page": page}.
func ListResponse(items any, page Page) *pluginv1.HTTPResponse {
	return JSONResponse(http.StatusOK, map[string]any{"data": items, "page": page})
}

// ErrorResponse returns {"error": {"code", "message"}} with status.
func ErrorResponse(status int, code, message string) *pluginv1.HTTPResponse {
	return JSONResponse(status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// FieldErrorResponse returns 400 invalid_argument with details.fields.
func FieldErrorResponse(message string, fields FieldErrors) *pluginv1.HTTPResponse {
	list := make([]map[string]string, 0, len(fields))
	for _, f := range fields {
		list = append(list, map[string]string{"field": f.GetField(), "code": f.GetCode(), "message": f.GetMessage()})
	}
	return JSONResponse(http.StatusBadRequest, map[string]any{"error": map[string]any{
		"code": "invalid_argument", "message": message, "details": map[string]any{"fields": list},
	}})
}

// ---------------------------------------------------------------- request helpers

// Query returns the first value of a query parameter.
func Query(req *pluginv1.HTTPRequest, name string) string {
	if v, ok := req.GetQuery()[name]; ok && len(v.GetValues()) > 0 {
		return v.GetValues()[0]
	}
	return ""
}

// Header returns the first value of a request header (case-insensitive).
func Header(req *pluginv1.HTTPRequest, name string) string {
	for k, v := range req.GetHeaders() {
		if strings.EqualFold(k, name) && len(v.GetValues()) > 0 {
			return v.GetValues()[0]
		}
	}
	return ""
}

// DecodeJSON unmarshals the request body into v.
func DecodeJSON(req *pluginv1.HTTPRequest, v any) error {
	if len(req.GetBody()) == 0 {
		return fmt.Errorf("empty request body")
	}
	return json.Unmarshal(req.GetBody(), v)
}

// Pagination reads page/page_size (CONTRACTS §3.1: max page_size 200).
func Pagination(req *pluginv1.HTTPRequest, defaultSize int) (page, size int) {
	page, _ = strconv.Atoi(Query(req, "page"))
	size, _ = strconv.Atoi(Query(req, "page_size"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultSize
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

// Param returns a path parameter by name, falling back to the last path segment
// when path_params is empty (a host that never filled it degrades gracefully).
func Param(req *pluginv1.HTTPRequest, name string) string {
	if v, ok := req.GetPathParams()[name]; ok {
		return v
	}
	path := strings.TrimSuffix(req.GetPath(), "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 && !strings.HasPrefix(path[i+1:], ":") {
		return path[i+1:]
	}
	return ""
}

// Unauthenticated is the 401 of a route that needs a console user and was
// called without one.
func Unauthenticated() *pluginv1.HTTPResponse {
	return ErrorResponse(http.StatusUnauthorized, "unauthenticated", "authentication required / 需要登录")
}

// Forbidden is the 403 of a caller who lacks a permission. It is the same
// whichever permission is missing: the answer does not name it.
func Forbidden() *pluginv1.HTTPResponse {
	return ErrorResponse(http.StatusForbidden, "permission_denied", "permission denied / 没有权限")
}

// RequirePermission wraps next so that it only runs for a console user who
// holds perm, asked of the host (Host.AuthzCheck: a plugin-local key such as
// "rules:manage" needs no grant, any other key needs "authz.read"). A request
// without a user is a 401, a user without the permission a 403, and a host
// that cannot answer a 503. Use it where one route needs more than the
// permission its manifest entry names; h must be the initialised host, so
// register such routes from Init. perm is asked for every row: a holder of
// only part of a scoped permission (own, some values) gets a 403, so routes
// of scoped permissions use RequestScope and RequireScope instead.
func RequirePermission(h Host, perm string, next HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		uid := req.GetCaller().GetUserId()
		if uid <= 0 {
			return Unauthenticated(), nil
		}
		if h == nil {
			return Unavailable("cannot check the permission / 无法校验权限"), nil
		}
		allowed, err := h.AuthzCheck(ctx, uid, perm)
		if err != nil {
			return Unavailable("cannot check the permission / 无法校验权限"), nil
		}
		if !allowed {
			return Forbidden(), nil
		}
		return next(ctx, req)
	}
}

// Unavailable is a 503 for "database unavailable" or similar errors.
func Unavailable(msg string) *pluginv1.HTTPResponse {
	return ErrorResponse(http.StatusServiceUnavailable, "unavailable", msg)
}

// BadRequest is a 400 with one field error.
func BadRequest(field, code, msg string) *pluginv1.HTTPResponse {
	return FieldErrorResponse("invalid request / 请求参数不正确", FieldErrors{}.Add(field, code, msg))
}

// RouteProxyTicket is the proxy ticket a console route call of an
// authorization flow carries in the conventional body field proxy_ticket
// (CONTRACTS §117). The console takes a fresh ticket of the account form's
// current proxy choice for every such call, so a route passes the one of
// this call to its fetches (ExecutionHTTPRequest.ProxyTicket) instead of
// keeping one from an earlier call: a proxy the operator changed meanwhile
// carries the next request. refused is the 400 of a body that is not JSON or
// has no ticket.
func RouteProxyTicket(req *pluginv1.HTTPRequest) (ticket string, refused *pluginv1.HTTPResponse) {
	var in struct {
		ProxyTicket string `json:"proxy_ticket"`
	}
	if body := strings.TrimSpace(string(req.GetBody())); body != "" && json.Unmarshal([]byte(body), &in) != nil {
		return "", BadRequest("body", "invalid_json", "the request body is not valid JSON / 请求体不是合法 JSON")
	}
	if strings.TrimSpace(in.ProxyTicket) == "" {
		return "", BadRequest("proxy_ticket", "required", "proxy_ticket is required: the account form's proxy choice / 缺少 proxy_ticket：账号表单的代理选择")
	}
	return in.ProxyTicket, nil
}
