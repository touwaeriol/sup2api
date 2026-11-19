package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Rewrites in the format of the request parameter override (CONTRACTS §84)
// run before Execute (design §4.5): the gateway's built-in override
// (claude_max_tokens.go), then the account's own override. The plugin is
// handed the result, with meta.model the model the override settled on, and
// builds the final bytes from it. The override's header decisions apply to
// the relayed request between the plugin's default and authoritative headers
// (upstreamRequest). The account's header override (CONTRACTS §108) is the
// initial header state of its parameter override, so the account override's
// header decisions are both of theirs.

// paramOverrideState is what the rewrites decided for the current attempt:
// the headers upstreamRequest applies and, when a rewrite changed the body's
// model, that upstream model.
type paramOverrideState struct {
	// headers are the account's header override and parameter override's,
	// applied after the passthrough.
	headers core.HeaderOverride
	model   string
}

// overrideRun runs one rewrite on a body.
type overrideRun func(body []byte, in *core.ParamOverrideContext) (core.ParamOverrideResult, error)

// overrideBody runs a rewrite on the upstream body of attempt n whose
// upstream model so far is upModel. When the rewrite itself changes the
// body's model, that
// model goes upstream and is recorded; billing keeps the client's. It
// returns the body, the upstream model and the header decisions, or the
// attempt's end; what names the rewrite in errors.
func (c *call) overrideBody(ctx context.Context, acc *core.Account, rt *typeRoute, body []byte, upModel string, n int, run overrideRun, what string) ([]byte, string, core.HeaderOverride, *attemptResult) {
	out, err := run(body, c.paramOverrideContext(acc, rt, upModel, n))
	if err != nil {
		return nil, "", core.HeaderOverride{}, c.paramOverrideFailure(ctx, acc, what, err)
	}
	if rt.modelPath != "" {
		m := gjson.GetBytes(out.Body, rt.modelPath)
		if m.Type == gjson.String && m.Str != upModel && m.Str != gjson.GetBytes(body, rt.modelPath).Str {
			upModel = m.Str
			c.paramOverride.model = upModel
			c.rec.UpstreamModel, c.upstreamPrimaryModel = upModel, upModel
		}
	}
	return out.Body, upModel, out.Headers, nil
}

// rewriteBody runs the rewrites of an attempt's body before Execute: the
// built-in override, then the account's override.
func (c *call) rewriteBody(ctx context.Context, acc *core.Account, rt *typeRoute, body []byte, upModel string, n int) ([]byte, string, *attemptResult) {
	c.paramOverride = paramOverrideState{}
	body, upModel, failure := c.overrideBuiltin(ctx, acc, rt, body, upModel, n)
	if failure != nil {
		return nil, "", failure
	}
	return c.overrideAccount(ctx, acc, rt, body, upModel, n)
}

// overrideBuiltin runs the gateway's built-in override of the route's
// upstream protocol on body. It sets no headers.
func (c *call) overrideBuiltin(ctx context.Context, acc *core.Account, rt *typeRoute, body []byte, upModel string, n int) ([]byte, string, *attemptResult) {
	p := c.gw.ClaudeMaxTokens.overrideFor(rt.upstream)
	if p == nil {
		return body, upModel, nil
	}
	body, upModel, _, failure := c.overrideBody(ctx, acc, rt, body, upModel, n, p.Apply, builtinOverride)
	return body, upModel, failure
}

// overrideAccount runs the account's override on body, from what its header
// override decided for the client's headers; the header decisions of both
// are kept for the upstream request (upstreamRequest, and the WebSocket
// handshake).
func (c *call) overrideAccount(ctx context.Context, acc *core.Account, rt *typeRoute, body []byte, upModel string, n int) ([]byte, string, *attemptResult) {
	if acc == nil || (acc.ParamOverride == nil && acc.HeaderOverride == nil) {
		return body, upModel, nil
	}
	run := func(body []byte, in *core.ParamOverrideContext) (out core.ParamOverrideResult, err error) {
		if in.InitialHeaders, err = accountHeaders(acc, in.Headers); err != nil {
			return out, err
		}
		return acc.ParamOverride.Apply(body, in)
	}
	var failure *attemptResult
	body, upModel, c.paramOverride.headers, failure = c.overrideBody(ctx, acc, rt, body, upModel, n, run, accountOverride)
	return body, upModel, failure
}

// accountOverride, accountHeaderOverride and builtinOverride name the
// overrides in errors.
const (
	accountOverride       = "account request parameter override"
	accountHeaderOverride = "account request header override"
	builtinOverride       = "built-in request parameter override"
)

// accountHeaders is what the account's header override (CONTRACTS §108)
// decides for the client headers: the initial header state of its parameter
// override. A stored override that no longer compiles
// (or a client value that cannot be sent) is an error.
func accountHeaders(acc *core.Account, client http.Header) (core.HeaderOverride, error) {
	h, err := acc.HeaderOverride.Resolve(client)
	if err != nil {
		return h, fmt.Errorf("%s: %w", accountHeaderOverride, err)
	}
	return h, nil
}

// paramOverrideContext is the context the override's conditions read.
func (c *call) paramOverrideContext(acc *core.Account, rt *typeRoute, upModel string, n int) *core.ParamOverrideContext {
	in := &core.ParamOverrideContext{Model: upModel, OriginalModel: c.model, RetryIndex: n, AccountID: acc.ID, Stream: c.stream || c.forcesStream(rt)}
	if c.c != nil && c.c.Request != nil {
		in.RequestPath, in.Headers = c.c.Request.URL.Path, c.c.Request.Header
	}
	if c.principal != nil {
		in.UserID, in.Group = c.principal.UserID, c.principal.Group.Name
	}
	return in
}

// paramOverrideFailure ends an attempt a rewrite stopped. A return_error
// operation answers the client (skip_retry) or fails over; an operation that
// could not run fails over. Neither blames the account.
func (c *call) paramOverrideFailure(ctx context.Context, acc *core.Account, what string, err error) *attemptResult {
	var returned *core.ParamOverrideError
	if errors.As(err, &returned) {
		kind := attemptFailover
		if returned.SkipRetry {
			kind = attemptReturn
		}
		return &attemptResult{kind: kind, err: &gwError{Status: returned.Status, Code: returned.Code, Type: returned.Type,
			Message: returned.Message, RecordType: errTypeInvalidRequest, NeutralReason: "param_override"}}
	}
	slog.WarnContext(ctx, "gateway: "+what+" failed", "request_id", c.rid, "account", acc.ID, "err", err)
	return &attemptResult{kind: attemptFailover, err: &gwError{Status: http.StatusInternalServerError, Code: "param_override_failed",
		Message: what + " failed: " + err.Error(), RecordType: errTypeInternal, NeutralReason: "param_override"}}
}

// upstreamModel is the model attempt sends upstream: a rewrite's when one
// changed the body's model, else the account's mapping of the client's.
func (c *call) upstreamModel(acc *core.Account) string {
	if c.paramOverride.model != "" {
		return c.paramOverride.model
	}
	return acc.MapModel(c.model)
}
