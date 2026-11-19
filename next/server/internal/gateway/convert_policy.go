package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
)

// Lazy conversion and loss policies (CONVERT-MATRIX §2.4, CONTRACTS §88).
// A converting route converts the request the first time pick considers an
// account of its type; the account's effective loss policy then admits or
// refuses the losses. A refused account is skipped like one of a type that
// cannot serve the request: it takes no slot, counts no attempt and
// triggers no failover. When every candidate is refused the request ends
// with 400 unsupported_conversion.

// Caps of usage_logs.conversion (CONVERT-MATRIX §7).
const (
	maxTracePaths     = 40
	maxTracePathBytes = 160
	maxTraceRejected  = 5
)

// conversionState is the request's side of the conversions.
type conversionState struct {
	opts *convert.Options
	// rejected are the account types that refused the request, for the
	// trace; refusal is the first reason a converting route could not serve
	// it, returned when no account could.
	rejected []core.ConversionRejection
	refusal  string
	// served is the route of the last attempt and policy the effective loss
	// policy of its account.
	served *typeRoute
	policy string
}

// preparedConversion is the request converted for one route, once per body
// version.
type preparedConversion struct {
	done    bool
	version uint64
	// unbound is the registry's converter the route's conv was bound from.
	unbound convert.Converter
	// err: the request does not convert, the type cannot serve it.
	err    error
	losses []convert.Loss // request losses
	// verdicts caches the policies judged so far: the loss refusing the
	// policy, nil when the policy admits the request.
	verdicts map[string]*convert.Loss
}

// usable reports whether ref can still be tried: its type has a route and,
// when converting, the request converts to the route's protocol and the
// account's loss policy accepts the losses.
func (c *call) usable(ctx context.Context, ref *core.AccountRef) bool {
	rt := c.route(ref)
	if rt == nil {
		return false
	}
	if rt.conv == nil {
		return true
	}
	return c.prepareRoute(ctx, rt) && c.admits(rt, c.lossPolicy(ref))
}

// routed reports whether ref has a route not already known to be unable to
// serve the request, without converting anything.
func (c *call) routed(ref *core.AccountRef) bool {
	rt := c.route(ref)
	return rt != nil && (!rt.prepared.done || rt.prepared.err == nil)
}

// lossPolicy is the effective conversion loss policy of ref in the
// request's group.
func (c *call) lossPolicy(ref *core.AccountRef) string {
	return core.EffectiveLossPolicy(ref.ConversionLossPolicy, c.principal.Group.ConversionLossPolicy)
}

// prepareRoute converts the request for a converting route, unless already
// done for the current body. It reports whether the request converts.
func (c *call) prepareRoute(ctx context.Context, rt *typeRoute) bool {
	p := &rt.prepared
	if p.done && p.version == c.bodyVersion {
		return p.err == nil
	}
	if p.unbound == nil {
		p.unbound = rt.conv
	}
	*p = preparedConversion{done: true, version: c.bodyVersion, unbound: p.unbound}
	opt := c.convertOptions(ctx)
	opt.AdaptModel = rt.adaptModel
	bound, body, err := p.unbound.Prepare(c.body, opt)
	if err == nil && (bound == nil || bound.From() != p.unbound.From() || bound.To() != p.unbound.To()) {
		err = fmt.Errorf("converter %s -> %s returned a different pair", p.unbound.From(), p.unbound.To())
	}
	if err != nil {
		rt.conv, rt.body, p.err = p.unbound, nil, err
		c.refuseConversion(ctx, rt, err)
		return false
	}
	rt.conv, rt.body, p.losses = bound, body, convert.Losses(bound)
	if len(p.losses) > 0 && slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.DebugContext(ctx, "gateway: request conversion dropped fields", "from", c.ep.Protocol, "to", rt.upstream,
			"fields", lossStrings(p.losses))
	}
	return true
}

// convertOptions are the request facts every conversion of the request
// shares, built once; prepareRoute adds the route's own (AdaptModel).
func (c *call) convertOptions(ctx context.Context) convert.Options {
	if c.conversion.opts == nil {
		c.conversion.opts = &convert.Options{Ctx: ctx, Model: c.model, Stream: c.stream, Fetch: c.mediaFetcher(),
			ClaudeDefaultMaxTokens: c.gw.ClaudeMaxTokens.maxTokens}
	}
	return *c.conversion.opts
}

