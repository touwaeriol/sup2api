package gateway

import (
	"context"
	"net/url"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
)

// The SSRF guard itself lives in internal/netguard: the gateway is not the
// only place the core sends a request a plugin described (the reconcile loop
// of CONTRACTS §25.4 is the other), and two copies of this check would be one
// copy too many.

// checkUpstream validates the URL of a plugin's relay, handshake or fetch:
// http(s), a host, and (unless AllowPrivateUpstream) a public target. A
// direct request checks the addresses the name resolves to; a proxied one
// (the account has a proxy) is resolved by the proxy, so only a literal
// private address is refused and nothing is resolved here
// (netguard.CheckHostVia).
func (g *Gateway) checkUpstream(ctx context.Context, raw string, proxied bool) (*url.URL, error) {
	return netguard.CheckURLVia(ctx, raw, g.allowPrivate, proxied, g.lookupIP)
}

// checkURL is checkUpstream for a request of x, made through its account's
// proxy when it has one (x.egress).
func (x *execution) checkURL(ctx context.Context, raw string) (*url.URL, error) {
	return x.c.g.checkUpstream(ctx, raw, x.egress.Proxied)
}
