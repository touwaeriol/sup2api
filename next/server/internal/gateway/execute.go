package gateway

// Plugin execution, the one path of a gateway request (core
// ports_execution.go, docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md §4).
// Every attempt calls PlatformService.Execute of the account type's plugin
// with the whole body as the core rewrote it; the plugin builds the final
// upstream request and hands it to the client built here
// (core.ExecutionClient). Two forms:
//
//   - Without waiting (every endpoint whose usage the core reads): the
//     relay is only checked while the plugin runs. send makes the request
//     after Execute returned host_usage, forward relays and meters the
//     answer and submit settles it, like any request; nothing is written per
//     execution and the plugin plays no part any more, so it may crash or
//     restart meanwhile. A plugin failing before that leaves nothing sent:
//     the attempt fails over without blaming the account.
//   - Waiting (an endpoint whose usage the plugin reads, a task
//     submission): Relay sends now, inside the plugin's call, and hands the
//     observation back (waited.go); the facts Execute returns are
//     committed through core.ExecutionRecords before the buffered answer is
//     published.

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// execution is one Execute call: one attempt on one account, or one turn of
// a WebSocket session.
type execution struct {
	c   *call
	rt  *typeRoute
	acc *core.Account
	// acct is the account the plugin is handed: its proxy is egress's.
	acct *pluginv1.Account
	// egress is the account's way out as it stood when the call began
	// (AccountDirectory.Egress): the relay, the fetches and the checks of
	// their URLs all go by it.
	egress core.Egress
	// body is what the plugin was handed: the request in the upstream
	// protocol after the core's rewrites. The usage rules read the
	// requested operation from it, never from the plugin's bytes.
	body []byte
	// network is the attempt's context (the client and the account's slot
	// lease); a waited relay sends under it.
	network context.Context
	// turn is a WebSocket turn; opens says it opens the upstream
	// connection, the only turn whose URL and headers are used.
	turn, opens bool

	// checked is the last request check accepted and target its parsed URL,
	// which send uses; refused is why check refused the last one, the
	// attempt's failure when Execute then fails.
	checked *pluginv1.UpstreamRequest
	target  *url.URL
	refused *gwError
	// waited is the exchange of a relay that waits (waited.go).
	waited *waitedRelay
	// detach stops the client's departure from cancelling the Execute call
	// of a relay that waits; it reports false when the client has already
	// left (run).
	detach func() bool
}

// errExecutionAborted cuts the upstream exchange of a relay that waits when
// the plugin gave up its call (killed, drained, timed out) while the client
// is still there.
var errExecutionAborted = errors.New("plugin execution aborted")

// newExecution is an Execute call on acc with body, bound to the account's
// egress as it stands now; the plugin is handed the account with that
// egress's proxy (CONTRACTS §124). Without an egress (the proxy disabled or
// gone) there is no call: the request could not go out, and the plugin is
// never handed the account without its proxy. The attempt then fails over.
func (c *call) newExecution(ctx context.Context, rt *typeRoute, acc *core.Account, body []byte) (*execution, *attemptResult) {
	eg, err := c.g.d.Accounts.Egress(ctx, acc.ID)
	if err != nil {
		return nil, &attemptResult{kind: attemptFailover, err: fromCore(core.ErrUnavailable.WithCause(err), errTypeInternal)}
	}
	return &execution{c: c, rt: rt, acc: acc, acct: acc.PluginAccount(c.platform, eg.Proxy), egress: eg, body: body, network: ctx}, nil
}

// execute runs attempt n on rt's account with body, the request the core
// rewrote for it, and upModel, the model it goes upstream with.
func (c *call) execute(ctx context.Context, rt *typeRoute, acc *core.Account, body []byte, upModel string, n int) attemptResult {
	x, failed := c.newExecution(ctx, rt, acc, body)
	if failed != nil {
		return *failed
	}
	// Whatever happens below - a commit, a close, an error, a panic - the
	// waited relay's heartbeat stops and its held-back answer is removed.
	defer func() {
		if w := x.waited; w != nil {
			w.alive.stop()
			w.release()
		}
	}()
	res, err := x.run(ctx, upModel, n)
	if w := x.waited; w != nil {
		return w.settle(ctx, res, err)
	}
	if err == nil && res.Relay != nil {
		return x.sendParked(ctx, res.Relay)
	}
	return x.unrelayed(ctx, res, err)
}

