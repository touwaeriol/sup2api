package e2e

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// ------------------------------------------------------------------ market

// marketChannel is the store branch whose index the e2e market serves
// (deploy/docker/build-go.sh serves every channel the same index).
const marketChannel = "index-main"

// MarketSource returns the id of the default channel of the plugin store
// (main, its base pointed at the stack's market by SUB2API_MARKET_BASE_URL).
func (e *Env) MarketSource(admin *Session) int64 {
	e.T.Helper()
	srcs := admin.OK(e.T, http.MethodGet, "/market/sources", nil).Array()
	def, ok := Find(srcs, "is_default", "true")
	if !ok || def.Get("branch").String() != "main" {
		e.T.Fatalf("market sources = %s, want the default channel main", srcs)
	}
	return def.Get("id").Int()
}

// MarketIndex fetches the static index.json of the main channel served by
// Caddy.
func (e *Env) MarketIndex() gjson.Result {
	e.T.Helper()
	r := NewClient(e.BaseURL).Do(e.T, http.MethodGet, "/market/"+marketChannel+"/index.json", nil)
	if r.Status != 200 {
		e.T.Fatalf("market index: %s", r)
	}
	return r.JSON()
}

// MarketPackage downloads a first-party package listed in the static index.
func (e *Env) MarketPackage(key, version string) []byte {
	e.T.Helper()
	idx := e.MarketIndex()
	for _, p := range idx.Get("plugins").Array() {
		if p.Get("key").String() != key || p.Get("publisher").String() != "sub2api" {
			continue
		}
		for _, v := range p.Get("versions").Array() {
			if v.Get("version").String() != version {
				continue
			}
			u := v.Get("url").String()
			if !strings.Contains(u, "://") {
				u = e.BaseURL + "/market/" + marketChannel + "/" + strings.TrimPrefix(u, "./")
			}
			resp, err := http.Get(u)
			if err != nil {
				e.T.Fatalf("download %s: %v", u, err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				e.T.Fatalf("download %s: HTTP %d", u, resp.StatusCode)
			}
			if int64(len(data)) != v.Get("size").Int() || sha256hex(data) != v.Get("sha256").String() {
				e.T.Fatalf("market package %s@%s does not match index size/sha256", key, version)
			}
			return data
		}
	}
	e.T.Fatalf("%s@%s not in market index", key, version)
	return nil
}

// ------------------------------------------------------------------ install

// InstallFromMarket calls POST /plugins/install-from-market and returns the review.
func (e *Env) InstallFromMarket(admin *Session, key, version string) gjson.Result {
	e.T.Helper()
	src := e.MarketSource(admin)
	d := admin.OK(e.T, http.MethodPost, "/plugins/install-from-market",
		map[string]any{"source_id": src, "publisher": "sub2api", "key": key, "version": version})
	return reviewOf(e, d)
}

// UploadPlugin uploads a package and returns the raw response. A 409
// cluster_change_busy (another cluster change, such as the previous test's
// uninstall, is being submitted) is retried for a while.
func (e *Env) UploadPlugin(admin *Session, filename string, data []byte) *Resp {
	e.T.Helper()
	var r *Resp
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(time.Second) {
		r = admin.Upload(e.T, "/plugins/upload", "file", filename, data)
		if r.ErrCode() != "cluster_change_busy" || time.Now().After(deadline) {
			return r
		}
	}
}

func reviewOf(e *Env, d gjson.Result) gjson.Result {
	e.T.Helper()
	if d.Get("review").Exists() {
		d = d.Get("review")
	}
	for _, f := range []string{"plugin_key", "version", "trust", "signature_status", "host_permissions"} {
		if !d.Get(f).Exists() {
			e.T.Fatalf("review lacks %q: %s", f, d.Raw)
		}
	}
	return d
}

// ConsentAll approves every requested host permission and grants every
// row of each plugin permission the consent creates (review new=true) to
// roleKeys instead of its manifest defaults; none keeps the defaults.
func (e *Env) ConsentAll(admin *Session, review gjson.Result, roleKeys []string) {
	e.T.Helper()
	var overrides map[string]any
	if len(roleKeys) > 0 {
		roles := map[string]bool{}
		for _, r := range roleKeys {
			roles[r] = true
		}
		overrides = map[string]any{}
		for _, up := range review.Get("user_permissions").Array() {
			if up.Get("new").Bool() {
				overrides[up.Get("key").String()] = roles
			}
		}
	}
	e.Consent(admin, review, overrides)
}

// Consent approves every requested host permission with
// newPermissionRoles as new_permission_roles (local key -> role -> true or
// scopes; nil keeps every manifest default).
func (e *Env) Consent(admin *Session, review gjson.Result, newPermissionRoles map[string]any) {
	e.T.Helper()
	grants := []map[string]any{}
	for _, p := range review.Get("host_permissions").Array() {
		g := map[string]any{"permission": p.Get("id").String()}
		if s := p.Get("scope"); s.Exists() && s.Type != gjson.Null {
			g["scope"] = s.Value()
		}
		grants = append(grants, g)
	}
	body := map[string]any{"grants": grants, "denied": []string{}}
	if newPermissionRoles != nil {
		body["new_permission_roles"] = newPermissionRoles
	}
	admin.OK(e.T, http.MethodPost,
		fmt.Sprintf("/plugins/%s/versions/%s/consent", review.Get("plugin_key").String(), review.Get("version").String()), body)
}

// Plugin returns GET /plugins/:key, or ok=false on 404.
func (e *Env) Plugin(admin *Session, key string) (gjson.Result, bool) {
	e.T.Helper()
	r := admin.API(e.T, http.MethodGet, "/plugins/"+key, nil)
	if r.Status == 404 {
		return gjson.Result{}, false
	}
	if r.Status != 200 {
		e.T.Fatalf("plugin detail: %s", r)
	}
	return r.Data(), true
}

// PluginPermissions returns the plugin's module of GET /permissions, or
// ok=false when the catalog holds none of its permissions.
func (e *Env) PluginPermissions(admin *Session, key string) (gjson.Result, bool) {
	e.T.Helper()
	return Find(admin.OK(e.T, http.MethodGet, "/permissions", nil).Array(), "plugin_key", key)
}

// PluginNodeStates extracts node -> state from a plugin detail. It accepts
// the fields "nodes" or "node_states" with "node_id" and "state"/"status".
func PluginNodeStates(d gjson.Result) map[string]string {
	out := map[string]string{}
	nodes := d.Get("nodes")
	if !nodes.Exists() {
		nodes = d.Get("node_states")
	}
	for _, n := range nodes.Array() {
		id := n.Get("node_id").String()
		st := n.Get("state").String()
		if s := n.Get("state"); s.IsObject() {
			st = s.Get("state").String()
		}
		if st == "" {
			st = n.Get("status").String()
		}
		out[id] = st
	}
	return out
}

// Enable starts an enable rollout and waits until the plugin is enabled on
// every live node.
func (e *Env) Enable(admin *Session, key string) {
	e.T.Helper()
	admin.OK(e.T, http.MethodPost, "/plugins/"+key+"/enable", nil)
	e.WaitPlugin(admin, key, "enabled", "")
}

// Disable disables a plugin and waits for the disable rollout to finish.
func (e *Env) Disable(admin *Session, key string) {
	e.T.Helper()
	admin.OK(e.T, http.MethodPost, "/plugins/"+key+"/disable", nil)
	e.WaitPlugin(admin, key, "disabled", "")
}

// Upgrade starts an upgrade rollout to version and waits for it.
func (e *Env) Upgrade(admin *Session, key, version string) {
	e.T.Helper()
	admin.OK(e.T, http.MethodPost, "/plugins/"+key+"/upgrade", map[string]any{"version": version})
	e.WaitPlugin(admin, key, "enabled", version)
}

// Uninstall removes a plugin (DELETE /plugins/:key?purge=).
func (e *Env) Uninstall(admin *Session, key string, purge bool) {
	e.T.Helper()
	r := admin.API(e.T, http.MethodDelete, "/plugins/"+key, nil, Query("purge", fmt.Sprint(purge)))
	if r.Status != 200 && r.Status != 204 {
		e.T.Fatalf("uninstall: %s", r)
	}
	Eventually(e.T, 30*time.Second, time.Second, "plugin "+key+" removed", func() bool {
		_, ok := e.Plugin(admin, key)
		return !ok
	})
}

// UninstallPurgeAccounts uninstalls a (disabled) plugin and soft-deletes the
// accounts of its account types (CONTRACTS §14.3 purge_accounts).
func (e *Env) UninstallPurgeAccounts(admin *Session, key string) {
	e.T.Helper()
	// status=disabled is reported before the disable rollout is fully
	// closed; uninstall answers 409 until then.
	var r *Resp
	Eventually(e.T, 30*time.Second, time.Second, "uninstall "+key+" with purge_accounts", func() bool {
		r = admin.API(e.T, http.MethodDelete, "/plugins/"+key, nil, Query("purge", "false", "purge_accounts", "true"))
		return r.Status != 409
	})
	if r.Status != 200 && r.Status != 204 {
		e.T.Fatalf("uninstall with purge_accounts: %s", r)
	}
	Eventually(e.T, 30*time.Second, time.Second, "plugin "+key+" removed", func() bool {
		_, ok := e.Plugin(admin, key)
		return !ok
	})
}

// WaitPlugin waits until the plugin has status (and active_version, if set)
// and its rollout has finished; for "enabled", every node must also report
// "active". The current-rollout API returns 200 with data:null when none is
// open; transport/server errors do not prove completion.
func (e *Env) WaitPlugin(admin *Session, key, status, version string) gjson.Result {
	e.T.Helper()
	var last gjson.Result
	var lastRollout string
	msg := func() string {
		return fmt.Sprintf("plugin %s status=%s version=%s (last detail: %s; rollout: %s)", key, status, version, last.Raw, lastRollout)
	}
	Eventually(e.T, 90*time.Second, time.Second, msg, func() bool {
		r := admin.API(e.T, http.MethodGet, "/plugins/"+key+"/rollouts/current", nil)
		lastRollout = r.String()
		if r.Status >= 500 {
			return false
		}
		if r.Status != http.StatusOK {
			e.T.Fatalf("cannot determine rollout completion for %s: %s", key, r)
		}
		if !r.Data().Exists() || (r.Data().Type != gjson.Null && !r.Data().IsObject()) {
			e.T.Fatalf("invalid current rollout response for %s: %s", key, r)
		}
		switch ph := r.Data().Get("phase").String(); ph {
		case "preparing", "activating":
			// Disable publishes status=disabled before nodes acknowledge it.
			// Returning now makes the next enable/uninstall race that rollout.
			return false
		case "failed", "cancelled", "rolled_back":
			e.T.Fatalf("rollout of %s ended in %s: %s", key, ph, r.Data().Raw)
		}
		d, ok := e.Plugin(admin, key)
		if !ok {
			return false
		}
		last = d
		if d.Get("status").String() != status {
			return false
		}
		if version != "" && d.Get("active_version").String() != version {
			return false
		}
		if status == "enabled" {
			states := PluginNodeStates(d)
			if len(states) < len(e.NodeURLs) {
				return false
			}
			for _, st := range states {
				if st != "active" {
					return false
				}
			}
		}
		return true
	})
	return last
}

// EnsurePlugin makes key@version (version "" = whatever the market's newest
// base version is, or the currently active one) installed and enabled.
// Different installed versions are replaced: newer via upgrade, older by
// uninstall(purge)+install.
func (e *Env) EnsurePlugin(admin *Session, key, version string) {
	e.T.Helper()
	d, ok := e.Plugin(admin, key)
	if ok && (version == "" || d.Get("active_version").String() == version) {
		switch d.Get("status").String() {
		case "enabled":
			e.WaitPlugin(admin, key, "enabled", version)
			return
		case "disabled", "installed":
			e.Enable(admin, key)
			return
		}
	}
	if ok && d.Get("builtin").Bool() {
		// Built-in plugins cannot be uninstalled: only forward upgrades work.
		if d.Get("status").String() != "enabled" {
			e.Enable(admin, key)
			d, _ = e.Plugin(admin, key)
		}
		if active := d.Get("active_version").String(); version != "" && active != version {
			if semverLess(version, active) {
				e.T.Skipf("built-in %s is at %s and cannot go back to %s; run on a fresh deployment", key, active, version)
			}
			rev := e.InstallFromMarket(admin, key, version)
			if rev.Get("consent_status").String() == "awaiting_consent" {
				e.ConsentAll(admin, rev, []string{"admin"})
			}
			e.Upgrade(admin, key, version)
		}
		return
	}
	if ok {
		// Replace whatever is installed: simplest and independent of semver order.
		e.Uninstall(admin, key, true)
	}
	if version == "" {
		version = e.defaultVersion(key)
	}
	rev := e.InstallFromMarket(admin, key, version)
	e.ConsentAll(admin, rev, []string{"admin"})
	e.Enable(admin, key)
}

// defaultVersion is the lowest non-prerelease version listed for key.
func (e *Env) defaultVersion(key string) string {
	e.T.Helper()
	for _, p := range e.MarketIndex().Get("plugins").Array() {
		if p.Get("key").String() != key {
			continue
		}
		best := ""
		for _, v := range p.Get("versions.#.version").Array() {
			s := v.String()
			if strings.Contains(s, "-") {
				continue
			}
			if best == "" || s < best {
				best = s
			}
		}
		if best != "" {
			return best
		}
	}
	e.T.Fatalf("plugin %s not in market", key)
	return ""
}

// PluginRoute calls /api/v1/p/:key/<path> on base (LB or a single node URL).
func (e *Env) PluginRoute(s *Session, base, method, key, path string, body any) *Resp {
	e.T.Helper()
	c := &Client{Base: base, HTTP: s.HTTP, Token: s.Token}
	return c.API(e.T, method, "/p/"+key+path, body)
}

// semverLess compares release versions "x.y.z" numerically (pre-release
// suffixes are ignored).
func semverLess(a, b string) bool {
	pa, pb := strings.Split(strings.SplitN(a, "-", 2)[0], "."), strings.Split(strings.SplitN(b, "-", 2)[0], ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
