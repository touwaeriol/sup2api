package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// Migration 0125 seeds the official store with the channels 正式 (main,
// default) and 测试 (develop). EnsureStores points every channel of the
// official store at the base keeping its id, and brings its default channel
// back when the database has none.
func TestEnsureStores(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	s := New(db, fakeInstaller{}, nil, Options{BaseURL: "https://mirror.example/m/"})
	if err := s.EnsureStores(ctx); err != nil {
		t.Fatal(err)
	}
	srcs := listChannels(t, s)
	if srcs[0].Name != DefaultChannelName || srcs[0].Branch != "main" || !srcs[0].IsDefault || srcs[0].StoreName != OfficialStoreName ||
		srcs[0].URL != "https://mirror.example/m/index-main/index.json" || srcs[1].Branch != "develop" ||
		srcs[1].URL != "https://mirror.example/m/index-develop/index.json" || srcs[1].IsDefault {
		t.Fatalf("channels = %+v", srcs)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE market_sources SET is_default = false, enabled = false`); err != nil {
		t.Fatal(err)
	}
	official := New(db, fakeInstaller{}, nil, Options{})
	if err := official.EnsureStores(ctx); err != nil {
		t.Fatal(err)
	}
	again := listChannels(t, official)
	if again[0].ID != srcs[0].ID || !again[0].IsDefault || !again[0].Enabled ||
		again[0].URL != OfficialBase+"/index-main/index.json" {
		t.Fatalf("channels after = %+v", again)
	}
}

// A listing without a channel reads the default one and returns its
// errors: here there is none.
func TestListPluginsDefaultMissing(t *testing.T) {
	db := testutil.DB(t)
	if _, err := db.SQL.ExecContext(context.Background(), `DELETE FROM market_sources`); err != nil {
		t.Fatal(err)
	}
	_, err := New(db, fakeInstaller{}, nil, Options{}).ListPlugins(context.Background(), 0, false)
	if e := core.AsError(err); e == nil || e.Code != core.ErrNotFound.Code {
		t.Fatalf("list without a default channel = %v, want not found", err)
	}
}

// listChannels lists the two channels of s.
func listChannels(t *testing.T, s *Service) []Source {
	t.Helper()
	srcs, err := s.ListSources(context.Background(), 0)
	if err != nil || len(srcs) != 2 {
		t.Fatalf("channels = %+v %v", srcs, err)
	}
	return srcs
}
