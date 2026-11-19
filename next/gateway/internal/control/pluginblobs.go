package control

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/pluginblob"
)

// PluginBlobs builds the plugin package service of node. Packages live under
// root/plugin-blobs; client is the authenticated node client that may reach
// any enabled node, since a missing package is fetched from whichever online
// node holds it.
func PluginBlobs(store *Store, node, root string, client *http.Client, maxBytes, maxTotalBytes int64) *pluginblob.Service {
	return &pluginblob.Service{
		Store:  &pluginblob.Store{Dir: filepath.Join(root, "plugin-blobs"), MaxBytes: maxBytes, MaxTotalBytes: maxTotalBytes},
		Client: client,
		Peers: func(ctx context.Context) ([]pluginblob.NodeRef, error) {
			return packagePeers(ctx, store, node)
		},
	}
}

// packagePeers lists the enabled nodes other than self, each marked online
// by its heartbeat (Node.Fresh). Which node holds which package is not
// recorded: every listed node may be asked, the online ones first, and one
// without the package answers 404.
func packagePeers(ctx context.Context, store *Store, self string) ([]pluginblob.NodeRef, error) {
	nodes, err := store.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var refs []pluginblob.NodeRef
	for _, n := range nodes {
		if n.Enabled && n.ID != self && n.PeerURL != "" {
			refs = append(refs, pluginblob.NodeRef{ID: n.ID, PeerURL: strings.TrimRight(n.PeerURL, "/"), Online: n.Fresh(now)})
		}
	}
	return refs, nil
}

// ManagementHandler serves the local management socket: the updater API and
// the plugin packages of this node's core. The caller wraps it in
// RequireManagementToken.
func ManagementHandler(store *Store, blobs *pluginblob.Service, engine *Engine) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/system/plugin-blobs/", blobs.Local())
	if engine != nil {
		engine.yieldHandlers(mux)
	}
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		nodes, err := store.Nodes(ctx)
		if err != nil {
			http.Error(w, "failed to query nodes", http.StatusInternalServerError)
			return
		}
		enabled := 0
		ready := 0
		for _, n := range nodes {
			if n.Enabled {
				enabled++
				if n.Ready && n.Mode == "local" {
					ready++
				}
			}
		}
		planID, planStatus, blockedReason, err := store.PlanStatus(ctx)
		if err != nil {
			// Keep the endpoint's historical 200 + zero gauges, but stop hiding the failure.
			slog.Warn("plan status unavailable for metrics", "error", err)
			planID, planStatus, blockedReason = "", "", ""
		}
		planActive := 0
		if planStatus == "running" || planStatus == "paused" {
			planActive = 1
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, "# HELP gateway_nodes_enabled Number of enabled nodes in the cluster\n")
		fmt.Fprintf(w, "# TYPE gateway_nodes_enabled gauge\n")
		fmt.Fprintf(w, "gateway_nodes_enabled %d\n", enabled)
		fmt.Fprintf(w, "# HELP gateway_nodes_ready Number of ready nodes serving locally\n")
		fmt.Fprintf(w, "# TYPE gateway_nodes_ready gauge\n")
		fmt.Fprintf(w, "gateway_nodes_ready %d\n", ready)
		fmt.Fprintf(w, "# HELP gateway_upgrade_active Whether an upgrade plan is running or paused (1) or not (0)\n")
		fmt.Fprintf(w, "# TYPE gateway_upgrade_active gauge\n")
		fmt.Fprintf(w, "gateway_upgrade_active %d\n", planActive)
		if blockedReason != "" && planID != "" {
			fmt.Fprintf(w, "# HELP gateway_upgrade_blocked Whether the upgrade is blocked (1) or not (0)\n")
			fmt.Fprintf(w, "# TYPE gateway_upgrade_blocked gauge\n")
			fmt.Fprintf(w, "gateway_upgrade_blocked{reason=%q} 1\n", blockedReason)
		}
	})
	mux.Handle("/", store.Handler())
	return mux
}

// CollectPluginBlobs removes packages no version references any more, every
// interval, keeping recent ones whose upload may still be committing.
func CollectPluginBlobs(ctx context.Context, store *Store, blobs *pluginblob.Service, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		referenced, err := store.ReferencedPackages(ctx)
		if err != nil {
			continue
		}
		if n, err := blobs.Store.GC(referenced, time.Hour); n > 0 || err != nil {
			slog.Info("plugin package cleanup", "removed", n, "error", err)
		}
	}
}
