package codex

import (
	"bytes"
	"encoding/json"
	"net/http"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// Request and answer helpers of the routes (the authorization routes in
// oauth.go, the reset routes in resetcredits.go and resetcache.go).

// decodeOptional reads an optional JSON body ("" or {} are both fine).
func decodeOptional(req *pluginv1.HTTPRequest, v any) error {
	if len(bytes.TrimSpace(req.GetBody())) == 0 {
		return nil
	}
	return json.Unmarshal(req.GetBody(), v)
}

// errInvalidJSON and errNoSession are malformed route requests
// (pluginsdk.BadRequest: 400 invalid_argument with details.fields).
func errInvalidJSON() *pluginv1.HTTPResponse {
	return pluginsdk.BadRequest("body", "invalid_json", "the request body is not valid JSON / 请求体不是合法 JSON")
}

func errNoSession() *pluginv1.HTTPResponse {
	return pluginsdk.BadRequest("session_id", "required", "session_id is required / 缺少 session_id")
}

// errNotReady is a 503 for a route called before Init.
func errNotReady() *pluginv1.HTTPResponse {
	return pluginsdk.Unavailable("the plugin is not ready / 插件尚未就绪: " + pluginsdk.ErrHostNotReady.Error())
}

// flowError is a 400 refusal of the authorization flow with its own code.
func flowError(code, msg string) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusBadRequest, code, msg)
}

func upstreamError(code, msg string) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusBadGateway, code, msg)
}
