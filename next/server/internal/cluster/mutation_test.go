package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestPluginMutationsPersistedPlansAndSharedLock(t *testing.T) {
	db := testutil.DB(t)
	_, rdb := newRedis(t)
	ctx := context.Background()
	g := &PluginMutations{DB: db, Locker: NewLocker(rdb, nil)}
	// Legacy mode works before the shell schema is installed.
	work, done, err := g.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, nested, nerr := g.Begin(work)
	if nerr != nil {
		t.Fatal(nerr)
	}
	nested()
	if rdb.Exists(ctx, "lock:"+rc.ClusterChangeLock).Val() != 1 {
		t.Fatal("nested release dropped shared lock")
	}
	if _, other, err := g.Begin(ctx); !errors.Is(err, core.ErrClusterChangeBusy) {
		other()
		t.Fatalf("parallel mutation: %v", err)
	}
	done()
	if _, err = db.SQL.ExecContext(ctx, `CREATE SCHEMA updater;CREATE TABLE updater.upgrades(status text);INSERT INTO updater.upgrades VALUES('paused')`); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"paused", "running"} {
		if _, err = db.SQL.ExecContext(ctx, `UPDATE updater.upgrades SET status=$1`, phase); err != nil {
			t.Fatal(err)
		}
		if _, release, err := g.Begin(ctx); err == nil || core.AsError(err).Code != core.ErrConflict.Code {
			release()
			t.Fatalf("%s plan admitted plugin: %v", phase, err)
		}
	}
	g.AllowBootstrap = func() bool { return true }
	if _, release, err := g.Begin(core.WithPluginBootstrap(ctx)); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	g.AllowBootstrap = func() bool { return false }
	if _, release, err := g.Begin(core.WithPluginBootstrap(ctx)); err == nil {
		release()
		t.Fatal("bootstrap bypass outlived preparation")
	}
	if _, err = db.SQL.ExecContext(ctx, `UPDATE updater.upgrades SET status='complete'`); err != nil {
		t.Fatal(err)
	}
	lk, ok, err := g.Locker.TryLock(ctx, rc.ClusterChangeLock, time.Second)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, release, err := g.Begin(ctx); !errors.Is(err, core.ErrClusterChangeBusy) {
		release()
		t.Fatalf("shell submission lock ignored: %v", err)
	}
	lk.Release()
	_, done, err = g.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done()
}

func TestManagedRegistryPublishesSupervisorIdentity(t *testing.T) {
	_, rdb := newRedis(t)
	ctx := context.Background()
	c := New(rdb, nil, Options{NodeID: "node", Managed: true, CoreBootID: "core-boot"})
	if err := c.Registry.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	info := rdb.HGetAll(ctx, "node:info:core-boot").Val()
	if info["managed"] != "true" || info["core_boot_id"] != "core-boot" {
		t.Fatal(info)
	}
	old := New(rdb, nil, Options{NodeID: "legacy"})
	if err := old.Registry.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	info = rdb.HGetAll(ctx, "node:info:"+old.Registry.BootID()).Val()
	if info["managed"] != "false" || info["core_boot_id"] != "" {
		t.Fatal(info)
	}
}
