package rollout

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

type historyState struct {
	State, Serving, Standby, Fallback, Rollout string
	RolloutID                                  int64
}

func historySignature(s core.PluginNodeState) historyState {
	return historyState{s.State, s.Serving, s.Standby, s.Fallback, s.Rollout, s.RolloutID}
}

// Called under recMu, after each completed reconcile. Error text alone does
// not trigger a new row, avoiding an unbounded stream on repeated retries.
func (c *Controller) recordNodeHistory(ctx context.Context, key string, s core.PluginNodeState) {
	signature := historySignature(s)
	if previous, ok := c.history[key]; ok && previous == signature {
		return
	}
	message, _ := json.Marshal(map[string]string{"standby": s.Standby, "fallback": s.Fallback, "error": s.Error, "rollout": s.Rollout})
	row := historyRow(key, s, c.o.Node.NodeID(), c.o.Node.BootID(), string(message))
	if err := store.G(ctx, c.o.DB.Gorm).Create(row).Error; err != nil {
		c.log.Warn("persist plugin node history failed", "plugin", key, "err", err)
		return
	}
	if c.history == nil {
		c.history = map[string]historyState{}
	}
	c.history[key] = signature
}

// historyRow is the plugin_history row of a node state; rollout 0 is none
// (NULL).
func historyRow(key string, s core.PluginNodeState, nodeID, bootID, message string) *model.PluginHistory {
	row := &model.PluginHistory{PluginKey: key, NodeID: nodeID, BootID: bootID, State: s.State, Version: s.Serving, Message: message}
	if s.RolloutID != 0 {
		row.RolloutID = &s.RolloutID
	}
	return row
}

// pruneHistory deletes the oldest history rows past the 30 days.
func pruneHistory(g *gorm.DB) *gorm.DB {
	// Bounded deletion avoids long transactions on busy clusters.
	return g.Where("id IN (?)", g.Model(&model.PluginHistory{}).Select("id").
		Where("created_at < now() - interval '30 days'").Order("created_at").Limit(10000)).
		Delete(&model.PluginHistory{})
}

func (c *Controller) cleanHistory(ctx context.Context) {
	if time.Since(c.historyCleaned) < time.Hour {
		return
	}
	if err := pruneHistory(store.G(ctx, c.o.DB.Gorm)).Error; err != nil {
		c.log.Warn("clean plugin history failed", "err", err)
		return
	}
	c.historyCleaned = time.Now()
}
