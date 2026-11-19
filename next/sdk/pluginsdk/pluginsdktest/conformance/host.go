package conformance

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// upstream scripts the fake host of one harness. Zero fields take the
// defaults: a waited relay answers the case's Observed, a fetch Plugin.Fetch.
type upstream struct {
	relay  func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error)
	fetch  func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)
	server []grpc.ServerOption
}

// start serves the plugin under test with a fake host scripted by u for c.
func (s *suite) start(t testing.TB, c *Case, u upstream) *pluginsdktest.Harness {
	t.Helper()
	if u.relay == nil {
		u.relay = observe(c)
	}
	if u.fetch == nil {
		u.fetch = s.p.Fetch
	}
	return s.serve(t, s.p.Platform, s.p.Manifest, s.p.SDK, s.p.Config, u)
}

func (s *suite) serve(t testing.TB, p any, manifest []byte, sdk []pluginsdk.Option, config any, u upstream) *pluginsdktest.Harness {
	t.Helper()
	fh := pluginsdktest.NewFakeHost()
	fh.RelayFunc, fh.ExecuteHTTPFunc = u.relay, u.fetch
	fh.DialFunc = refuseDial // the fake host records the address (Dials)
	s.sockets.watch(fh)
	return pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Config: config, Manifest: manifest, SDK: sdk, PluginServer: u.server})
}

// observe answers a relay with the case's observation: the fake host fills
// the copy in, so every answer is a fresh one.
func observe(c *Case) func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		if c.Observed == nil {
			return &pluginv1.RelayUpstreamResponse{}, nil
		}
		return proto.Clone(c.Observed).(*pluginv1.RelayUpstreamResponse), nil
	}
}

// sockets replaces the process's default transport and resolver, and the
// fake hosts' egress dialer, with dialers that fail and remember the
// address (rule 8).
type sockets struct {
	mu    sync.Mutex
	dials []string
	hosts []*pluginsdktest.FakeHost
}

// guard serialises Runs: the defaults are process-wide.
var guard sync.Mutex

// install swaps the defaults in and returns what swaps them back.
func (g *sockets) install() (restore func()) {
	guard.Lock()
	transport, resolver := http.DefaultTransport, net.DefaultResolver
	http.DefaultTransport = &http.Transport{DialContext: g.dial, DialTLSContext: g.dial}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: g.dial}
	return func() {
		http.DefaultTransport, net.DefaultResolver = transport, resolver
		guard.Unlock()
	}
}

func (g *sockets) dial(_ context.Context, network, address string) (net.Conn, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dials = append(g.dials, network+" "+address)
	return nil, fmt.Errorf("conformance: a plugin opens no socket (dial %s %s)", network, address)
}

func refuseDial(_ context.Context, network, address string) (net.Conn, error) {
	return nil, fmt.Errorf("conformance: a plugin opens no socket (egress dial %s %s)", network, address)
}

func (g *sockets) watch(fh *pluginsdktest.FakeHost) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hosts = append(g.hosts, fh)
}

// opened returns every dial seen so far: through the defaults and through
// the hosts' egress service.
func (g *sockets) opened() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string(nil), g.dials...)
	for _, fh := range g.hosts {
		for _, address := range fh.Dials() {
			out = append(out, "egress "+address)
		}
	}
	return out
}
