package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The official store is the only market source: its row defaults to the
// official index, takes SUB2API_MARKET_URL keeping its id, and every other
// row goes.
func TestEnsureOfficial(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO market_sources (name, url) VALUES ('local-dev', 'http://market:8080/index.json'), ('community', 'https://example.com/index.json')`); err != nil {
		t.Fatal(err)
	}
	s := New(db, fakeInstaller{}, nil, Options{})
	want := func(url string) int64 {
		t.Helper()
		srcs, err := s.ListSources(ctx)
		if err != nil || len(srcs) != 1 || srcs[0].Name != OfficialName || srcs[0].URL != url {
			t.Fatalf("sources = %+v %v, want only official at %s", srcs, err, url)
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
