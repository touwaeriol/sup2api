package pluginsdk_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const broadcastManifest = `{
  "apiVersion": 1, "key": "bc", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "app.broadcast.v1"}],
  "hostPermissions": [{"id": "broadcast", "reason": {"en": "x", "zh": "x"}}]
}`

type broadcaster struct {
	*pluginsdk.BroadcastMux
	host pluginsdk.Host

	mu  sync.Mutex
	got []pluginsdk.Broadcast
}

func newBroadcaster() *broadcaster {
	b := &broadcaster{BroadcastMux: pluginsdk.NewBroadcastMux()}
	b.OnTopic("rules.changed", func(_ context.Context, m pluginsdk.Broadcast) error {
		b.mu.Lock()
		b.got = append(b.got, m)
		b.mu.Unlock()
		return nil
	})
	b.OnTopic("fail", func(context.Context, pluginsdk.Broadcast) error { return errors.New("nope") })
	return b
}

func (b *broadcaster) Init(_ context.Context, h pluginsdk.Host) error { b.host = h; return nil }

func (b *broadcaster) received() []pluginsdk.Broadcast {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]pluginsdk.Broadcast(nil), b.got...)
}

func TestBroadcastBetweenTwoNodes(t *testing.T) {
	raw := []byte(broadcastManifest)
	a, b := newBroadcaster(), newBroadcaster()
	hostA := pluginsdktest.NewFakeHost()
	ha := pluginsdktest.Start(t, a, pluginsdktest.Options{Host: hostA, Manifest: raw})
	hb := pluginsdktest.Start(t, b, pluginsdktest.Options{Manifest: raw})
	if caps := ha.Info.GetCapabilities(); len(caps) != 1 || caps[0] != manifest.CapAppBroadcast {
		t.Fatalf("capabilities = %v", caps)
	}
	// "Node A" relays its publishes to "node B".
	hostA.OnPublish = func(m pluginsdktest.PublishedMessage) { _ = hb.Deliver(m.Topic, m.Payload, "node-a") }

	ctx := context.Background()
	if err := a.host.Publish(ctx, "rules.changed", []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if got := b.received(); len(got) != 1 || got[0].Topic != "rules.changed" || string(got[0].Payload) != `{"v":2}` || got[0].SourceNodeID != "node-a" {
		t.Fatalf("node B received %+v", got)
	}
	if len(a.received()) != 0 {
		t.Fatal("the publisher must not receive its own message")
	}
	if pub := hostA.Published(); len(pub) != 1 || pub[0].Topic != "rules.changed" {
		t.Fatalf("published = %+v", pub)
	}

	// Client-side validation.
	for _, topic := range []string{"", "Upper", "a b", strings.Repeat("x", 65)} {
		if err := a.host.Publish(ctx, topic, nil); status.Code(err) != codes.InvalidArgument {
			t.Errorf("topic %q: err = %v", topic, err)
		}
	}
	if err := a.host.Publish(ctx, "big", make([]byte, pluginsdk.MaxBroadcastPayload+1)); status.Code(err) != codes.InvalidArgument {
		t.Errorf("oversized payload: err = %v", err)
	}
	hostA.PublishErr = status.Error(codes.PermissionDenied, "no broadcast grant")
	if err := a.host.Publish(ctx, "rules.changed", nil); status.Code(err) != codes.PermissionDenied {
		t.Errorf("host error: %v", err)
	}

	// Unknown topics are ignored; handler errors are reported.
	if err := hb.Deliver("unknown.topic", nil, "node-a"); err != nil {
		t.Fatalf("unknown topic: %v", err)
	}
	if err := hb.Deliver("fail", nil, "node-a"); status.Code(err) != codes.Internal {
		t.Fatalf("failing handler: %v", err)
	}
}

func TestBroadcastManifestChecks(t *testing.T) {
	noPerm := `{"apiVersion": 1, "key": "bc", "version": "0.1.0", "runtime": "grpc", "capabilities": [{"id": "app.broadcast.v1"}]}`
	if err := pluginsdk.Register(nil, newBroadcaster(), nil, pluginsdk.WithManifest([]byte(noPerm))); err == nil || !strings.Contains(err.Error(), `"broadcast"`) {
		t.Fatalf("missing host permission: %v", err)
	}
	// Declared but not implemented.
	if err := pluginsdk.Register(nil, newDemo(), nil, pluginsdk.WithManifest([]byte(broadcastManifest))); err == nil || !strings.Contains(err.Error(), "OnBroadcast") {
		t.Fatalf("declared without handler: %v", err)
	}
	// Implemented but not declared: the capability is not reported.
	h := pluginsdktest.Start(t, newBroadcaster(), pluginsdktest.Options{Manifest: []byte(testManifest)})
	for _, c := range h.Info.GetCapabilities() {
		if c == manifest.CapAppBroadcast {
			t.Fatal("undeclared capability reported")
		}
	}
	// A plugin without a broadcast handler answers Unimplemented.
	hd := pluginsdktest.Start(t, newDemo(), pluginsdktest.Options{Manifest: []byte(testManifest)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := hd.App.OnBroadcast(ctx, &pluginv1.OnBroadcastRequest{Topic: "x"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("OnBroadcast without handler: %v", err)
	}
}
