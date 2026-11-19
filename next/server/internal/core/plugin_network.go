package core

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// PluginNetworkAccess (owner: plugin/egress) is the administrator's plugin
// network access (CONTRACTS §113): the hosts every plugin may or may not
// reach. It rules the plugin's own connections through the egress tunnel and
// every request the core sends for a plugin to an address the plugin chose:
// a plugin has no network of its own, so both go out through the core.
type PluginNetworkAccess interface {
	// PluginRequestRefusal is why the setting does not let the core send a
	// request of plugin pluginKey to target (an http(s) URL), "" when it
	// does. The decision is the tunnel's (a cached copy of the setting, no
	// database read per request); a refusal is also written to the plugin's
	// egress log as a denied row, where the tunnel's refusals are.
	PluginRequestRefusal(ctx context.Context, pluginKey string, target *url.URL) string
}

// Why the core sent nothing for a plugin: the request's host is neither
// declared nor configured (CONTRACTS §112.17), or the administrator's plugin
// network access refuses it (§113). The gateway records them as the
// NeutralReason of the attempt; an *Error carries them as details.reason.
const (
	ReasonUpstreamHostNotAllowed = "upstream_host_not_allowed"
	ReasonUpstreamHostBlocked    = "upstream_host_blocked"
)

// CheckPluginUpstream decides a request the core sends for plugin pluginKey
// (manifest m) with an account of type accountType to the http(s) URL raw:
// the relay, the fetch, the proxy-ticket fetch and SendAccountRequest. The
// host must be one m allows for the account (manifest.UpstreamHostAllowed
// with accountDocs, the account's settings and credentials JSON), and the
// administrator's plugin network access must let the plugin reach it
// (CheckPluginNetwork). nil when both pass; otherwise ErrPermissionDenied
// with details.reason ReasonUpstreamHostNotAllowed or
// ReasonUpstreamHostBlocked.
func CheckPluginUpstream(ctx context.Context, access PluginNetworkAccess, pluginKey string, m *manifest.Manifest,
	raw, accountType string, accountDocs ...string) *Error {
	u, err := url.Parse(raw)
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if !m.UpstreamHostAllowed(host, accountType, accountDocs...) {
		return refusedUpstream(ReasonUpstreamHostNotAllowed, "plugin "+pluginKey+": host "+strconv.Quote(host)+
			" is neither in the manifest's externalServices nor an upstream address of the account")
	}
	return CheckPluginNetwork(ctx, access, pluginKey, u)
}

// CheckPluginNetwork is the administrator's half alone, for the requests a
// plugin describes that no manifest rule covers (quota, refresh, account
// test, model list): nil when access lets plugin pluginKey reach target, or
// when there is no access (a module built without it, unit tests).
func CheckPluginNetwork(ctx context.Context, access PluginNetworkAccess, pluginKey string, target *url.URL) *Error {
	if access == nil {
		return nil
	}
	if msg := access.PluginRequestRefusal(ctx, pluginKey, target); msg != "" {
		return refusedUpstream(ReasonUpstreamHostBlocked, "plugin "+pluginKey+": "+msg)
	}
	return nil
}

func refusedUpstream(reason, msg string) *Error {
	return ErrPermissionDenied.WithMessage(msg).WithDetails(map[string]any{"reason": reason})
}
