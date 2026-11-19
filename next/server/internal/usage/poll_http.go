package usage

import (
	"context"
	"net/http"
	"net/url"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
)

// fetchExecutionHTTP is the fetch of a Monitor call (core.ExecutionHTTP).
// The account and admission come from the already bound account, never from
// the plugin; each fetch goes out through the account's proxy as it stands
// then (AccountDirectory.Egress), and through a proxy the name is the
// proxy's to resolve (netguard.CheckHostVia). It performs one HTTP exchange,
// without redirects or retries; the runtime has already checked the token
// and the host (neither the number of fetches nor their size is bounded).
func (s *Service) fetchExecutionHTTP(ctx context.Context, in *pluginv1.ExecutionHTTPRequest, accountID int64, admit func(context.Context) error) (*pluginv1.ExecutionHTTPResponse, error) {
	eg, err := s.rec.Accounts.Egress(ctx, accountID)
	return execfetch.Do(ctx, in, execfetch.Binding{
		CheckURL: func(ctx context.Context, raw string) (*url.URL, error) {
			return netguard.CheckURLVia(ctx, raw, s.rec.AllowPrivateUpstream, eg.Proxied, netguard.DefaultLookup)
		},
		Client:        func(context.Context) (*http.Client, error) { return eg.Client, err },
		HeaderTimeout: reconcileHTTPTimeout, TotalTimeout: reconcileHTTPTimeout,
		Admit: admit,
	})
}
