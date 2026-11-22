package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// bodyLimit is the largest client request body (or WebSocket message): the
// request body limit of the gateway settings and nothing else.
func (c *call) bodyLimit() int64 { return c.gw.MaxRequestBodyBytes() }

func (c *call) readBody() *gwError {
	if c.c.Request.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(c.c.Writer, c.c.Request.Body, c.bodyLimit()))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return &gwError{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large",
					Message: "request body is too large", RecordType: errTypeInvalidRequest}
			}
			if c.c.Request.Context().Err() != nil {
				return &gwError{Status: statusClientClosed, Message: "client canceled", RecordType: errTypeClientCanceled}
			}
			return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
				Message: "failed to read request body", RecordType: errTypeInvalidRequest}
		}
		c.body = body
	}
	return c.checkBodyJSON()
}

// checkBodyJSON accepts an empty body or one JSON text.
//
// JSON text is UTF-8 (RFC 8259 §8.1) and gjson's validator does not check
// that, so a body with a stray byte inside a string would pass here and
// then poison every proto string it is copied into - RequestMeta.model
// from request.modelPath, the request fields handed to plugins, price
// params - and proto.Marshal refuses invalid UTF-8,
// which makes every plugin call of the request fail: one curl, a stable
// 5xx, and a usage row PostgreSQL refuses as well (CONTRACTS §25.1). It is
// not valid JSON; say so, once, here.
//
// The shape scan runs before gjson: gjson's validator recurses, and a deeply
// nested body would overflow the stack (fatal, not a recoverable panic). A
// valid body with a repeated object key is refused too (CONTRACTS §25.9), on
// every endpoint: hooks read the first copy with gjson, the upstream and the
// protocol converters read the last. The body is never rewritten.
func (c *call) checkBodyJSON() *gwError {
	if len(c.body) == 0 {
		return nil
	}
	if !utf8.Valid(c.body) {
		return errBodyNotJSON
	}
	shape := scanJSONShape(c.body)
	if shape.tooDeep {
		return jsonShapeError(shape, "request body")
	}
	if !gjson.ValidBytes(c.body) {
		return errBodyNotJSON
	}
	if e := jsonShapeError(shape, "request body"); e != nil {
		return e
	}
	return nil
}

var errBodyNotJSON = &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
	Message: "request body is not valid JSON", RecordType: errTypeInvalidRequest}

// checkModel (re)reads model and stream and applies the group allowlist.
// The model comes from the path parameter request.modelParam when declared
// (e.g. Gemini's /v1beta/models/:model:generateContent), from the body at
// request.modelPath, or from the platform plugin when the endpoint declares
// request.modelSource "plugin" (resolveModelFromPlugin).
// request.stream marks endpoints that always stream.
//
// It runs before hooks and again after them, and it must stay there: the
// model decides hook matching, the group allowlist, pricing and the `models`
// filter on the candidate accounts, so every one of those needs it already
// resolved.
func (c *call) checkModel(ctx context.Context) *gwError {
	if e := c.readModel(ctx); e != nil {
		return e
	}
	c.readStream()
	c.rec.Model = c.model
	c.rec.Stream = c.stream
	if !c.principal.Group.AllowsModel(c.model) {
		return fromCore(core.ErrModelNotFound.WithDetails(map[string]any{"model": c.model}), errTypeModelNotAllowed)
	}
	// The check below walks the whole body. Hooks replace the body only
	// through setBody, which bumps bodyVersion; an unchanged version
	// therefore left the input of the first pass identical, and repeating it
	// would only repeat the scan.
	if c.scanned && c.scannedBodyVersion == c.bodyVersion {
		return nil
	}
	if err := c.checkReferencedModels(); err != nil {
		return err
	}
	c.scanned, c.scannedBodyVersion = true, c.bodyVersion
	return nil
}

