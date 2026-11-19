package cluster

import (
	"context"
	"time"

	"gorm.io/gorm"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

type PluginMutations struct {
	DB     *store.DB
	Locker core.Locker
	// AllowBootstrap is evaluated together with a host-only context marker.
	AllowBootstrap func() bool
}
type mutationScopeKey struct{}

func (g *PluginMutations) Begin(ctx context.Context) (context.Context, func(), error) {
	noop := func() {}
	if ctx.Value(mutationScopeKey{}) == g {
		return ctx, noop, ctx.Err()
	}
	if core.IsPluginBootstrap(ctx) && g.AllowBootstrap != nil && g.AllowBootstrap() {
		return ctx, noop, nil
	}
	if g.Locker == nil {
		return ctx, noop, core.ErrUnavailable.WithMessage("cluster change lock unavailable")
	}
	work, cancel := context.WithTimeout(ctx, 25*time.Second)
	lk, ok, err := g.Locker.TryLock(work, rc.ClusterChangeLock, 30*time.Second)
	if err != nil || !ok {
		cancel()
		if err != nil {
			return ctx, noop, err
		}
		return ctx, noop, core.ErrClusterChangeBusy
	}
	work, stop := core.KeepLock(work, lk)
	done := func() { stop(); cancel(); lk.Release() }
	busy, err := CoreUpdateBusy(work, g.DB.Gorm)
	if err == nil && busy {
		err = core.ErrConflict.WithMessage("core update is running or paused; finish or cancel it before changing plugins")
	}
	if err != nil {
		done()
		return ctx, noop, err
	}
	return context.WithValue(work, mutationScopeKey{}, g), done, nil
}

// CoreUpdateBusy also works on installations without a managed shell.
// Callers must fail closed on errors. The mutation gate still rechecks under
// the shared lock: this preflight alone does not authorize a plugin change.
func CoreUpdateBusy(ctx context.Context, q *gorm.DB) (bool, error) {
	db := store.G(ctx, q)
	// The managed shell's updater schema is not part of the core schema.
	installed, err := gormpg.RelationExists(db, "updater.upgrades")
	if err != nil || !installed {
		return false, err
	}
	return gormpg.Exists(db, upgradeActive(db))
}

// upgradeActive selects the core updates running or paused.
func upgradeActive(db *gorm.DB) *gorm.DB {
	return db.Table("updater.upgrades").Where("status IN ('running', 'paused')")
}
