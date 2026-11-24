package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// An administrator adds channels to a store; names and branches are each
// one channel's of the store, and a branch is one git ref name segment.
func TestCreateChannel(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	store := listChannels(t, s)[0].StoreID
	create := func(name, branch string) func() error {
		return func() error {
			_, err := s.CreateChannel(ctx, store, ChannelInput{Name: name, Branch: branch}, 0)
			return err
		}
	}
	rc, err := s.CreateChannel(ctx, store, ChannelInput{Name: " 候选 ", Branch: "rc-1.2"}, 0)
	if err != nil || rc.Name != "候选" || rc.URL != OfficialBase+"/index-rc-1.2/index.json" || !rc.Enabled || rc.IsDefault || rc.StoreID != store {
		t.Fatalf("create = %+v %v", rc, err)
	}
	wantCode(t, "same branch", core.ErrConflict.Code, create("x", "rc-1.2"))
	wantCode(t, "same name", core.ErrConflict.Code, create("测试", "x"))
	for _, b := range []string{"", "a/b", "a..b", ".hidden", "x.lock", "空"} {
		wantCode(t, "branch "+b, core.ErrInvalidArgument.Code, create("n"+b, b))
	}
	wantCode(t, "missing store", core.ErrNotFound.Code, func() error { _, err := s.CreateChannel(ctx, 999, ChannelInput{Name: "x", Branch: "x"}, 0); return err })
}

// An administrator renames, enables, disables, defaults and deletes
// channels; the default stays enabled and cannot be deleted.
func TestUpdateChannel(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	main, dev := listChannels(t, s)[0], listChannels(t, s)[1]
	update := func(id int64, p ChannelPatch) func() error {
		return func() error { _, err := s.UpdateChannel(ctx, id, p, 0); return err }
	}
	f, yes := false, true
	wantCode(t, "disable default", core.ErrConflict.Code, update(main.ID, ChannelPatch{Enabled: &f}))
	wantCode(t, "unset default", core.ErrConflict.Code, update(main.ID, ChannelPatch{IsDefault: &f}))
	wantCode(t, "delete default", core.ErrConflict.Code, func() error { return s.DeleteChannel(ctx, main.ID, 0) })
	wantCode(t, "empty patch", core.ErrInvalidArgument.Code, update(dev.ID, ChannelPatch{}))
	if err := update(dev.ID, ChannelPatch{Enabled: &f})(); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "default a disabled channel", core.ErrConflict.Code, update(dev.ID, ChannelPatch{IsDefault: &yes}))
	name := "测试频道"
	if got, err := s.UpdateChannel(ctx, dev.ID, ChannelPatch{Name: &name, Enabled: &yes, IsDefault: &yes}, 0); err != nil || got.Name != name || !got.IsDefault {
		t.Fatalf("make develop the default = %+v %v", got, err)
	}
	if def, err := s.source(ctx, 0); err != nil || def.ID != dev.ID {
		t.Fatalf("default = %+v %v", def, err)
	}
	if err := s.DeleteChannel(ctx, main.ID, 0); err != nil {
		t.Fatalf("delete the former default: %v", err)
	}
	wantCode(t, "delete gone", core.ErrNotFound.Code, func() error { return s.DeleteChannel(ctx, main.ID, 0) })
}

// wantCode fails unless fn fails with the error code.
func wantCode(t *testing.T, what, code string, fn func() error) {
	t.Helper()
	if e := core.AsError(fn()); e == nil || e.Code != code {
		t.Errorf("%s: %v, want %s", what, e, code)
	}
}
