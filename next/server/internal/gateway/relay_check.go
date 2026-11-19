package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
)

// validate is the gateway's check of a relay, before anything is sent: what
// the runtime cannot judge (it has refused a missing URL and the
// transport's header names already). It returns the URL to send to.
//
// The body is not inspected: the plugin's bytes are final and the core does
// not parse them again (the SDK conformance suite holds plugins to the
// model, stream flag and usage of the body they were handed). What stays is
// what only the core can enforce at run time: a target host the plugin
// declared or the account names (allowHost), the SSRF guard on the target,
// headers the transport can carry, a response framing this core decodes,
// and, for a type declaring model availability, the upstream model the
// plugin names.
func (x *execution) validate(ctx context.Context, req *pluginv1.UpstreamRequest) (*url.URL, *gwError) {
	c := x.c
	if e := checkRelayHeaders(req); e != nil {
		return nil, e
	}
	if e := x.checkResponseStream(req.GetResponseStream()); e != nil {
		return nil, e
	}
	if m := req.GetUpstreamModel(); m != "" && x.rt.binding.Type.ModelAvailability {
		if expected := c.upstreamModel(x.acc); m != expected {
			return nil, c.availabilityMismatch(ctx, &x.acc.AccountRef, expected, m)
		}
	}
	if x.turn && !x.opens {
		// A later turn sends its message on the open connection: its URL and
		// headers are not used.
		return nil, nil
	}
	raw, e := x.httpTarget(req)
	if e != nil {
		return nil, e
	}
	if e := x.allowHost(ctx, raw); e != nil {
		return nil, e
	}
	target, err := x.checkURL(ctx, raw)
	if err != nil {
		slog.WarnContext(ctx, "gateway: upstream url rejected", "request_id", c.rid, "plugin", x.rt.binding.Plugin.Key, "account", x.acc.ID, "err", err)
		e := fromCore(core.ErrUnavailable.WithMessage("upstream address rejected"), errTypeInternal)
		e.NeutralReason = "upstream_address_rejected" // the target, not the account's health
		return nil, e
	}
	return target, nil
}

// allowHost refuses a relay whose host is neither one of the plugin's
// externalServices (or a subdomain of one) nor the host of one of the
// account's upstream addresses (CONTRACTS §112.17), or that the
// administrator's plugin network access refuses (§113): the core sends the
// account's credentials only where the operator reviewed at install or
// entered on the account, and a plugin reaches only the hosts the
// administrator lets plugins reach (core.CheckPluginUpstream). The refusal
// is the plugin's, not the account's: the attempt fails over and nothing is
// sent.
func (x *execution) allowHost(ctx context.Context, raw string) *gwError {
	key, acct := x.rt.binding.Plugin.Key, x.acct
	ce := core.CheckPluginUpstream(ctx, x.c.g.d.NetworkAccess, key, x.rt.binding.Plugin.Manifest, raw,
		acct.GetType(), acct.GetSettingsJson(), acct.GetCredentialsJson())
	if ce == nil {
		return nil
	}
	reason, _ := ce.Details["reason"].(string)
	var host string
	if u, err := url.Parse(raw); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	slog.WarnContext(ctx, "gateway: upstream host not allowed", "request_id", x.c.rid, "plugin", key, "account", x.acc.ID,
		"host", host, "reason", reason)
	e := relayRefused(ce.Message)
	e.NeutralReason = reason
	return e
}

// httpTarget is the http(s) address req goes to. A WebSocket handshake is a
// GET to a ws(s) or http(s) address, judged as the http(s) one it really
// goes to.
func (x *execution) httpTarget(req *pluginv1.UpstreamRequest) (string, *gwError) {
	raw := strings.TrimSpace(req.GetUrl())
	if !x.turn {
		return raw, nil
	}
	if m := strings.ToUpper(req.GetMethod()); m != "" && m != http.MethodGet {
		return "", relayRefused("a websocket handshake is a GET, the plugin relayed " + m)
	}
	scheme, rest, _ := strings.Cut(raw, "://")
	switch strings.ToLower(scheme) {
	case "wss", "https":
		return "https://" + rest, nil
	case "ws", "http":
		return "http://" + rest, nil
	}
	return "", relayRefused("the upstream websocket url must be ws(s):// or http(s)://")
}

// checkResponseStream accepts a response framing this core decodes, and
// none on a WebSocket turn.
func (x *execution) checkResponseStream(name string) *gwError {
	switch {
	case name == "":
		return nil
	case x.turn:
		return relayRefused("response stream formats are not supported on WebSocket routes")
	}
	if _, ok := responseStreams[name]; !ok {
		return relayRefused("unknown response stream format " + strconvQuote(name))
	}
	return nil
}

// checkRelayHeaders refuses a header the transport could not send: an
// invalid method, name or value would fail the exchange after it started,
// as if the upstream had.
func checkRelayHeaders(req *pluginv1.UpstreamRequest) *gwError {
	if m := req.GetMethod(); m != "" && !httpguts.ValidHeaderFieldName(m) {
		return relayRefused("invalid method " + strconvQuote(m))
	}
	for _, headers := range []map[string]string{req.GetDefaultHeaders(), req.GetHeaders()} {
		for k, v := range headers {
			if !execfetch.ValidHeader(k, v) {
				return relayRefused("invalid header " + strconvQuote(k))
			}
		}
	}
	return nil
}

// relayRefused is a relay the plugin built wrongly: the attempt fails over
// as the plugin's failure (pluginUnavailable).
func relayRefused(msg string) *gwError {
	return pluginUnavailable(core.ErrPluginUnavailable.WithMessage(msg))
}
