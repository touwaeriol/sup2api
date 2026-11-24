package install

import (
	"context"
	"errors"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// fakeStore lists the publishers of the official store's index; err fails
// every lookup.
type fakeStore struct {
	listed map[string]bool
	err    error
}

func (f fakeStore) ListsPublisher(_ context.Context, id string) (bool, error) {
	return f.listed[id], f.err
}

// A local publisher's name is its publisher id (CONTRACTS §137): a valid
// one, not the first-party sub2api and not one the official store lists;
// a store that cannot be read refuses it.
func TestLocalPublisherID(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	create := func(name string) error {
		_, err := e.svc.CreatePublisher(ctx, CreatePublisherInput{Name: name, TrustLevel: pkg.TrustCommunity}, e.admin)
		return err
	}
	for _, name := range []string{"my-pub", "a__b", "erio_", "Erio"} {
		if code := fieldCode(create(name), "name"); code != "invalid_format" {
			t.Fatalf("%q: %q", name, code)
		}
	}
	if code := fieldCode(create("sub2api"), "name"); code != "reserved" {
		t.Fatalf("sub2api: %q", code)
	}
	e.svc.d.Store = fakeStore{listed: map[string]bool{"erio": true}}
	if code := errCode(create("erio")); code != "publisher_in_store" {
		t.Fatalf("store publisher: %q", code)
	}
	if err := create("erio_dev"); err != nil {
		t.Fatalf("erio_dev: %v", err)
	}
	e.svc.d.Store = fakeStore{err: errors.New("offline")}
	if err := create("acme"); err == nil {
		t.Fatal("created a publisher the store could not be asked about")
	}
}