// admits judges the route's request losses under policy: allow admits
// everything, safe refuses a tool loss, strict any loss.
func (c *call) admits(rt *typeRoute, policy string) bool {
	p := &rt.prepared
	loss, judged := p.verdicts[policy]
	if judged {
		return loss == nil
	}
	loss = refusingLoss(p.losses, policy)
	if p.verdicts == nil {
		p.verdicts = map[string]*convert.Loss{}
	}
	p.verdicts[policy] = loss
	if loss != nil {
		path := tracePath(*loss)
		c.refuse(rt, core.ConversionRejection{Policy: policy, Category: string(loss.Category), Path: path},
			fmt.Sprintf("%s would be lost (%s, policy %s)", path, loss.Category, policy))
	}
	return loss == nil
}

// refusingLoss is the first loss policy refuses, nil when it admits them.
func refusingLoss(losses []convert.Loss, policy string) *convert.Loss {
	for i := range losses {
		if policy == core.LossPolicyStrict || policy == core.LossPolicySafe && losses[i].Category == convert.LossTool {
			return &losses[i]
		}
	}
	return nil
}

// refuseConversion records a request the route cannot convert. A
// RequestError is the request's fault (it cannot be expressed); anything
// else is the converter's and is logged as such, but either way the type
// cannot serve the request and the error is the reason given.
func (c *call) refuseConversion(ctx context.Context, rt *typeRoute, err error) {
	level := slog.LevelInfo
	if !errors.As(err, new(*convert.RequestError)) {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "gateway: request conversion failed", "from", c.ep.Protocol, "to", rt.upstream, "err", err)
	c.refuse(rt, core.ConversionRejection{Error: err.Error()}, err.Error())
}

// refuse records a refusal of the route for the trace and keeps the first
// reason for the client.
func (c *call) refuse(rt *typeRoute, rej core.ConversionRejection, reason string) {
	if c.conversion.refusal == "" {
		c.conversion.refusal = "request cannot be converted to " + rt.upstream + ": " + reason
	}
	if len(c.conversion.rejected) >= maxTraceRejected {
		return
	}
	rej.Type = rt.binding.Plugin.Key + "/" + rt.binding.Type.ID
	rej.Path = truncateUTF8(rej.Path, maxTracePathBytes)
	rej.Error = truncateUTF8(rej.Error, maxTracePathBytes)
	c.conversion.rejected = append(c.conversion.rejected, rej)
}

// unsupportedConversion is the answer to a request no account could take
// because of its conversion; nil when nothing was refused.
func (c *call) unsupportedConversion() *gwError {
	if c.conversion.refusal == "" {
		return nil
	}
	return &gwError{Status: http.StatusBadRequest, Code: "unsupported_conversion", Message: c.conversion.refusal,
		RecordType: errTypeInvalidRequest}
}

// serveRoute notes the route and account of an attempt for the trace.
func (c *call) serveRoute(rt *typeRoute, ref *core.AccountRef) {
	c.conversion.served, c.conversion.policy = rt, c.lossPolicy(ref)
}

// conversionTrace is usage_logs.conversion: nil for a native request no
// type refused.
func (c *call) conversionTrace() *core.ConversionTrace {
	s := &c.conversion
	rt := s.served
	converted := rt != nil && rt.grade != convert.GradeNative
	if !converted && len(s.rejected) == 0 {
		return nil
	}
	t := &core.ConversionTrace{V: 1, Rejected: s.rejected}
	if rt == nil {
		return t
	}
	t.Grade = rt.grade.String()
	if !converted {
		return t
	}
	t.Hops = rt.hops
	if !rt.local {
		t.Policy = s.policy
	}
	for i, l := range convert.Losses(rt.conv) {
		if i >= maxTracePaths {
			t.Omitted++
			continue
		}
		dst := &t.Lost
		if strings.HasPrefix(l.Path, "response.") {
			dst = &t.ResponseLost
		}
		if *dst == nil {
			*dst = map[string][]string{}
		}
		cat := string(l.Category)
		(*dst)[cat] = append((*dst)[cat], truncateUTF8(tracePath(l), maxTracePathBytes))
	}
	return t
}

// tracePath is a loss path as the trace shows it: a second hop's path
// carries its hop ("1:tools[0]").
func tracePath(l convert.Loss) string {
	if l.Hop == 0 {
		return l.Path
	}
	return fmt.Sprintf("%d:%s", l.Hop, l.Path)
}

func lossStrings(losses []convert.Loss) []string {
	out := make([]string, len(losses))
	for i, l := range losses {
		out[i] = string(l.Category) + ":" + tracePath(l)
	}
	return out
}
