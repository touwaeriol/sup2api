package gateway

import (
	"net/http"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type quotaCall struct {
	id     int64
	keys   int
	status int
}

type fakeQuotaObserver struct {
	calls   []quotaCall
	headers []http.Header
	events  []quotaEventCall
}

type quotaEventCall struct {
	id  int64
	typ string
	msg string
}

func (f *fakeQuotaObserver) ObserveQuotaHeaders(id int64, h []manifest.QuotaHeader, status int, header http.Header) {
	f.calls = append(f.calls, quotaCall{id, len(h), status})
	f.headers = append(f.headers, header)
}

func (f *fakeQuotaObserver) ObserveQuotaEvent(id int64, ev manifest.QuotaEvent, msg []byte) {
	f.events = append(f.events, quotaEventCall{id, ev.Type, string(msg)})
}

// Upstream responses of account types declaring quota headers reach the
// observer with the declaration (CONTRACTS §44); other types never do.
func TestObserveQuota(t *testing.T) {
	obs := &fakeQuotaObserver{}
	g := &Gateway{d: Deps{Quota: obs}}
	withQuota := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "oauth",
		Quota: &manifest.AccountQuota{Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-s"}}}}}}
	queryOnly := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "q",
		Quota: &manifest.AccountQuota{Query: true}}}}
	apiKey := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "apikey"}}}
	g.observeQuota(withQuota, 1, 429, http.Header{})
	g.observeQuota(queryOnly, 2, 200, http.Header{})
	g.observeQuota(apiKey, 3, 200, http.Header{})
	g.observeQuota(nil, 4, 200, http.Header{})
	if len(obs.calls) != 1 || obs.calls[0] != (quotaCall{1, 1, 429}) {
		t.Fatalf("calls = %+v", obs.calls)
	}
	// No observer: nothing to do.
	(&Gateway{}).observeQuota(withQuota, 1, 200, http.Header{})
}
