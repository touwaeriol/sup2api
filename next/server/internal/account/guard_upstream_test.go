package account

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
)

// The console's upstream calls follow the gateway's rule: through a proxy
// the name is the proxy's to resolve, but a literal private address is
// refused all the same; a direct call checks the resolved addresses.
func TestGuardUpstreamFollowsTheGatewayRule(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	for raw, want := range map[string]error{
		"https://name.invalid/v1":  nil,
		"http://127.0.0.1:9/v1":    netguard.ErrPrivate,
		"http://10.0.0.1/v1":       netguard.ErrPrivate,
		"http://localhost:8080/v1": netguard.ErrPrivate,
	} {
		u, _ := url.Parse(raw)
		if err := s.guardUpstream(ctx, "", u, true); !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Errorf("proxied %s: %v, want %v", raw, err, want)
		}
	}
	u, _ := url.Parse("http://127.0.0.1:9/v1")
	if err := s.guardUpstream(ctx, "", u, false); !errors.Is(err, netguard.ErrPrivate) {
		t.Errorf("direct loopback: %v", err)
	}
	s.d.AllowPrivateUpstream = true
	if err := s.guardUpstream(ctx, "", u, true); err != nil {
		t.Errorf("allow private: %v", err)
	}
}
