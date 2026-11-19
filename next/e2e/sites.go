package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// Helpers of the upstream site plugins (newapi, sub2api: CONTRACTS §119),
// account balances (§52), image generation (§118) and plugin network
// access (§113).

// mockHost is the host name of the mock's internal address (an account's
// base_url in the tests).
func (e *Env) mockHost() string {
	e.T.Helper()
	u, err := url.Parse(e.MockInternalURL)
	if err != nil {
		e.T.Fatal(err)
	}
	return u.Hostname()
}

// AccountBalance is GET /accounts/:id/balance on c; force reads the
// upstream now.
func (e *Env) AccountBalance(c *Client, id int64, force bool) gjson.Result {
	e.T.Helper()
	return c.OK(e.T, http.MethodGet, fmt.Sprintf("/accounts/%d/balance", id), nil, Query("force", fmt.Sprint(force)))
}

// nodeOneNetworkAccess returns a client of node 1 and a setter of the
// plugin network access setting saved there. The node that saves the
// setting drops its cached copy before answering, the other one on a
// broadcast a moment later, so a test sends what the setting rules to node
// 1 too. The setting is the default again now and at cleanup.
func (e *Env) nodeOneNetworkAccess(admin *Session) (*Client, func(map[string]any)) {
	e.T.Helper()
	t := e.T
	node := *admin.Client
	node.Base = e.NodeURLs[0]
	set := func(body map[string]any) {
		t.Helper()
		node.OK(t, http.MethodPut, "/settings/plugin-egress", body)
	}
	defaults := map[string]any{"mode": "allow_all", "blocked_domains": []string{}, "allowed_domains": []string{}}
	t.Cleanup(func() { set(defaults) })
	set(defaults)
	return &node, set
}

// deniedInEgressLog reports a denied row of plugin's egress log for host
// since from whose error contains reason.
func (e *Env) deniedInEgressLog(admin *Session, plugin, host, reason string, from time.Time) bool {
	e.T.Helper()
	d := admin.OK(e.T, http.MethodGet, "/plugins/"+plugin+"/egress", nil, Query("from", from.UTC().Format(time.RFC3339),
		"to", time.Now().Add(time.Minute).UTC().Format(time.RFC3339), "page_size", "200"))
	for _, r := range d.Get("items").Array() {
		if r.Get("host").String() == host && r.Get("result").String() == "denied" && strings.Contains(r.Get("error").String(), reason) {
			return true
		}
	}
	return false
}

// callsTo returns the mock requests since mark to path.
func callsTo(t testing.TB, m *Mock, mark int64, path string) []MockRequest {
	t.Helper()
	var out []MockRequest
	for _, r := range m.Since(t, mark) {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// ensureExpressionPrice makes the admin price of model the expression
// price expr: updated when the model has a price (a fixed model name priced
// by an earlier run on the same stack), created otherwise.
func (e *Env) ensureExpressionPrice(admin *Session, model, expr string) {
	e.T.Helper()
	if p, ok := Find(admin.ListAll(e.T, "/prices", "q", model), "model", model); ok {
		admin.OK(e.T, http.MethodPatch, fmt.Sprintf("/prices/%d", p.Get("id").Int()), map[string]any{"mode": "expression", "expression": expr})
		return
	}
	e.CreatePrice(admin, map[string]any{"model": model, "mode": "expression", "expression": expr, "note": "e2e image tool price"})
}