// run calls Execute under ctx, the client's: the call has no time limit of
// its own. A request that waits is cancelled by the client leaving only
// until its relay starts sending (detach): from then on the exchange
// follows the disconnect policy (its answer may be read on for billing,
// disconnect.go) and the plugin's work after the observation is not
// cancelled by the client, since its facts must still be committed. It
// then ends with the plugin's return, the upstream exchange (the
// administrator's upstream timeouts, the policy) or the plugin process.
func (x *execution) run(ctx context.Context, upModel string, n int) (core.ExecutionResult, error) {
	c, rt := x.c, x.rt
	meta := c.metaFor(rt)
	meta.Model = upModel
	in := &pluginv1.ExecuteRequest{Meta: meta, Account: x.acct, Body: x.body, InboundHeaders: c.inboundHeaders(rt, x.acc.ID, n),
		Attempt: int32(n), ExtractUsage: rt.pluginUsage && !x.turn, TaskSubmission: c.ep.TaskSubmit()}
	client := core.ExecutionClient{Check: x.check, Fetch: x.fetch}
	if in.ExtractUsage || in.TaskSubmission {
		client.Relay = x.relay
		detached, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		x.detach = context.AfterFunc(ctx, cancel)
		ctx = detached
	}
	return rt.binding.Client.Execute(ctx, in, client)
}

// sendParked sends the relay the plugin confirmed with host_usage. A task
// submission must wait: its task is registered from the facts Execute
// returns, so one relayed without them is not sent. An answered request is
// billed by the host's reading - on an endpoint whose usage the plugin was
// to read, as the plugin's fallback (hostUsageChosen) - and is unreported
// when that holds nothing to bill (endUnreported).
func (x *execution) sendParked(ctx context.Context, req *pluginv1.UpstreamRequest) attemptResult {
	c := x.c
	if c.ep.TaskSubmit() {
		return x.pluginFailed(ctx, errors.New("a task submission was relayed without waiting"))
	}
	res := c.send(ctx, x, req)
	if res.kind != attemptDone {
		return res
	}
	reading := c.readingEmpty
	if x.rt.pluginUsage {
		c.usage = nil
		reading = c.hostUsageChosen
	}
	c.endUnreported(ctx, x.acc.ID, reading())
	return res
}

// unrelayed ends an attempt for which the plugin made no relay that may be
// sent: nothing reached the upstream. A classification without a relay is
// the plugin's verdict on a fetch that failed before it, applied like an
// upstream failure; anything else is the plugin's failure, never the
// account's.
func (x *execution) unrelayed(ctx context.Context, res core.ExecutionResult, err error) attemptResult {
	c := x.c
	switch cls := res.Response.GetClassification(); {
	case ctx.Err() != nil:
		// The client left: whatever the plugin made of it (a fetch it cut,
		// classified) says nothing about the account.
		return attemptResult{kind: attemptCanceled, err: canceledErr()}
	case err == nil && cls != nil:
		return c.applyClassification(ctx, x.rt, x.acct, 0, nil, nil, firstNonEmpty(cls.GetReason(), "upstream request failed before the relay"), cls)
	case x.refused != nil:
		return attemptResult{kind: attemptFailover, err: x.refused}
	case err == nil:
		err = errors.New("Execute returned without a relay to send")
	}
	return x.pluginFailed(ctx, err)
}

// pluginFailed fails over an attempt the plugin could not serve, without
// blaming the account (pluginUnavailable).
func (x *execution) pluginFailed(ctx context.Context, err error) attemptResult {
	slog.WarnContext(ctx, "gateway: plugin execution failed", "request_id", x.c.rid, "plugin", x.rt.binding.Plugin.Key,
		"account", x.acc.ID, "err", err)
	return attemptResult{kind: attemptFailover, err: pluginUnavailable(core.ErrPluginUnavailable.WithCause(err))}
}

// pluginUnavailable is the failure of an attempt the plugin could not serve
// (an Execute error or crash, a relay the core refuses): nothing reached
// the upstream and the account is not to blame. Under a failure policy it
// moves to another account like an availability refusal, with no account
// rule run (planAvailabilityFailure); without one it fails over.
func pluginUnavailable(e *core.Error) *gwError {
	g := fromCore(e, errTypePluginUnavailable)
	g.NeutralReason = errTypePluginUnavailable
	return g
}

// check is ExecutionClient.Check: the gateway's part of validating a relay
// before it is kept or sent (relay_check.go). A refusal is the plugin's to
// correct; its text is what the plugin sees.
func (x *execution) check(ctx context.Context, req *pluginv1.UpstreamRequest) error {
	target, e := x.validate(ctx, req)
	x.checked, x.target, x.refused = req, target, e
	if e != nil {
		return errors.New(e.Message)
	}
	return nil
}

// targetOf is the URL to send req to: the one check parsed, else req's own
// checked now.
func (x *execution) targetOf(ctx context.Context, req *pluginv1.UpstreamRequest) (*url.URL, *gwError) {
	if req == x.checked && x.refused == nil {
		return x.target, nil
	}
	return x.validate(ctx, req)
}
