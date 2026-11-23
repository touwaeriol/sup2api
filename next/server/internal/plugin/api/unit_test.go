package api

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// GET /market/plugins keeps data as the plugin array and reports the core
// version as a top-level host_version.
func TestMarketPluginsHostVersion(t *testing.T) {
	svc := install.New(install.Deps{}, install.Options{HostVersion: "0.3.1-dev"})
	a := New(Deps{Install: svc})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	a.RegisterRoutes(httpapi.NewRouter(engine, tokens{}, authz{}))
	h := &harness{t: t, engine: engine}

	code, out := h.do("GET", "/market/plugins", "admin", nil)
	if code != 200 || out["host_version"] != "0.3.1-dev" {
		t.Fatalf("market = %d %v", code, out)
	}
	if list, ok := out["data"].([]any); !ok || len(list) != 0 {
		t.Fatalf("market data = %v", out["data"])
	}
}

func TestResourcesMessage(t *testing.T) {
	running := resourcesMessage(true, install.StatusEnabled)
	if !strings.Contains(running["zh"], "已通知各节点按新限制重启插件实例") || !strings.Contains(running["en"], "restart") {
		t.Fatalf("running = %v", running)
	}
	if m := resourcesMessage(true, install.StatusDisabled); !strings.Contains(m["en"], "enabled") || m["zh"] == "" {
		t.Fatalf("disabled = %v", m)
	}
	if m := resourcesMessage(false, install.StatusEnabled); !strings.Contains(m["en"], "next time") || m["zh"] == "" {
		t.Fatalf("not notified = %v", m)
	}
}

// refusingTokens refuses every step-up proof (shallower than the pass
// tokens embeds), to show which routes ask for one.
type refusingTokens struct {
	tokens
	testutil.StepUpRefused
}

// stepUpCase is a route and the step-up scope it asks for ("": none).
type stepUpCase struct {
	method, path string
	body         any
	scope        string
}

// stepUpCases are the routes TestPluginRoutesStepUp checks. The ones taking
// no proof fail further on (no database or market here), not with 403.
func stepUpCases(t *testing.T) []stepUpCase {
	market := map[string]any{"source_id": 1, "key": "guard", "version": "0.1.0"}
	withConsent := map[string]any{"source_id": 1, "key": "guard", "version": "0.1.0", "consent": map[string]any{"package_sha256": "00"}}
	return []stepUpCase{
		{"POST", "/plugins/upload", fileBody(t, []byte("x")), core.StepUpPluginInstall},
		{"POST", "/plugins/upload", consentFileBody(t, []byte("x"), `{"package_sha256":"00"}`), core.StepUpPluginGrant},
		{"POST", "/plugins/install-from-market", market, core.StepUpPluginInstall},
		{"POST", "/plugins/install-from-market", withConsent, core.StepUpPluginGrant},
		{"POST", "/plugins/guard/versions/0.1.0/consent", nil, core.StepUpPluginGrant},
		{"POST", "/plugins/guard/versions/0.1.0/role-grants/confirm", nil, core.StepUpPluginGrant},
		{"POST", "/publishers", nil, core.StepUpPublisherManage},
		{"POST", "/publishers/1/keys", nil, core.StepUpPublisherManage},
		{"POST", "/publishers/1/revoke", nil, core.StepUpPublisherManage},
		{"POST", "/publisher-keys/k/revoke", nil, core.StepUpPublisherManage},
		{"POST", "/plugins/review-upload", fileBody(t, []byte("x")), ""},
		{"POST", "/plugins/review-from-market", market, ""},
		{"POST", "/plugins/guard/versions/0.1.0/reject", nil, ""},
		{"POST", "/plugins/guard/enable", nil, ""},
		{"DELETE", "/plugins/guard", nil, ""},
		{"GET", "/publishers", nil, ""},
	}
}

// Code execution and trust roots take a step-up proof (CONTRACTS §80.2):
// installing, consenting and the publishers' writes; rejecting, enabling,
// uninstalling, reading and the install preview do not. An install
// carrying the consent asks for the consent's scope.
func TestPluginRoutesStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	New(Deps{Install: install.New(install.Deps{}, install.Options{})}).RegisterRoutes(httpapi.NewRouter(engine, refusingTokens{}, authz{}))
	h := &harness{t: t, engine: engine}
	for _, tc := range stepUpCases(t) {
		code, out := h.do(tc.method, tc.path, "admin", tc.body)
		e, _ := out["error"].(map[string]any)
		details, _ := e["details"].(map[string]any)
		stepUp := code == 403 && e["code"] == "verification_required"
		if stepUp != (tc.scope != "") || stepUp && details["scope"] != tc.scope {
			t.Errorf("%s %s: %d %v, step-up wanted %q", tc.method, tc.path, code, out, tc.scope)
		}
	}
}
