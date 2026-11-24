package market

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// An administrator adds, renames, enables, disables, defaults and deletes
// channels; the default stays enabled and cannot be deleted, and names and
// branches are each one channel's.
func TestChannelCRUD(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	main, dev := listChannels(t, s)[0], listChannels(t, s)[1]
	rc, err := s.CreateChannel(ctx, ChannelInput{Name: " 候选 ", Branch: "rc-1.2"}, 0)
	if err != nil || rc.Name != "候选" || rc.URL != OfficialBase+"/index-rc-1.2/index.json" || !rc.Enabled || rc.IsDefault {
		t.Fatalf("create = %+v %v", rc, err)
	}
	wantCode(t, "same branch", core.ErrConflict.Code, func() error { _, err := s.CreateChannel(ctx, ChannelInput{Name: "x", Branch: "rc-1.2"}, 0); return err })
	wantCode(t, "same name", core.ErrConflict.Code, func() error { _, err := s.CreateChannel(ctx, ChannelInput{Name: "测试", Branch: "x"}, 0); return err })
	for _, b := range []string{"", "a/b", "a..b", ".hidden", "x.lock", "空"} {
		wantCode(t, "branch "+b, core.ErrInvalidArgument.Code, func() error { _, err := s.CreateChannel(ctx, ChannelInput{Name: "n" + b, Branch: b}, 0); return err })
	}
	f, yes := false, true
	wantCode(t, "disable default", core.ErrConflict.Code, func() error { _, err := s.UpdateChannel(ctx, main.ID, ChannelPatch{Enabled: &f}, 0); return err })
	wantCode(t, "unset default", core.ErrConflict.Code, func() error { _, err := s.UpdateChannel(ctx, main.ID, ChannelPatch{IsDefault: &f}, 0); return err })
	wantCode(t, "delete default", core.ErrConflict.Code, func() error { return s.DeleteChannel(ctx, main.ID, 0) })
	wantCode(t, "empty patch", core.ErrInvalidArgument.Code, func() error { _, err := s.UpdateChannel(ctx, dev.ID, ChannelPatch{}, 0); return err })
	if _, err := s.UpdateChannel(ctx, dev.ID, ChannelPatch{Enabled: &f}, 0); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "default a disabled channel", core.ErrConflict.Code, func() error { _, err := s.UpdateChannel(ctx, dev.ID, ChannelPatch{IsDefault: &yes}, 0); return err })
	name := "测试频道"
	got, err := s.UpdateChannel(ctx, dev.ID, ChannelPatch{Name: &name, Enabled: &yes, IsDefault: &yes}, 0)
	if err != nil || got.Name != name || !got.IsDefault || !got.Enabled {
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
