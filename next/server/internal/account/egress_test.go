package account

import (
	"fmt"
	"net/http"
	"testing"
)

// Egress follows the account through the real service: a proxy changed with
// PATCH /accounts/:id carries the next request at once on this node, and on
// a node whose bus lost the account:changed, from the bus's reconnect on.
func TestEgressFollowsTheAccountsSavedProxy(t *testing.T) {
	e := setup(t)
	id := e.mkRawAccount("anthropic", "apikey", "egress", "sk-good-key-123", true)
	aHost, aPort, aHits := forwardProxy(t)
	bHost, bPort, bHits := forwardProxy(t)
	a := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('a', 'http', $1, $2) RETURNING id`, aHost, aPort)
	b := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('b', 'http', $1, $2) RETURNING id`, bHost, bPort)
	setProxy := func(p any) {
		t.Helper()
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"proxy_id": p}); code != http.StatusOK {
			t.Fatalf("set proxy %v: %d %v", p, code, out)
		}
	}
	// send makes one request through svc's egress of the account and
	// reports which way it went.
	send := func(svc *Service) string {
		t.Helper()
		viaA, viaB := aHits.Load(), bHits.Load()
		eg, err := svc.Egress(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := eg.Client.Get(e.upstream.URL + "/v1/models")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		via := "direct"
		if aHits.Load() > viaA {
			via = "a"
		} else if bHits.Load() > viaB {
			via = "b"
		}
		if (via == "direct") == eg.Proxied {
			t.Fatalf("went %q with Proxied %v", via, eg.Proxied)
		}
		return via
	}

	// Another node on the same database, whose bus this node does not reach:
	// every account:changed is lost to it, as during a disconnect.
	bus := &reconnectBus{}
	other := New(Deps{DB: e.db, Cipher: e.svc.d.Cipher, Registry: e.reg, Proxies: e.prx, Bus: bus, Events: dbEvents{}})
	defer other.watchChanges(t.Context())()

	setProxy(a)
	if via := send(e.svc); via != "a" {
		t.Fatalf("first request went %q, want a", via)
	}
	if via := send(other); via != "a" {
		t.Fatalf("other node's first request went %q, want a", via)
	}

	setProxy(b)
	if via := send(e.svc); via != "b" {
		t.Fatalf("request after the change went %q, want b", via)
	}
	if via := send(other); via != "a" {
		t.Fatalf("other node before its reconnect went %q, want its snapshot's a", via)
	}
	bus.reconnect()
	if via := send(other); via != "b" {
		t.Fatalf("other node after its bus reconnected went %q, want b", via)
	}

	setProxy(nil)
	if via := send(e.svc); via != "direct" {
		t.Fatalf("request without a proxy went %q, want direct", via)
	}
}
