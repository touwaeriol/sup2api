package account

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// blockedHosts is the administrator's plugin network access (CONTRACTS
// §113) refusing the listed hosts and their subdomains; asked records the
// plugin of every question.
type blockedHosts struct {
	hosts []string
	mu    sync.Mutex
	asked []string
}

func (b *blockedHosts) PluginRequestRefusal(_ context.Context, pluginKey string, target *url.URL) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked = append(b.asked, pluginKey)
	if manifest.ExternalServiceHost(b.hosts, target.Hostname()) {
		return target.Hostname() + " is on the blocked list of plugin network access"
	}
	return ""
}

const blockedMsg = "127.0.0.1 is on the blocked list of plugin network access"

// Every request a plugin describes and the core sends for one of its
// accounts passes the administrator's plugin network access: a blocked host
// - here the account's own upstream and the manifest's declared host alike -
// gets nothing sent. SendAccountRequest is PERMISSION_DENIED without an
// audit row; the account test, the model list, the quota query and the
// refresh report the refusal where they report a failure to reach the
// upstream.
func TestPluginRequestsToBlockedHostSendNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	e.plat.mu.Lock()
	e.plat.testURL, e.plat.quotaURL, e.plat.refreshURL = srv.URL+"/v1/messages", srv.URL+"/quota", srv.URL+"/token"
	e.plat.mu.Unlock()
	blocked := &blockedHosts{hosts: []string{"127.0.0.1"}}
	e.svc.d.NetworkAccess = blocked
	id := e.mkOAuthAccount("acct", "rt-1", 0)

	_, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers,
		&pluginv1.SendAccountRequestRequest{AccountId: id, Url: srv.URL + "/reset"})
	if ce := core.AsError(err); ce.Code != core.ErrPermissionDenied.Code || !strings.Contains(ce.Message, blockedMsg) ||
		ce.Details["reason"] != core.ReasonUpstreamHostBlocked {
		t.Fatalf("SendAccountRequest: %v", err)
	}
	if rows := e.auditRows(AuditPluginAccountRequest); len(rows) != 0 {
		t.Fatalf("a refused request was audited: %v", rows)
	}

	// Account test: a failed test naming the refusal.
	code, out := e.do("POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{})
	if d, _ := out["data"].(map[string]any); code != 200 || d["ok"] != false || !strings.Contains(fmt.Sprint(d["message"]), blockedMsg) {
		t.Fatalf("test: %d %v", code, out)
	}
	// Model list: an error naming the refusal.
	code, out = e.do("POST", fmt.Sprintf("/accounts/%d/models/fetch", id), map[string]any{})
	if code < 400 || !strings.Contains(fmt.Sprint(out), blockedMsg) {
		t.Fatalf("models: %d %v", code, out)
	}
	// Quota query: recorded as the query's error.
	e.withQuota(&manifest.AccountQuota{Query: true, AutoRefresh: true, Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-status"}}})
	if code, out := e.do("GET", fmt.Sprintf("/accounts/%d/quota?force=true", id), nil); code != 200 {
		t.Fatalf("quota: %d %v", code, out)
	}
	e.plat.mu.Lock()
	built := e.plat.quotaCalls
	e.plat.mu.Unlock()
	if built == 0 {
		t.Fatal("the quota request was not built")
	}
	// Refresh: the plugin reads the refusal as a transport failure.
	e.gen.types[0].Type.Refresh = &manifest.AccountRefresh{}
	if code, out := e.do("POST", fmt.Sprintf("/accounts/%d/refresh-credentials", id), nil); code == 200 && fmt.Sprint(out["data"]) == "" {
		t.Fatalf("refresh: %d %v", code, out)
	}
	if c := e.creds(id); c["access_token"] != "at-old" {
		t.Fatalf("renewed through a blocked host: %v", c)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the blocked host", n)
	}
	blocked.mu.Lock()
	asked := strings.Join(blocked.asked, ",")
	blocked.mu.Unlock()
	if strings.Count(asked, "anthropic") < 5 || strings.Trim(strings.ReplaceAll(asked, "anthropic", ""), ",") != "" {
		t.Fatalf("questions: %q", asked)
	}

	// Allowed again: the same requests go out.
	blocked.mu.Lock()
	blocked.hosts = nil
	blocked.mu.Unlock()
	if out, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers,
		&pluginv1.SendAccountRequestRequest{AccountId: id, Url: srv.URL + "/reset"}); err != nil || out.GetStatus() != 200 {
		t.Fatalf("allowed: %v %v", out, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits %d", hits.Load())
	}
}
