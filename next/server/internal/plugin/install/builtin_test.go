package install

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

func TestEnsureBuiltinInstallsEnablesAndBlocksUninstall(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	for i := range m.UserPermissions {
		m.UserPermissions[i].Defaults = map[string]manifest.RoleDefault{"operator": {Unscoped: true}, "user": {Unscoped: true}}
	}
	if err := os.WriteFile(filepath.Join(dir, "guard-0.1.0.s2plugin"), pkgtest.Build(m, e.root), 0o644); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)

	if err := e.svc.EnsureBuiltin(ctx, dir, log); err != nil {
		t.Fatal(err)
	}
	var status string
	var builtin bool
	var active *string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT status, builtin, active_version FROM plugins WHERE key = 'guard'`).
		Scan(&status, &builtin, &active); err != nil {
		t.Fatal(err)
	}
	if !builtin || status != StatusEnabled || active == nil || *active != "0.1.0" || len(e.rollout.enabled) != 1 {
		t.Fatalf("after ensure: status=%s builtin=%v active=%v enabled=%v", status, builtin, active, e.rollout.enabled)
	}
	// Every requested host permission is granted, including critical ones,
	// without an operator; new plugin permissions went to their manifest
	// defaults, not to a role the start names.
	grants, err := LoadGrants(ctx, e.db.Gorm, "guard")
	if err != nil {
		t.Fatal(err)
	}
	for _, hp := range m.HostPermissions {
		if g, ok := grants[hp.ID]; !ok || g.Status != GrantGranted {
			t.Fatalf("grant %s = %+v", hp.ID, g)
		}
	}
	if last := e.perms.syncs[len(e.perms.syncs)-1]; !slices.Equal(last.roles, []string{"operator", "supplier_agent", "user"}) {
		t.Fatalf("permission roles = %v", last.roles)
	}

	// Idempotent: a second start changes nothing.
	if err := e.svc.EnsureBuiltin(ctx, dir, log); err != nil {
		t.Fatal(err)
	}
	if len(e.rollout.enabled) != 1 {
		t.Fatalf("enabled twice: %v", e.rollout.enabled)
	}

	// Uninstall is refused, disabled or not; a disabled one stays disabled.
	if _, err := e.svc.Uninstall(ctx, "guard", UninstallOptions{Purge: true, PurgeAccounts: true}, e.admin); core.AsError(err).Code != core.ErrPermissionDenied.Code {
		t.Fatalf("uninstall enabled builtin: %v", err)
	}
	if _, err := e.rollout.Disable(ctx, "guard", e.admin, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Uninstall(ctx, "guard", UninstallOptions{}, e.admin); core.AsError(err).Code != core.ErrPermissionDenied.Code {
		t.Fatalf("uninstall disabled builtin: %v", err)
	}
	if len(e.accounts.purged) != 0 {
		t.Fatalf("builtin accounts purged: %v", e.accounts.purged)
	}
	if err := e.svc.EnsureBuiltin(ctx, dir, log); err != nil {
		t.Fatal(err)
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT status FROM plugins WHERE key = 'guard'`).Scan(&status)
	if status != StatusDisabled || len(e.rollout.enabled) != 1 || len(e.schemas.dropped) != 0 {
		t.Fatalf("after restart: status=%s enabled=%v dropped=%v", status, e.rollout.enabled, e.schemas.dropped)
	}
}

// testLogger sends EnsureBuiltin's log to the test output: it logs, and does
// not return, the failure of a single package, so with a discarding logger a
// package that never installed reads as "the core did not enable it".
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(tlogWriter{t}, nil))
}

type tlogWriter struct{ t *testing.T }

