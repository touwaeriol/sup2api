package rollout_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// TestRealRuntimeSingleNode drives the controller with real plugin processes.
func TestRealRuntimeSingleNode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.bin = registrytest.TestPlugin(t)
	registrytest.Install(t, h.db, h.manifest("1.0.0"), h.pkg("1.0.0"), registrytest.DefaultGrants(), "installed")
	t.Cleanup(func() { _ = dbschema.New(h.db, "", nil).Drop(context.Background(), h.key) })

	mk := make([]byte, 32)
	cipher, _ := secret.New(mk)
	nd := &registrytest.Node{RDB: h.rdb, ID: "solo", Boot: "boot-solo"}
	nd.Heartbeat(ctx)
	grt, err := grpcruntime.New(grpcruntime.Options{DB: h.db, Redis: h.rdb, Cipher: cipher, Node: nd,
		Launcher: &registrytest.Launcher{}, DataDir: t.TempDir(), DrainTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := registry.NewPackages(h.db, t.TempDir(), registry.WithSource(registrytest.Source()))
	t.Cleanup(pkgs.Close)
	reg := registry.New()
	ctl, err := rollout.New(rollout.Options{DB: h.db, Node: nd, Bus: registrytest.Bus{RDB: h.rdb}, Packages: pkgs,
		Registry: reg, Runtime: rollout.FromGRPC(grt),
		Schemas:           dbschema.New(h.db, testutil.DSN(h.db), nil),
		ReconcileInterval: 200 * time.Millisecond, LeaseTTL: 8 * time.Second, LeaseRenew: time.Second,
		CoordinatorTick: 100 * time.Millisecond, DrainTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctl.Start(ctx)
	t.Cleanup(func() { ctl.Stop(context.Background()) })

	// The gateway calls the plugin declaring the account type.
	call := func() map[string]string {
		b, ok := reg.Current().AccountType(h.key, "apikey")
		if !ok {
			return nil
		}
		res, err := b.Client.Execute(ctx, &pluginv1.ExecuteRequest{}, core.ExecutionClient{})
		if err != nil {
			t.Fatalf("account type call: %v", err)
		}
		return res.Relay.GetHeaders()
	}

	if _, err := ctl.Enable(ctx, h.key, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "enabled", func() bool { s, _, _ := h.plugin(); return s == "enabled" })
	first := call()
	if first == nil || first["x-calls"] != "1" {
		t.Fatalf("first call: %v", first)
	}

	registrytest.AddVersion(t, h.db, h.manifest("2.0.0"), h.pkg("2.0.0"))
	if _, err := ctl.Upgrade(ctx, h.key, "2.0.0", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "upgraded", func() bool {
		s, act, _ := h.plugin()
		p, _ := reg.Current().Plugin(h.key)
		return s == "enabled" && act == "2.0.0" && p.Version == "2.0.0"
	})
	second := call()
	if second["x-pid"] == first["x-pid"] || second["x-calls"] != "2" {
		t.Fatalf("after upgrade: %v (before %v)", second, first)
	}
	// MigrateData ran on the new version (the test plugin records it in KV).
	if v, _ := h.rdb.Get(ctx, "plugin:kv:"+h.key+":t:migrated").Result(); v != "1.0.0->2.0.0" {
		t.Fatalf("migrated = %q", v)
	}
	// The package of the old version is closed and removed once drained.
	waitFor(t, "old package released", func() bool {
		refs := pkgs.Cached()
		return len(refs) == 1 && refs[0].Version == "2.0.0"
	})

	// Resource limits apply at once: a new process replaces the instance.
	if _, err := h.db.SQL.ExecContext(ctx, `UPDATE plugins SET resource_limits = '{"memory_mb": 300}' WHERE key = $1`, h.key); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rollout.Message{Type: rollout.MessageResources, PluginKey: h.key})
	if err := (registrytest.Bus{RDB: h.rdb}).Publish(ctx, core.ChannelPluginEvents, b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted with new limits", func() bool {
		third := call()
		return third != nil && third["x-pid"] != second["x-pid"]
	})

	// A broadcast from another node reaches the local instance.
	msg, _ := json.Marshal(grpcruntime.BroadcastMessage{Topic: "rules.changed", Payload: []byte("x"), SourceNodeID: "other", SourceBootID: "boot-other"})
	waitFor(t, "broadcast delivered", func() bool {
		_ = (registrytest.Bus{RDB: h.rdb}).Publish(ctx, grpcruntime.BroadcastChannel(h.key), msg)
		v, _ := h.rdb.Get(ctx, "plugin:kv:"+h.key+":t:broadcast").Result()
		return v == "rules.changed:x:other"
	})

	if _, err := ctl.Disable(ctx, h.key, 0, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "disabled", func() bool {
		_, ok := reg.Current().Platform("p_" + h.key)
		_, typeOK := reg.Current().AccountType(h.key, "apikey")
		return !ok && !typeOK
	})
}
