package grpcruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

type staticNode struct{ id, boot string }

func (n staticNode) NodeID() string { return n.id }
func (n staticNode) BootID() string { return n.boot }

// fakeApp records OnBroadcast calls; delay simulates a slow plugin.
type fakeApp struct {
	pluginv1.AppServiceClient
	got   chan *pluginv1.OnBroadcastRequest
	delay time.Duration
}

func (f *fakeApp) OnBroadcast(ctx context.Context, in *pluginv1.OnBroadcastRequest, _ ...grpc.CallOption) (*pluginv1.OnBroadcastResponse, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	f.got <- in
	return &pluginv1.OnBroadcastResponse{}, nil
}

func testInstance(t *testing.T, bus *registrytest.MemBus, caps []string, grants registry.Grants) (*Instance, *fakeApp) {
	t.Helper()
	m := registrytest.Manifest("bc", "1.0.0")
	m.Capabilities = nil
	for _, c := range caps {
		m.Capabilities = append(m.Capabilities, manifest.Capability{ID: c})
	}
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	rt := &Runtime{o: Options{Bus: bus, Node: staticNode{"node-a", "boot-a"}, Concurrency: Concurrency{Hot: 4, Console: 4, Background: 4}, DataDir: t.TempDir()}, log: slog.Default()}
	if bus == nil {
		rt.o.Bus = nil
	}
	i := newInstance(rt, pkg, "", "", &settings{configJSON: "{}", grants: grants})
	app := &fakeApp{got: make(chan *pluginv1.OnBroadcastRequest, 4)}
	i.proc.Store(&proc{app: app, limits: limitsOf(i.launchSpec())})
	return i, app
}

func TestHostPublish(t *testing.T) {
	bus := &registrytest.MemBus{}
	ctx := context.Background()

	i, _ := testInstance(t, bus, []string{manifest.CapAppBroadcast}, registry.Grants{})
	h := &hostServer{i: i}
	if _, err := h.Publish(ctx, &pluginv1.PublishRequest{Topic: "rules.changed"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("publish without grant: %v", err)
	}

	i, _ = testInstance(t, bus, nil, registry.Grants{PermBroadcast: json.RawMessage(`{}`)})
	h = &hostServer{i: i}
	for _, topic := range []string{"", "Rules", "a b", strings.Repeat("x", 65), "x/y"} {
		if _, err := h.Publish(ctx, &pluginv1.PublishRequest{Topic: topic}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("topic %q: %v", topic, err)
		}
	}
	big := make([]byte, BroadcastMaxPayload+1)
	if _, err := h.Publish(ctx, &pluginv1.PublishRequest{Topic: "t", Payload: big}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized payload: %v", err)
	}
	if _, err := h.Publish(ctx, &pluginv1.PublishRequest{Topic: "rules.changed", Payload: big[:BroadcastMaxPayload]}); err != nil {
		t.Fatalf("max payload: %v", err)
	}
	msgs := bus.Messages(BroadcastChannel("bc"))
	if len(msgs) != 1 {
		t.Fatalf("messages %d", len(msgs))
	}
	var m BroadcastMessage
	if err := json.Unmarshal(msgs[0].Payload, &m); err != nil {
		t.Fatal(err)
	}
	if m.Topic != "rules.changed" || len(m.Payload) != BroadcastMaxPayload || m.SourceNodeID != "node-a" || m.SourceBootID != "boot-a" {
		t.Fatalf("message %+v", m.Topic)
	}

	i, _ = testInstance(t, nil, nil, registry.Grants{PermBroadcast: json.RawMessage(`{}`)})
	if _, err := (&hostServer{i: i}).Publish(ctx, &pluginv1.PublishRequest{Topic: "t"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("publish without bus: %v", err)
	}
}

func TestInstanceOnBroadcast(t *testing.T) {
	ctx := context.Background()
	msg := BroadcastMessage{Topic: "rules.changed", Payload: []byte("v2"), SourceNodeID: "node-b", SourceBootID: "boot-b"}

	// Without app.broadcast.v1 nothing is delivered.
	i, app := testInstance(t, nil, []string{manifest.CapAppJobs}, registry.Grants{})
	if i.HandlesBroadcast() {
		t.Fatal("HandlesBroadcast without capability")
	}
	if err := i.OnBroadcast(ctx, msg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-app.got:
		t.Fatal("delivered without capability")
	default:
	}

	i, app = testInstance(t, nil, []string{manifest.CapAppBroadcast}, registry.Grants{})
	if err := i.OnBroadcast(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got := <-app.got
	if got.GetTopic() != "rules.changed" || string(got.GetPayload()) != "v2" || got.GetSourceNodeId() != "node-b" {
		t.Fatalf("delivered %+v", got)
	}

	// Delivery is bounded by BroadcastTimeout.
	app.delay = BroadcastTimeout + time.Second
	start := time.Now()
	if err := i.OnBroadcast(ctx, msg); err == nil {
		t.Fatal("slow delivery did not time out")
	}
	if d := time.Since(start); d > BroadcastTimeout+500*time.Millisecond {
		t.Fatalf("timeout took %s", d)
	}
}

func TestLimitsStale(t *testing.T) {
	i, _ := testInstance(t, nil, nil, registry.Grants{})
	if i.LimitsStale() {
		t.Fatal("fresh instance reported stale limits")
	}
	st := *i.settings.Load()
	st.limits = resourceLimits{MemoryMB: 64}
	i.settings.Store(&st)
	if !i.LimitsStale() {
		t.Fatal("changed memory limit not detected")
	}
	i.rt.o.MaxMemoryMB = 32 // global cap wins over both
	p := *i.proc.Load()
	p.limits.MemoryMB = 32
	i.proc.Store(&p)
	if i.LimitsStale() {
		t.Fatal("effective limit unchanged but reported stale")
	}
}
