package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// ------------------------------------------------------------------ container control

// Container returns the compose container name of a service (e.g. "node-2").
func (e *Env) Container(service string) string {
	return fmt.Sprintf("%s-%s-1", e.Project, service)
}

// Docker runs a docker CLI command on E2E_DOCKER_HOST ("local" or an ssh
// host) and returns stdout. The test is skipped when no host is configured.
func (e *Env) Docker(args ...string) string {
	e.T.Helper()
	e.RequireDocker()
	var cmd *exec.Cmd
	if e.DockerHost == "local" {
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("ssh", e.DockerHost, shellJoin(append([]string{"docker"}, args...)))
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		e.T.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// KillNode SIGKILLs node n (1-based) to simulate a crash.
func (e *Env) KillNode(n int) { e.T.Helper(); e.Docker("kill", e.Container(fmt.Sprintf("node-%d", n))) }

// StopNode stops node n gracefully.
func (e *Env) StopNode(n int) { e.T.Helper(); e.Docker("stop", e.Container(fmt.Sprintf("node-%d", n))) }

// StartNode starts node n and waits until it answers /healthz directly.
func (e *Env) StartNode(n int) {
	e.T.Helper()
	c := e.Container(fmt.Sprintf("node-%d", n))
	// A node still draining after SIGTERM (stop grace 180 s) is running:
	// starting it is a no-op and it exits afterwards. Wait for it to stop.
	Eventually(e.T, 200*time.Second, time.Second, fmt.Sprintf("node-%d serving or stopped", n), func() bool {
		return e.NodeHealthy(n) || strings.TrimSpace(e.Docker("inspect", "--format", "{{.State.Running}}", c)) == "false"
	})
	e.Docker("start", c)
	Eventually(e.T, 90*time.Second, time.Second, fmt.Sprintf("node-%d healthy", n), func() bool {
		return e.NodeHealthy(n)
	})
}

// NodeExec runs a shell command inside node n.
func (e *Env) NodeExec(n int, script string) string {
	e.T.Helper()
	return e.Docker("exec", e.Container(fmt.Sprintf("node-%d", n)), "sh", "-c", script)
}

// KillPluginProcesses SIGKILLs every process of plugin key inside node n
// (matched on the command line; the core itself is never matched).
func (e *Env) KillPluginProcesses(n int, key string) {
	e.T.Helper()
	// "[p]lugins" keeps the pattern from matching this sh -c command line itself.
	e.NodeExec(n, fmt.Sprintf(`pkill -9 -f '[p]lugins/.*%s' || true`, key))
}

// ------------------------------------------------------------------ PG / Redis inspection

// SQL runs a query in the compose PostgreSQL and returns rows as
// "|"-separated lines (psql -At).
func (e *Env) SQL(query string) []string {
	e.T.Helper()
	out := e.Docker("exec", e.Container("pg"), "psql", "-U", "sub2api", "-d", "sub2api", "-v", "ON_ERROR_STOP=1", "-At", "-c", query)
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			rows = append(rows, l)
		}
	}
	return rows
}

// Redis runs the cache CLI in the compose cache container (valkey-cli, or
// redis-cli when E2E_REDIS_IMAGE is a Redis image) and returns trimmed output.
func (e *Env) Redis(args ...string) string {
	e.T.Helper()
	cli := `if command -v valkey-cli >/dev/null 2>&1; then exec valkey-cli --raw "$@"; else exec redis-cli --raw "$@"; fi`
	return strings.TrimSpace(e.Docker(append([]string{"exec", e.Container("redis"), "sh", "-c", cli, "sh"}, args...)...))
}

// ------------------------------------------------------------------ nodes

// NodeHealthy reports whether node n (1-based) answers /healthz directly.
func (e *Env) NodeHealthy(n int) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(e.NodeURLs[n-1] + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// NodeHealth returns the /healthz document of node n.
func (e *Env) NodeHealth(n int) gjson.Result {
	e.T.Helper()
	r := NewClient(e.NodeURLs[n-1]).Do(e.T, http.MethodGet, "/healthz", nil)
	if r.Status != 200 {
		e.T.Fatalf("node-%d /healthz: %s", n, r)
	}
	return r.JSON()
}

// ClusterNodes returns GET /nodes.
func (e *Env) ClusterNodes(admin *Session) []gjson.Result {
	e.T.Helper()
	return admin.OK(e.T, http.MethodGet, "/nodes", nil).Array()
}

// AliveNodeIDs returns node_id of nodes reported alive by GET /nodes (a node
// without an explicit "alive"/"status" field counts as alive).
func (e *Env) AliveNodeIDs(admin *Session) []string {
	e.T.Helper()
	var ids []string
	for _, n := range e.ClusterNodes(admin) {
		if a := n.Get("alive"); a.Exists() && !a.Bool() {
			continue
		}
		if s := n.Get("status").String(); s != "" && s != "alive" && s != "online" && s != "active" {
			continue
		}
		ids = append(ids, n.Get("node_id").String())
	}
	return ids
}

// OnEachNode runs fn with a client bound to each node directly.
func (e *Env) OnEachNode(s *Session, fn func(n int, c *Client)) {
	e.T.Helper()
	for i, u := range e.NodeURLs {
		c := &Client{Base: u, HTTP: s.HTTP, Token: s.Token}
		fn(i+1, c)
	}
}
