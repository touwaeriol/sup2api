package sandbox

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// With a private run directory the plugin gets its own TMPDIR, not the
// core's.
func TestRunDirReplacesSharedTemp(t *testing.T) {
	t.Setenv("TMPDIR", "/shared/tmp")
	t.Setenv("TEMP", "/shared/tmp")
	t.Setenv("TMP", "/shared/tmp")
	run := "/data/.run/demo-1"
	spec := core.LaunchSpec{PluginKey: "demo", Version: "1.0.0", BinaryPath: "/x/plugin", RunDir: run,
		Env: []string{"TMPDIR=" + run + "/tmp", "TEMP=" + run + "/tmp", "TMP=" + run + "/tmp"}}
	for _, wrap := range []bool{false, true} {
		l := NewLauncher(LauncherOptions{DisableWrap: !wrap, Executable: "/usr/bin/sub2api"})
		l.wrap = wrap
		cmd, err := l.Command(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range cmd.Env {
			if strings.HasSuffix(kv, "=/shared/tmp") {
				t.Fatalf("wrap=%v: shared temp passed on: %q", wrap, cmd.Env)
			}
		}
		if !slices.Contains(cmd.Env, "TMPDIR="+run+"/tmp") {
			t.Fatalf("wrap=%v: env %q", wrap, cmd.Env)
		}
		// Without a data directory ("fs.data") it runs in the run directory
		// and is told of no data directory.
		if cmd.Dir != run || slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "SUB2API_PLUGIN_DATA_DIR=") }) {
			t.Fatalf("wrap=%v: dir %q env %q", wrap, cmd.Dir, cmd.Env)
		}
		if wrap {
			o, err := parseExecArgs(cmd.Args[2:], os.Stderr)
			if err != nil || o.DataDir != "" || o.CACerts {
				t.Fatalf("plugin-exec args %q: %+v %v", cmd.Args, o, err)
			}
		}
	}
	// Without a run directory the inherited TMPDIR stays (unchanged
	// behaviour for other callers).
	l := NewLauncher(LauncherOptions{DisableWrap: true})
	cmd, err := l.Command(context.Background(), core.LaunchSpec{BinaryPath: "/x/plugin"})
	if err != nil || !slices.Contains(cmd.Env, "TMPDIR=/shared/tmp") {
		t.Fatalf("env %q %v", cmd.Env, err)
	}
}

// Whatever the core's environment holds, the plugin command only carries
// the allow list: no database, Redis, key, token or other secret.
func TestCommandEnvIsAllowList(t *testing.T) {
	for _, k := range []string{"DATABASE_URL", "REDIS_URL", "SUB2API_MASTER_KEY",
		"SUB2API_CONTROL_TOKEN", "SUB2API_BOOTSTRAP_ADMIN_PASSWORD", "ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "UPDATER_SOCKET"} {
		t.Setenv(k, "secret-"+k)
	}
	for _, wrap := range []bool{false, true} {
		l := NewLauncher(LauncherOptions{Executable: "/usr/bin/sub2api"})
		l.wrap = wrap
		cmd, err := l.Command(context.Background(), core.LaunchSpec{PluginKey: "demo", BinaryPath: "/x/plugin"})
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range cmd.Env {
			if strings.Contains(kv, "secret-") {
				t.Fatalf("wrap=%v: %s passed to the plugin", wrap, kv)
			}
		}
	}
	// plugin-exec filters again what reaches the plugin binary.
	env := buildEnv([]string{"PATH=/bin", "SUB2API_MASTER_KEY=x", "SUB2API_CONTROL_TOKEN=y", "SUB2API_PLUGIN_KEY=demo"}, &execOptions{})
	secret := func(kv string) bool {
		return strings.HasPrefix(kv, "SUB2API_MASTER_KEY") || strings.HasPrefix(kv, "SUB2API_CONTROL_TOKEN")
	}
	if slices.ContainsFunc(env, secret) {
		t.Fatalf("buildEnv kept secrets: %q", env)
	}
}
