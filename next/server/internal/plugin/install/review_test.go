package install

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

type reviewEndpoint struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Billing  string `json:"billing"`
}

type reviewOut struct {
	Platforms []struct {
		ID        string            `json:"id"`
		Label     map[string]string `json:"label"`
		Endpoints []reviewEndpoint  `json:"endpoints"`
	} `json:"platforms"`
	GatewayEndpoints []reviewEndpoint `json:"gateway_endpoints"`
	AccountTypes     []struct {
		ID        string            `json:"id"`
		Label     map[string]string `json:"label"`
		Platforms []string          `json:"platforms"`
		FormMode  string            `json:"form_mode"`
	} `json:"account_types"`
}

func review(t *testing.T, r *Review) reviewOut {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out reviewOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The review lists the declared platforms with their endpoints, derives
// gateway_endpoints from them, and lists account types with the platforms
// they serve (CONTRACTS §13).
func TestBuildReviewPlatforms(t *testing.T) {
	m := pkgtest.Platform("video", "0.1.0", "sub2api")
	out := review(t, buildReview(m, pkgtest.Files(m), &pkg.Verification{Publisher: "sub2api", Trust: "official"}, "0.1.0", nil))

	if len(out.Platforms) != 1 {
		t.Fatalf("platforms = %+v", out.Platforms)
	}
	p := out.Platforms[0]
	if p.ID != "video" || p.Label["en"] != "Video video" || len(p.Endpoints) != 2 {
		t.Fatalf("platform = %+v", p)
	}
	if e := p.Endpoints[1]; e.Method != "GET" || e.Path != "/video/v1/models/:model:status" || e.Protocol != "video.status" || e.Billing != "free" {
		t.Fatalf("endpoint = %+v", e)
	}
	if len(out.GatewayEndpoints) != 2 {
		t.Fatalf("gateway_endpoints = %+v", out.GatewayEndpoints)
	}
	if e := out.GatewayEndpoints[0]; e.ID != "generate" || e.Platform != "video" || e.Path != "/video/v1/videos" || e.Billing != "usage" {
		t.Fatalf("gateway endpoint = %+v", e)
	}
	if len(out.AccountTypes) != 1 {
		t.Fatalf("account_types = %+v", out.AccountTypes)
	}
	at := out.AccountTypes[0]
	if at.ID != "apikey" || at.Label["en"] != "API Key" || len(at.Platforms) != 1 || at.Platforms[0] != "video" || at.FormMode != "native" {
		t.Fatalf("account type = %+v", at)
	}

	// An account-type-only plugin serving a built-in platform.
	a := pkgtest.Anthropic("anthropic", "0.1.0", "sub2api")
	a.AccountTypes[0].Platforms[0].Endpoints = []string{"messages"}
	declared := buildReview(a, pkgtest.Files(a), &pkg.Verification{}, "0.1.0", nil)
	if got := declared.AccountTypes[0].PlatformDeclarations; len(got) != 1 || len(got[0].Endpoints) != 1 || got[0].Endpoints[0] != "messages" {
		t.Fatalf("review lost endpoint subset: %+v", got)
	}
	out = review(t, buildReview(a, pkgtest.Files(a), &pkg.Verification{}, "0.1.0", nil))
	if len(out.Platforms) != 0 || len(out.GatewayEndpoints) != 0 {
		t.Fatalf("anthropic plugin platforms = %+v %+v", out.Platforms, out.GatewayEndpoints)
	}
	if len(out.AccountTypes) != 1 || len(out.AccountTypes[0].Platforms) != 1 || out.AccountTypes[0].Platforms[0] != "anthropic" {
		t.Fatalf("anthropic account types = %+v", out.AccountTypes)
	}

	// Plugins without platforms or account types report empty lists, not null.
	g := pkgtest.Guard("guard", "0.1.0", "sub2api")
	raw, _ := json.Marshal(buildReview(g, pkgtest.Files(g), &pkg.Verification{}, "0.1.0", nil))
	var gout map[string]any
	_ = json.Unmarshal(raw, &gout)
	for _, k := range []string{"account_types", "platforms", "gateway_endpoints"} {
		if list, ok := gout[k].([]any); !ok || len(list) != 0 {
			t.Fatalf("guard %s = %v", k, gout[k])
		}
	}
	if _, ok := gout["platform"]; ok {
		t.Fatal("the review has no platform field")
	}
}

// The broadcast host permission is low risk: granting it needs no extra
// console permission (CONTRACTS §14.3).
func TestReviewBroadcastLowRisk(t *testing.T) {
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapAppBroadcast})
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "broadcast"})
	r := buildReview(m, pkgtest.Files(m), &pkg.Verification{}, "0.1.0", nil)
	found := false
	for _, hp := range r.HostPermissions {
		if hp.ID != "broadcast" {
			continue
		}
		found = true
		if hp.Risk != manifest.RiskLow || hp.Requires != "" {
			t.Fatalf("broadcast = %+v", hp)
		}
	}
	if !found {
		t.Fatal("broadcast host permission missing from review")
	}
	if caps := strings.Join(r.Capabilities, ","); !strings.Contains(caps, manifest.CapAppBroadcast) {
		t.Fatalf("capabilities = %s", caps)
	}
}
