package gateway

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
)

// fetch is ExecutionClient.Fetch: one exchange whose answer goes
// back to the plugin (a token exchange, an upload, a preflight). It is the
// attempt's account's request like the relay - its proxy, the SSRF guard,
// no redirect, the environment's timeouts (no total timeout when
// relay_timeout_seconds is unset, as for the relayed request) - but not the
// client's request: the operator's
// passthrough and overrides do not apply. The runtime has already checked
// the token and the host; execfetch applies what every fetch shares with
// Monitor's. A transport failure is an answer with status
// 0, so the plugin can classify it.
func (x *execution) fetch(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	c := x.c
	header, total := c.upstreamTimeouts(false)
	out, err := execfetch.Do(ctx, in, execfetch.Binding{
		CheckURL:      x.checkURL,
		Client:        func(context.Context) (*http.Client, error) { return x.egress.Client, nil },
		HeaderTimeout: header, TotalTimeout: total,
	})
	if err == nil && out.GetTransportError() == "" {
		u, _ := url.Parse(in.GetUrl())
		slog.InfoContext(ctx, "gateway: plugin fetch", "request_id", c.rid, "plugin", x.rt.binding.Plugin.Key, "account", x.acc.ID,
			"method", cmp.Or(strings.ToUpper(in.GetMethod()), http.MethodGet), "host", u.Host, "path", u.EscapedPath(), "status", out.GetStatus(), "bytes", len(out.GetBody()))
	}
	return out, err
}
