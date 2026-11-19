package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// The account clients never take the process environment's proxy (only the
// media download does, CONVERT-MATRIX §9.2): the direct client stays
// direct, a proxy client uses its own proxy only.
func TestAccountClientsIgnoreEnvironmentProxy(t *testing.T) {
	counting := func() (*httptest.Server, *atomic.Int32) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1) }))
		t.Cleanup(srv.Close)
		return srv, &n
	}
	env, envHits := counting()
	own, ownHits := counting()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(k, env.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	svc := New(nil, nil, nil, Options{AllowPrivate: true})

	pc, err := svc.HTTPClient(t.Context(), nil)
	direct := pc.Client
	if err != nil {
		t.Fatal(err)
	}
	if tr, ok := direct.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatalf("direct transport %T has a proxy", direct.Transport)
	}
	if resp, err := direct.Get("http://target.invalid/"); err == nil {
		resp.Body.Close()
		t.Fatal("direct client reached an unresolvable name")
	}
	host, port, _ := net.SplitHostPort(own.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	viaOwn, err := svc.HTTPClientFor(t.Context(), Spec{Protocol: "http", Host: host, Port: p})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://target.invalid/", nil)
	if pu, err := viaOwn.Transport.(*http.Transport).Proxy(req); err != nil || pu.Host != own.Listener.Addr().String() {
		t.Fatalf("own proxy client picks %v, %v", pu, err)
	}
	resp, err := viaOwn.Get("http://target.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if envHits.Load() != 0 || ownHits.Load() != 1 {
		t.Fatalf("environment proxy %d requests, own proxy %d", envHits.Load(), ownHits.Load())
	}
}
