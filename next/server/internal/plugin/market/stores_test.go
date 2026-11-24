package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// An administrator adds a third-party store with its default channel; a
// repository is one store's, and each store keeps its own default.
func TestCreateStore(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	st, err := s.CreateStore(ctx, StoreInput{Name: " 社区 ", Repo: "acme/plugins", Branch: "stable"}, 0)
	if err != nil || st.Name != "社区" || st.Official {
		t.Fatalf("create = %+v %v", st, err)
	}
	chs, err := s.ListSources(ctx, st.ID)
	if err != nil || len(chs) != 1 || !chs[0].IsDefault || chs[0].StoreName != "社区" ||
		chs[0].URL != "https://raw.githubusercontent.com/acme/plugins/index-stable/index.json" {
		t.Fatalf("channels = %+v %v", chs, err)
	}
	for repo, code := range map[string]string{"acme/plugins": core.ErrConflict.Code, OfficialRepo: core.ErrConflict.Code,
		"acme": core.ErrInvalidArgument.Code, "acme/..": core.ErrInvalidArgument.Code, "acme/x.git": core.ErrInvalidArgument.Code} {
		wantCode(t, "repo "+repo, code, func() error {
			_, err := s.CreateStore(ctx, StoreInput{Name: "x", Repo: repo, Branch: "main"}, 0)
			return err
		})
	}
	if def, err := s.source(ctx, 0); err != nil || !def.Official || def.Branch != "main" {
		t.Fatalf("default channel = %+v %v", def, err)
	}
}

// The official store can be renamed, not deleted; a third-party store is
// deleted with its channels.
func TestUpdateDeleteStore(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	official, name := listChannels(t, s)[0].StoreID, "官方"
	st, err := s.CreateStore(ctx, StoreInput{Name: "社区", Repo: "acme/plugins", Branch: "main"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.UpdateStore(ctx, official, StorePatch{Name: &name}, 0); err != nil || got.Name != name || got.Repo != OfficialRepo {
		t.Fatalf("rename official = %+v %v", got, err)
	}
	wantCode(t, "delete official", core.ErrConflict.Code, func() error { return s.DeleteStore(ctx, official, 0) })
	if err := s.DeleteStore(ctx, st.ID, 0); err != nil {
		t.Fatal(err)
	}
	if chs, err := s.ListSources(ctx, st.ID); err != nil || len(chs) != 0 {
		t.Fatalf("channels of a deleted store = %+v %v", chs, err)
	}
	wantCode(t, "rename gone", core.ErrNotFound.Code, func() error { _, err := s.UpdateStore(ctx, st.ID, StorePatch{Name: &name}, 0); return err })
}

// A third-party store's index rates its publishers at most community, in
// the listing and for an install, which carries the store.
func TestThirdPartyStoreTrust(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, _ := newMarket(t)
	st, err := s.CreateStore(ctx, StoreInput{Name: "社区", Repo: "acme/plugins", Branch: "main"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.ListSources(ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Served by the test market instead of GitHub.
	if _, err := s.db.SQL.ExecContext(ctx, `UPDATE market_sources SET url = $1 WHERE id = $2`, s.base+"/index-main/index.json", ch[0].ID); err != nil {
		t.Fatal(err)
	}
	l, err := s.ListPlugins(ctx, ch[0].ID, false)
	if err != nil || l.Plugins[0].Trust != pkg.TrustCommunity || l.Plugins[0].StoreID != st.ID || l.Plugins[0].StoreName != "社区" {
		t.Fatalf("listing = %+v %v", l, err)
	}
	_, opt, err := s.Download(ctx, ch[0].ID, "acme", "tool", "1.1.0")
	if err != nil || opt.Indexed.Trust != pkg.TrustCommunity || opt.Indexed.StoreID != st.ID {
		t.Fatalf("download = %+v %v", opt.Indexed, err)
	}
}
