package market

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// An administrator adds channels to a store; names and branches are each
// one channel's of the store, and a branch is one git ref name segment.
func TestCreateChannel(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	githubOffline(s)
	store := listChannels(t, s)[0].StoreID
	create := func(name, branch string) func() error {
		return func() error {
			_, err := s.CreateChannel(ctx, store, ChannelInput{Name: name, Branch: branch}, 0)
			return err
		}
	}
	rc, err := s.CreateChannel(ctx, store, ChannelInput{Name: " 候选 ", Branch: "rc-1.2"}, 0)
	if err != nil || rc.Name != "候选" || rc.URL != OfficialBase+"/index-rc-1.2/index.json" || !rc.Enabled || rc.IsDefault || rc.StoreID != store || rc.Warning != WarningNoIndex {
		t.Fatalf("create = %+v %v", rc, err)
	}
	wantCode(t, "same branch", core.ErrConflict.Code, create("x", "rc-1.2"))
	wantCode(t, "same name", core.ErrConflict.Code, create("测试", "x"))
	for _, b := range []string{"", "a/b", "a..b", ".hidden", "x.lock", "空"} {
		wantCode(t, "branch "+b, core.ErrInvalidArgument.Code, create("n"+b, b))
	}
	wantCode(t, "missing store", core.ErrNotFound.Code, func() error { _, err := s.CreateChannel(ctx, 999, ChannelInput{Name: "x", Branch: "x"}, 0); return err })
}

// A new channel is saved whether or not its branch has an index yet; one
// without warns (WarningNoIndex).
func TestCreateChannelProbe(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, _ := newMarket(t)
	store := listChannels(t, s)[0].StoreID
	for branch, warning := range map[string]string{"rc": "", "nightly": WarningNoIndex} {
		ch, err := s.CreateChannel(ctx, store, ChannelInput{Name: branch, Branch: branch}, 0)
		if err != nil || ch.Warning != warning {
			t.Errorf("%s: %+v %v, want warning %q", branch, ch, err, warning)
		}
	}
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

// Concurrent requests to make channels the default take turns on the
// store: each succeeds and one default is left.
func TestConcurrentDefault(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	githubOffline(s)
	chs := listChannels(t, s)
	for _, b := range []string{"a", "b", "c"} {
		ch, err := s.CreateChannel(ctx, chs[0].StoreID, ChannelInput{Name: b, Branch: b}, 0)
		if err != nil {
			t.Fatal(err)
		}
		chs = append(chs, *ch)
	}
	yes := true
	errs := make(chan error, len(chs))
	for _, ch := range chs {
		go func() { _, err := s.UpdateChannel(ctx, ch.ID, ChannelPatch{IsDefault: &yes}, 0); errs <- err }()
	}
	for range chs {
		if err := <-errs; err != nil {
			t.Errorf("make default: %v", err)
		}
	}
	all, err := s.ListSources(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, ch := range all {
		if ch.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("%d defaults, want 1", defaults)
	}
}

// The official store lists a publisher when an enabled channel's index
// does; a channel whose index is not published (develop here) lists no
// one, and a channel that cannot be read refuses.
func TestListsPublisher(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, _ := newMarket(t)
	for id, want := range map[string]bool{"acme": true, "nobody": false} {
		if got, err := s.ListsPublisher(ctx, id); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", id, got, err, want)
		}
	}
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
	if _, err := s.ListsPublisher(ctx, "acme"); err == nil {
		t.Fatal("an unreadable index lists no one")
	}
}