// readModel reads the model from where the endpoint declares it.
func (c *call) readModel(ctx context.Context) *gwError {
	req := c.ep.Request
	switch {
	case c.ep.TaskQuery():
		if e := c.resolveTask(ctx); e != nil {
			return e
		}
	case req.ModelSource == manifest.ModelSourcePlugin:
		if e := c.resolveModelFromPlugin(ctx); e != nil {
			return e
		}
	case req.ModelParam != "":
		c.model = strings.TrimSpace(c.params[req.ModelParam])
	case req.ModelPath != "":
		c.model = gjson.GetBytes(c.body, req.ModelPath).String()
	}
	if (req.ModelParam != "" || req.ModelPath != "") && c.model == "" {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model is required", RecordType: errTypeInvalidRequest}
	}
	// A model the client named (path, body or the plugin reading the body)
	// reaches account selection, plugins and the usage record: refuse what
	// no model id looks like before any of them sees it (CONTRACTS §15.2).
	if !c.ep.TaskQuery() && c.model != "" && !validModelName(c.model) {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model must be a model identifier without whitespace or control characters", RecordType: errTypeInvalidRequest}
	}
	return nil
}

// readStream reads whether the request streams. request.stream is an
// endpoint-level fact ("this endpoint only streams") and always wins,
// including over a plugin: a plugin answering false would have the host
// read an SSE response as JSON. Otherwise the plugin's answer replaces
// request.streamPath, which such an endpoint may not declare (manifest
// validation).
func (c *call) readStream() {
	req := c.ep.Request
	switch {
	case req.Stream:
		c.stream = true
	case req.ModelSource == manifest.ModelSourcePlugin:
		// c.stream was set by resolveModelFromPlugin.
	case req.StreamPath != "":
		c.stream = gjson.GetBytes(c.body, req.StreamPath).Bool()
	}
}

// resolveModelFromPlugin asks PlatformService.ResolveModel of the plugin
// declaring the endpoint's platform for the model and the stream flag
// (CONTRACTS §25.2). Endpoints that do not declare request.modelSource
// "plugin" never reach it, so nothing existing pays for this.
//
// Every failure is a 400 "model is required" — a timeout, a transport error,
// an UNIMPLEMENTED from a plugin that never wrote the method, an empty
// answer. Without a model the host cannot resolve a price, apply the group
// allowlist or filter the candidate accounts, so letting the request through
// would serve it for free.
//
// The one exception is a nil Client: the plugin declared the platform without
// declaring platform.adapter.v1 (legal per §13, and rejected at install time
// since B, but a package installed earlier or a built-in platform can still
// get here). That is a host-side misconfiguration, not a client mistake, so
// it is a 500 with a log naming the plugin.
func (c *call) resolveModelFromPlugin(ctx context.Context) *gwError {
	if c.modelResolved {
		return nil // same body as the last call: the answer cannot have changed
	}
	pb, ok := c.gen.Platform(c.platform)
	if !ok || pb.Client == nil {
		slog.ErrorContext(ctx, "gateway: endpoint declares modelSource \"plugin\" but its platform has no plugin client",
			"platform", c.platform, "plugin", c.plugin.Key, "endpoint", c.ep.Path, "protocol", c.ep.Protocol,
			"builtin", pb.Builtin, "found", ok)
		return fromCore(core.ErrInternal.WithMessage("the platform of this endpoint cannot resolve models"), errTypeInternal)
	}
	rctx, cancel := context.WithTimeout(ctx, c.gw.hotpathTimeout())
	resp, err := pb.Client.ResolveModel(rctx, &pluginv1.ResolveModelRequest{
		Meta: c.meta(), InboundHeaders: c.passHeaders(c.pf.PassHeaders)})
	cancel()
	if err != nil || resp.GetModel() == "" {
		slog.InfoContext(ctx, "gateway: resolve model failed", "plugin", pb.Plugin.Key,
			"platform", c.platform, "protocol", c.ep.Protocol, "err", err)
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model is required", RecordType: errTypeInvalidRequest}
	}
	c.model, c.stream, c.modelResolved = resp.GetModel(), resp.GetStream(), true
	return nil
}
