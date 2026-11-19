package install

import (
	"context"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

type builtinTestGeneration struct {
	core.Generation
	version string
}

func (g builtinTestGeneration) Plugin(key string) (core.PluginInfo, bool) {
	return core.PluginInfo{Key: key, Version: g.version}, g.version != ""
}

func TestBuiltinReadinessDuringUpgrade(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, version := range []string{"0.0.5", "0.1.0", "0.2.0"} {
		data := pkgtest.Build(pkgtest.Guard("guard", version, "sub2api"), e.root)
		if _, err := e.svc.Upload(ctx, data, e.admin, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]BuiltinRequirement{"guard": {Version: semver.MustParse("0.1.0")}}
	for _, tc := range []struct {
		name, status, active, local, unapproved string
		ready                                   bool
	}{
		{"before upgrade", "enabled", "0.1.0", "0.1.0", "", true},
		{"preparing keeps serving old", "upgrading", "0.1.0", "0.1.0", "", true},
		{"commit before local switch", "upgrading", "0.2.0", "0.1.0", "", true},
		{"local switch before database snapshot", "upgrading", "0.1.0", "0.2.0", "", true},
		{"completion after generation snapshot", "enabled", "0.2.0", "0.1.0", "", true},
		{"after upgrade", "enabled", "0.2.0", "0.2.0", "", true},
		{"missing local instance", "upgrading", "0.2.0", "", "", false},
		{"local below image minimum", "upgrading", "0.2.0", "0.0.5", "", false},
		{"active below image minimum", "enabled", "0.0.5", "0.1.0", "", false},
		{"image version unapproved", "upgrading", "0.2.0", "0.2.0", "0.1.0", false},
		{"active version unapproved", "upgrading", "0.2.0", "0.1.0", "0.2.0", false},
		{"local version unapproved", "upgrading", "0.1.0", "0.2.0", "0.2.0", false},
		{"initial enable still waits", "enabling", "0.1.0", "0.1.0", "", false},
		{"operator disabled remains optional", "disabled", "0.2.0", "", "0.2.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status='approved' WHERE plugin_key='guard'`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugins SET builtin=true,status=$1,active_version=$2 WHERE key='guard'`, tc.status, tc.active); err != nil {
				t.Fatal(err)
			}
			if tc.unapproved != "" {
				if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status='awaiting_consent' WHERE plugin_key='guard' AND version=$1`, tc.unapproved); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := e.svc.BuiltinsReady(ctx, want, builtinTestGeneration{version: tc.local})
			if err != nil || ready != tc.ready {
				t.Fatalf("ready=%v, err=%v; want ready=%v", ready, err, tc.ready)
			}
			// The cluster-level check ignores this node's instances: a lagging
			// node catches up on its own and must not keep the bundle upgrade busy.
			committed, err := e.svc.BuiltinsCommitted(ctx, want)
			wantCommitted := tc.ready
			switch tc.name {
			case "missing local instance", "local below image minimum", "local version unapproved":
				wantCommitted = true
			}
			if err != nil || committed != wantCommitted {
				t.Fatalf("committed=%v, err=%v; want %v", committed, err, wantCommitted)
			}
		})
	}
}

// BuiltinsInstalled is whether EnsureBuiltin has anything left to do, for
// every plugin status: a rollout in progress belongs to the rollout.
func TestBuiltinsInstalledByStatus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, version := range []string{"0.0.5", "0.1.0", "0.2.0"} {
		data := pkgtest.Build(pkgtest.Guard("guard", version, "sub2api"), e.root)
		if _, err := e.svc.Upload(ctx, data, e.admin, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, status string
		active       any
		installOnly  bool
		unapproved   string
		notBuiltin   bool
		installed    bool
	}{
		{name: "no built-in row: EnsureBuiltin uploads and marks it", status: "enabled", active: "0.1.0", notBuiltin: true},
		{name: "bundled version unapproved: EnsureBuiltin approves it", status: "enabled", active: "0.2.0", unapproved: "0.1.0"},
		{name: "awaiting consent of another version", status: "awaiting_consent", active: nil, installed: true},
		{name: "installed, never enabled: EnsureBuiltin enables it", status: "installed", active: nil},
		{name: "installed install-only stays off", status: "installed", active: nil, installOnly: true, installed: true},
		{name: "first enable in progress", status: "enabling", active: nil, installed: true},
		{name: "install-only enable in progress", status: "enabling", active: nil, installOnly: true, installed: true},
		{name: "re-enable in progress", status: "enabling", active: "0.1.0", installed: true},
		{name: "upgrade in progress", status: "upgrading", active: "0.1.0", installed: true},
		{name: "enabled below the bundle: EnsureBuiltin upgrades it", status: "enabled", active: "0.0.5"},
		{name: "enabled at the bundle", status: "enabled", active: "0.1.0", installed: true},
		{name: "enabled above the bundle", status: "enabled", active: "0.2.0", installed: true},
		{name: "disabled by an operator", status: "disabled", active: "0.1.0", installed: true},
		{name: "disabled for an uninstall", status: "disabled", active: nil, installed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status='approved' WHERE plugin_key='guard'`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugins SET builtin=$1,status=$2,active_version=$3 WHERE key='guard'`, !tc.notBuiltin, tc.status, tc.active); err != nil {
				t.Fatal(err)
			}
			if tc.unapproved != "" {
				if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status='awaiting_consent' WHERE plugin_key='guard' AND version=$1`, tc.unapproved); err != nil {
					t.Fatal(err)
				}
			}
			want := map[string]BuiltinRequirement{"guard": {Version: semver.MustParse("0.1.0"), InstallOnly: tc.installOnly}}
			installed, err := e.svc.BuiltinsInstalled(ctx, want)
			if err != nil || installed != tc.installed {
				t.Fatalf("installed=%v, err=%v; want %v", installed, err, tc.installed)
			}
		})
	}
}
