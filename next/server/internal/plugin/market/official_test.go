package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The official store is the only market source: its row defaults to the
// official index, takes SUB2API_MARKET_URL keeping its id, is enabled again,
// and every other row goes.
func TestEnsureOfficial(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO market_sources (name, url, enabled) VALUES ('official', 'https://old.example/', false), ('local-dev', 'http://market:8080/index.json', true), ('community', 'https://example.com/index.json', true)`); err != nil {
		t.Fatal(err)
	}
	s := New(db, fakeInstaller{}, nil, Options{})
	want := func(url string) int64 {
		t.Helper()
		srcs, err := s.ListSources(ctx)
		if err != nil || len(srcs) != 1 || srcs[0].Name != OfficialName || srcs[0].URL != url || !srcs[0].Enabled {
			t.Fatalf("sources = %+v %v, want only official enabled at %s", srcs, err, url)
		}
		return srcs[0].ID
	}
	if err := s.EnsureOfficial(ctx, ""); err != nil {
		t.Fatal(err)
	}
	id := want(OfficialURL)
	if err := s.EnsureOfficial(ctx, "https://mirror.example/m/"); err != nil {
		t.Fatal(err)
	}
	if want("https://mirror.example/m/") != id {
		t.Fatal("the official source changed its id")
	}
}

// A listing without a source id reads the official store and returns its
// errors: here it has no row.
func TestListPluginsOfficialMissing(t *testing.T) {
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	_, err := s.ListPlugins(context.Background(), 0, false)
	if e := core.AsError(err); e == nil || e.Code != core.ErrNotFound.Code {
		t.Fatalf("list without the official store = %v, want not found", err)
	}
}
