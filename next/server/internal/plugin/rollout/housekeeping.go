package rollout

import (
	"context"
	"errors"
	"sort"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// drainAsync drains (or stops) an instance that left the slot. While it
// drains, its package stays cached; afterwards unused packages are swept.
// It only takes drainMu (a leaf lock), so callers may hold c.mu or slot.mu.
func (c *Controller) drainAsync(key, version string, inst Instance, stop bool) {
	id := key + "@" + version
	c.drainMu.Lock()
	c.draining[id]++
	c.drainMu.Unlock()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if stop {
			inst.Stop()
		} else {
			inst.Drain(c.o.DrainTimeout)
		}
		c.drainMu.Lock()
		c.draining[id]--
		if c.draining[id] <= 0 {
			delete(c.draining, id)
		}
		c.drainMu.Unlock()
		c.sweepPackages()
	}()
}

// setReferenced records the versions the database still refers to:
// active, desired and both sides of an open rollout (the standby).
func (c *Controller) setReferenced(rows []*pluginRow) {
	ref := map[string]map[string]bool{}
	add := func(key string, v *string) {
		if v == nil || *v == "" {
			return
		}
		if ref[key] == nil {
			ref[key] = map[string]bool{}
		}
		ref[key][*v] = true
	}
	for _, p := range rows {
		add(p.key, p.active)
		add(p.key, p.desired)
		if p.ro != nil {
			add(p.key, p.ro.from)
			add(p.key, p.ro.target)
		}
	}
	c.mu.Lock()
	c.referenced = ref
	c.mu.Unlock()
}

// sweepPackages releases cached packages (file handle and unpacked
// directory) of versions that are not referenced, have no local instance
// and no instance still draining.
func (c *Controller) sweepPackages() {
	for _, ref := range c.o.Packages.Cached() {
		if c.packageInUse(ref.Key, ref.Version) {
			continue
		}
		err := c.o.Packages.Release(ref.Key, ref.Version)
		if errors.Is(err, registry.ErrPackageInUse) {
			continue // read right now (a migration); the next sweep retries
		}
		if err != nil {
			c.log.Warn("remove unused plugin package failed", "plugin", ref.Key, "version", ref.Version, "err", err)
			continue
		}
		c.log.Info("removed unused plugin package", "plugin", ref.Key, "version", ref.Version)
	}
}

func (c *Controller) packageInUse(key, version string) bool {
	c.mu.Lock()
	if c.stopped || c.referenced[key][version] {
		c.mu.Unlock()
		return true
	}
	s := c.slots[key]
	c.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		_, local := s.entries[version]
		s.mu.Unlock()
		if local {
			return true
		}
	}
	// Checked after the slot: an entry leaves the slot only after its
	// drain was counted.
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	return c.draining[key+"@"+version] > 0
}

// pruneOrphans removes package directories of versions the database no
// longer refers to (left over from earlier runs). Runs once at Start.
func (c *Controller) pruneOrphans(ctx context.Context) {
	keep, err := c.referencedPackages(ctx)
	if err != nil {
		c.log.Warn("plugin package cleanup skipped", "err", err)
		return
	}
	removed, err := c.o.Packages.PruneOrphans(func(key, versionHash string) bool {
		return keep[key+"@"+versionHash]
	})
	for _, d := range removed {
		c.log.Info("removed orphaned plugin package directory", "dir", d)
	}
	if err != nil {
		c.log.Warn("plugin package cleanup incomplete", "err", err)
	}
}

// referencedPackages returns "key@<version hash>" of every package version
// named by plugins.active_version/desired_version or an open rollout.
func (c *Controller) referencedPackages(ctx context.Context) (map[string]bool, error) {
	var rows []model.PluginVersion
	if err := referencedVersions(store.G(ctx, c.o.DB.Gorm)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, v := range rows {
		if sum := v.PackageSha256; len(sum) >= 8 {
			out[v.PluginKey+"@"+core.PluginVersionHash(v.Version, sum)] = true
		}
	}
	return out, nil
}

// referencedVersions selects the package of every version a plugin or an
// open rollout names.
func referencedVersions(g *gorm.DB) *gorm.DB {
	return g.Table("plugin_versions v").Select("v.plugin_key, v.version, v.package_sha256").
		Where(`EXISTS (SELECT 1 FROM plugins p WHERE p.key = v.plugin_key
				AND (v.version = p.active_version OR v.version = p.desired_version))
			OR EXISTS (SELECT 1 FROM plugin_rollouts r WHERE r.plugin_key = v.plugin_key
				AND r.phase IN ('preparing','activating')
				AND (v.version = r.from_version OR v.version = r.target_version))`)
}

// servingInstance returns the instance of key currently in the generation.
func (c *Controller) servingInstance(key string) Instance {
	c.mu.Lock()
	s := c.slots[key]
	c.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[s.serving]; s.serving != "" && e != nil {
		return e.inst
	}
	return nil
}

// eachSlot calls fn with every slot, under the slot's lock.
func (c *Controller) eachSlot(fn func(s *slot)) {
	c.mu.Lock()
	slots := make([]*slot, 0, len(c.slots))
	for _, s := range c.slots {
		slots = append(slots, s)
	}
	c.mu.Unlock()
	for _, s := range slots {
		s.mu.Lock()
		fn(s)
		s.mu.Unlock()
	}
}

// servingInstances returns the instance each plugin served here has in the
// generation, by plugin key.
func (c *Controller) servingInstances() map[string]Instance {
	out := map[string]Instance{}
	c.eachSlot(func(s *slot) {
		if e := s.entries[s.serving]; s.serving != "" && e != nil && e.inst != nil {
			out[s.key] = e.inst
		}
	})
	return out
}

// keyedInstance is an instance with its plugin key.
type keyedInstance struct {
	key  string
	inst Instance
}

// loadedInstances returns every instance loaded here, the one in the
// generation and those of a rollout alike.
func (c *Controller) loadedInstances() []keyedInstance {
	var out []keyedInstance
	c.eachSlot(func(s *slot) {
		for _, e := range s.entries {
			if e.inst != nil {
				out = append(out, keyedInstance{s.key, e.inst})
			}
		}
	})
	return out
}

// syncBroadcast subscribes to the broadcast channels of the plugins served
// here that declare app.broadcast.v1.
func (c *Controller) syncBroadcast() {
	var keys []string
	for key, inst := range c.servingInstances() {
		if r, ok := inst.(broadcastReceiver); ok && r.HandlesBroadcast() {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	c.hub.sync(keys)
}