func (w tlogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

// An install-only built-in is installed, approved and marked built-in, but
// never enabled by the core - on the first start or any later one - while a
// built-in next to it that is not listed is still enabled. The core used to
// enable every built-in on first install; that behaviour fails this test on
// the rollout.enabled assertions.
func TestEnsureBuiltinInstallOnlyStaysDisabledUntilAnOperatorEnablesIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	off := pkgtest.Guard("guard", "0.1.0", "sub2api")
	on := pkgtest.Guard("guard_on", "0.1.0", "sub2api")
	if err := os.WriteFile(filepath.Join(dir, "guard-0.1.0.s2plugin"), pkgtest.Build(off, e.root), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "guard_on-0.1.0.s2plugin"), pkgtest.Build(on, e.root), 0o644); err != nil {
		t.Fatal(err)
	}
	// CRLF, a comment and blank lines: the file is written on Linux by
	// build-go.sh but must not break when edited elsewhere.
	if err := os.WriteFile(filepath.Join(dir, InstallOnlyFile), []byte("# needs configuration first\r\n\r\n  guard  \r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := testLogger(t)

	state := func(key string) (string, bool, *string) {
		t.Helper()
		var status string
		var builtin bool
		var active *string
		if err := e.db.SQL.QueryRowContext(ctx, `SELECT status, builtin, active_version FROM plugins WHERE key = $1`, key).
			Scan(&status, &builtin, &active); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return status, builtin, active
	}

	for start := 1; start <= 2; start++ {
		if err := e.svc.EnsureBuiltin(ctx, dir, log); err != nil {
			t.Fatal(err)
		}
		status, builtin, active := state("guard")
		if !builtin || status != StatusInstalled || active != nil {
			t.Fatalf("start %d: install-only builtin status=%s builtin=%v active=%v", start, status, builtin, active)
		}
		if len(e.rollout.enabled) != 1 || e.rollout.enabled[0] != "guard_on" {
			t.Fatalf("start %d: the core enabled %v, want only guard_on", start, e.rollout.enabled)
		}
		if status, _, _ := state("guard_on"); status != StatusEnabled {
			t.Fatalf("start %d: unlisted builtin status=%s, want enabled", start, status)
		}
	}
	// Installed means ready to enable: the host permissions are already
	// granted, so the operator's enable needs no consent step.
	grants, err := LoadGrants(ctx, e.db.Gorm, "guard")
	if err != nil {
		t.Fatal(err)
	}
	for _, hp := range off.HostPermissions {
		if g, ok := grants[hp.ID]; !ok || g.Status != GrantGranted {
			t.Fatalf("grant %s = %+v", hp.ID, g)
		}
	}
	// Still built-in: it cannot be uninstalled either.
	if _, err := e.svc.Uninstall(ctx, "guard", UninstallOptions{}, e.admin); core.AsError(err).Code != core.ErrPermissionDenied.Code {
		t.Fatalf("uninstall install-only builtin: %v", err)
	}

	// The operator enables it; later starts leave that choice alone.
	if _, err := e.rollout.Enable(ctx, "guard", e.admin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnsureBuiltin(ctx, dir, log); err != nil {
		t.Fatal(err)
	}
	if status, _, active := state("guard"); status != StatusEnabled || active == nil || *active != "0.1.0" {
		t.Fatalf("after operator enable and restart: status=%s active=%v", status, active)
	}
}

// An install-only list that cannot be read is not an empty list: reading it
// as empty would enable, on every deployment, the plugins meant to stay off.
// Nothing is installed until it is fixed.
func TestEnsureBuiltinUnreadableInstallOnlyInstallsNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guard-0.1.0.s2plugin"), pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where the file should be: ReadFile fails, but not with ErrNotExist.
	if err := os.Mkdir(filepath.Join(dir, InstallOnlyFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnsureBuiltin(ctx, dir, testLogger(t)); err == nil {
		t.Fatal("EnsureBuiltin accepted an unreadable " + InstallOnlyFile)
	}
	var n int
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugins WHERE key = 'guard'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(e.rollout.enabled) != 0 {
		t.Fatalf("installed %d, enabled %v with an unreadable list", n, e.rollout.enabled)
	}
}
