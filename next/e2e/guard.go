package e2e

import (
	"net/http"
	"time"

	"github.com/tidwall/gjson"
)

// Guard plugin helpers (route shapes confirmed by e-sdk-plugins):
//   PUT /api/v1/p/guard/rules  {"rules":[{"name","kind":"keyword|regex","pattern","enabled"}]}
//       replaces the whole rule set; ids are int64 and omitted for new rules
//   GET /api/v1/p/guard/stats?from=&to=  {"data":{"requests_total","blocked_total","top_rules",...}}
//   PUT /api/v1/plugins/guard/settings {"values":{"webhook_url","record_snippets"}}
//   test build guardTestVersion (not in index.json; market/test/guard-<guardTestVersion>.s2plugin,
//   the overlay plugins/guard/testdata/guardtest):
//   POST /debug/dial {"address"} -> {"ok":false,"error"}; POST /debug/alloc?mb=N[&free=1]

const (
	guardTestVersion = "0.2.11-test"
	guardTestPath    = "/market/test/guard-" + guardTestVersion + ".s2plugin"
	guardDenyCode    = "guard_blocked"
)

// GuardRule is one interception rule.
type GuardRule struct {
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind"` // keyword | regex
	Pattern string `json:"pattern"`
	Enabled bool   `json:"enabled"`
}

// SetGuardRules replaces the guard rule set (no rules = allow everything).
func (e *Env) SetGuardRules(admin *Session, rules ...GuardRule) {
	e.T.Helper()
	if rules == nil {
		rules = []GuardRule{}
	}
	r := admin.API(e.T, http.MethodPut, "/p/guard/rules", map[string]any{"rules": rules})
	if r.Status != 200 && r.Status != 204 {
		e.T.Fatalf("guard rules: %s", r)
	}
	// The receiving node reloads at once, the others on the rules.changed
	// broadcast (best effort) or at the latest on their 5 s refresh.
	time.Sleep(6 * time.Second)
}

// GuardStats returns guard's statistics for [from, to].
func (e *Env) GuardStats(admin *Session, from, to time.Time) gjson.Result {
	e.T.Helper()
	return admin.OK(e.T, http.MethodGet, "/p/guard/stats", nil,
		Query("from", from.UTC().Format(time.RFC3339), "to", to.UTC().Format(time.RFC3339)))
}

// SetGuardSettings stores guard's settings form.
func (e *Env) SetGuardSettings(admin *Session, webhookURL string) {
	e.T.Helper()
	admin.OK(e.T, http.MethodPut, "/plugins/guard/settings", map[string]any{
		"values": map[string]any{"webhook_url": webhookURL, "record_snippets": false},
	})
}

// ForbiddenWord returns the keyword blocked by the e2e guard rule.
func (e *Env) ForbiddenWord() string { return "E2E-FORBIDDEN-" + e.RunID }

// ForbiddenRule blocks ForbiddenWord.
func (e *Env) ForbiddenRule() GuardRule {
	return GuardRule{Name: "e2e-" + e.RunID, Kind: "keyword", Pattern: e.ForbiddenWord(), Enabled: true}
}

// EnsureGuardTestBuild makes the guard test build (guardTestVersion) the active
// guard version: upgrade in place when guard is installed, fresh install
// otherwise. The package is uploaded (it is not listed in the index).
func (e *Env) EnsureGuardTestBuild(admin *Session) {
	e.T.Helper()
	d, installed := e.Plugin(admin, "guard")
	if installed && d.Get("active_version").String() == guardTestVersion && d.Get("status").String() == "enabled" {
		return
	}
	r := NewClient(e.BaseURL).Do(e.T, http.MethodGet, guardTestPath, nil)
	if r.Status != 200 {
		e.T.Fatalf("guard test build: %s", r)
	}
	if installed && d.Get("status").String() != "enabled" {
		e.EnsurePlugin(admin, "guard", "")
	}
	up := e.UploadPlugin(admin, "guard-"+guardTestVersion+".s2plugin", r.Body)
	if up.Status != 200 {
		e.T.Fatalf("upload guard test build: %s", up)
	}
	rev := reviewOf(e, up.Data())
	if !installed {
		e.ConsentAll(admin, rev, []string{"admin"})
		e.Enable(admin, "guard")
		e.WaitPlugin(admin, "guard", "enabled", guardTestVersion)
		return
	}
	if len(rev.Get("diff.added").Array())+len(rev.Get("diff.widened").Array()) > 0 {
		e.ConsentAll(admin, rev, []string{"admin"})
	}
	e.Upgrade(admin, "guard", guardTestVersion)
}
