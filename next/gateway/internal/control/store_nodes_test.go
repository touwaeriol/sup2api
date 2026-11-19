package control

import (
	"context"
	"strings"
	"testing"
)

// remove-node, set-primary and the plugin package sources used tables that
// the gateway schema never created (updater.node_artifacts,
// updater.node_plugin_packages); each failed with "relation does not exist".
// Package sources are the other enabled nodes, whatever the primary is.
func TestPostgresRemoveNodeSetPrimaryAndPackageFallback(t *testing.T) {
	s, a, b, _ := setupEngines(t)
	ctx := context.Background()
	s.RevokePeer = func(context.Context, string) error { return nil }

	for self, other := range map[string]string{a.Node.ID: b.Node.ID, b.Node.ID: a.Node.ID} {
		peers, err := packagePeers(ctx, s, self)
		if err != nil || len(peers) != 1 || peers[0].ID != other || peers[0].PeerURL == "" {
			t.Fatalf("package sources of %s: %v %v", self, peers, err)
		}
	}

	if err := s.SetPrimary(ctx, b.Node.ID); err != nil {
		t.Fatalf("set-primary: %v", err)
	}
	if primary, _, _, _ := s.ClusterState(ctx); primary != b.Node.ID {
		t.Fatalf("primary = %s", primary)
	}
	if err := s.SetPrimary(ctx, a.Node.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveNode(ctx, b.Node.ID); err == nil || !strings.Contains(err.Error(), "still enabled") {
		t.Fatalf("enabled node removed: %v", err)
	}
	if err := s.DisableNode(ctx, b.Node.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db(ctx).Exec(`UPDATE updater.nodes SET last_seen=now()-interval '10 minutes' WHERE cluster_id=? AND node_id=?`, s.Cluster, b.Node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrimary(ctx, b.Node.ID); err == nil {
		t.Fatal("disabled node became primary")
	}
	if peers, _ := packagePeers(ctx, s, a.Node.ID); len(peers) != 0 {
		t.Fatalf("disabled node offered as package source: %v", peers)
	}
	if err := s.RemoveNode(ctx, b.Node.ID); err != nil {
		t.Fatalf("remove-node: %v", err)
	}
	var left int
	if err := s.db(ctx).Raw(`SELECT (SELECT count(*) FROM updater.nodes WHERE cluster_id=? AND node_id=?) + (SELECT count(*) FROM updater.node_admissions WHERE cluster_id=? AND node_id=?)`, s.Cluster, b.Node.ID, s.Cluster, b.Node.ID).Row().Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows left %d %v", left, err)
	}
	if err := s.RemoveNode(ctx, a.Node.ID); err == nil {
		t.Fatal("enabled primary removed")
	}
}

// A node disabled by hand (the second node of a cluster cut down to one) is
// left out of a core plan instead of joining it and pausing it at its first
// step ("planned node is disabled").
func TestPostgresPreflightLeavesDisabledNodesOut(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	s.RevokePeer = func(context.Context, string) error { return nil }
	if err := s.DisableNode(ctx, b.Node.ID); err != nil {
		t.Fatal(err)
	}
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Nodes) != 1 || pf.Nodes[0] != a.Node.ID || len(pf.Blockers) != 0 {
		t.Fatalf("preflight nodes %v, blockers %v", pf.Nodes, pf.Blockers)
	}
}
