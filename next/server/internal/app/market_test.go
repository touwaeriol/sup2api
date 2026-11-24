package app

import (
	"context"
	"testing"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// marketCase is one start of ensureMarket: the node's managed state, its
// SUB2API_MARKET_BASE_URL and whether it brings back the default channel
// or fails.
type marketCase struct {
	name    string
	managed *managedCore
	url     string
	writes  bool
	fails   bool
}

const testStore = "https://store.example/s"

// Every node checks SUB2API_MARKET_BASE_URL at start; only a core that may
// coordinate plugins (unmanaged, bootstrap, managed primary) brings back a
// lost default channel, whose index follows the base.
func TestEnsureMarketGating(t *testing.T) {
	preparing := func(p rc.PrepareRequest) *managedCore { return &managedCore{mode: "preparing", prepare: p} }
	for _, tc := range []marketCase{
		{name: "unmanaged", url: testStore, writes: true},
		{name: "bootstrap", managed: preparing(rc.PrepareRequest{Bootstrap: true}), url: testStore, writes: true},
		{name: "primary", managed: preparing(rc.PrepareRequest{CoordinatePlugins: true}), url: testStore, writes: true},
		{name: "secondary", managed: preparing(rc.PrepareRequest{}), url: testStore},
		{name: "secondary validates", managed: preparing(rc.PrepareRequest{}), url: "http://store.example/", fails: true},
	} {
		t.Run(tc.name, tc.run)
	}
}

func (tc marketCase) run(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM market_sources`); err != nil {
		t.Fatal(err)
	}
	mkt := market.New(db, nil, nil, market.Options{BaseURL: tc.url})
	b := &boot{ctx: ctx, cfg: &config.Config{}, managed: tc.managed, mkt: mkt}
	if err := b.ensureMarket(); (err != nil) != tc.fails {
		t.Fatalf("ensureMarket = %v, want failure %v", err, tc.fails)
	}
	srcs, err := mkt.ListSources(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if wrote := len(srcs) == 1 && srcs[0].IsDefault && srcs[0].URL == testStore+"/index-main/index.json"; wrote != tc.writes || len(srcs) > 1 {
		t.Fatalf("sources = %+v, want written %v", srcs, tc.writes)
	}
}
