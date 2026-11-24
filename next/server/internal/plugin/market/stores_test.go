package market

import (
	"cmp"
	"context"
	"net/http"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The official store is the one store: renamed, never another repository;
// its default channel is main. Renaming a missing store is 404.
func TestUpdateStore(t *testing.T) {
	ctx := context.Background()
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	official, name := listChannels(t, s)[0].StoreID, "官方"
	if got, err := s.UpdateStore(ctx, official, StorePatch{Name: &name}, 0); err != nil || got.Name != name || got.Repo != OfficialRepo || !got.Official {
		t.Fatalf("rename official = %+v %v", got, err)
	}
	if def, err := s.source(ctx, 0); err != nil || !def.Official || def.Branch != "main" || def.StoreName != name {
		t.Fatalf("default channel = %+v %v", def, err)
	}
	wantCode(t, "rename missing", core.ErrNotFound.Code, func() error { _, err := s.UpdateStore(ctx, official+1, StorePatch{Name: &name}, 0); return err })
	if sts, err := s.ListStores(ctx); err != nil || len(sts) != 1 {
		t.Fatalf("stores = %+v %v", sts, err)
	}
}

// githubOffline answers the index requests s sends to GitHub with 404
// instead of going to the network; other hosts are served as before.
func githubOffline(s *Service) {
	inner := cmp.Or[http.RoundTripper](s.client.Transport, http.DefaultTransport)
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" {
			return inner.RoundTrip(r)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
