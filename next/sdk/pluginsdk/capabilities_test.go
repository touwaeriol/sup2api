package pluginsdk

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

type monitorTestPlatform struct {
	Platform
	UpstreamBuilder
	TaskMonitor
}

type rankOnly struct{ AccountRanker }

func TestCapabilitiesAreInferredFromInterfaces(t *testing.T) {
	for name, tc := range map[string]struct {
		p    any
		want []string
	}{
		"platform":  {ordinaryTestPlatform{}, []string{manifest.CapPlatformAdapter}},
		"monitor":   {monitorTestPlatform{}, []string{manifest.CapPlatformAdapter, manifest.CapPlatformMonitor}},
		"rank only": {rankOnly{}, []string{manifest.CapSchedulerRank}},
	} {
		if got := Capabilities(tc.p); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func manifestWith(caps ...string) []byte {
	return []byte(`{"key":"p","version":"0.1.0","capabilities":[{"id":"` + strings.Join(caps, `"},{"id":"`) + `"}]}`)
}

// tasks.v1 and websocket.v1 are promises about Execute: reported when the
// manifest declares them, and only a Platform can make them.
func TestDeclaredPromisesAreReported(t *testing.T) {
	all := []string{manifest.CapPlatformAdapter, manifest.CapPlatformMonitor, manifest.CapPlatformTasks, manifest.CapPlatformWebSocket}
	rt, err := newRuntime(monitorTestPlatform{}, options{manifestRaw: manifestWith(all...)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.capabilities, all) {
		t.Fatalf("reported %v, want %v", rt.capabilities, all)
	}
	info, _ := rt.GetInfo(t.Context(), nil)
	if info.GetProtocolVersion() != 2 || protocol.Handshake.ProtocolVersion != 2 {
		t.Fatalf("plugin protocol %d, handshake %d, want 2", info.GetProtocolVersion(), protocol.Handshake.ProtocolVersion)
	}
	// Undeclared, an implemented capability is not reported.
	rt, err = newRuntime(monitorTestPlatform{}, options{manifestRaw: manifestWith(manifest.CapPlatformAdapter)}, nil)
	if err != nil || !slices.Equal(rt.capabilities, []string{manifest.CapPlatformAdapter}) {
		t.Fatalf("reported %v, %v", rt.capabilities, err)
	}
}

func TestManifestPromisesNeedTheirImplementation(t *testing.T) {
	for name, tc := range map[string]struct {
		p    any
		caps []string
	}{
		"websocket without a Platform":   {struct{}{}, []string{manifest.CapPlatformWebSocket}},
		"tasks without a Platform":       {rankOnly{}, []string{manifest.CapPlatformTasks}},
		"monitor without a TaskMonitor":  {ordinaryTestPlatform{}, []string{manifest.CapPlatformAdapter, manifest.CapPlatformMonitor}},
		"monitor without the adapter":    {monitorTestPlatform{}, []string{manifest.CapPlatformMonitor}},
		"tasks without a TaskMonitor":    {ordinaryTestPlatform{}, []string{manifest.CapPlatformAdapter, manifest.CapPlatformTasks}},
		"tasks without monitor declared": {monitorTestPlatform{}, []string{manifest.CapPlatformAdapter, manifest.CapPlatformTasks}},
	} {
		if _, err := newRuntime(tc.p, options{manifestRaw: manifestWith(tc.caps...)}, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A task endpoint is a promise even without platform.tasks.v1.
	raw := []byte(`{"key":"p","version":"0.1.0","capabilities":[{"id":"platform.adapter.v1"},{"id":"platform.monitor.v1"}],
		"platforms":[{"id":"x","endpoints":[{"protocol":"x.submit","task":{"action":"submit"}}]}]}`)
	if _, err := newRuntime(monitorTestPlatform{}, options{manifestRaw: raw}, nil); err == nil {
		t.Error("task endpoint without platform.tasks.v1 accepted")
	}
	// Inferred without a manifest, a monitor still needs its Platform.
	if _, err := newRuntime(struct{ TaskMonitor }{}, options{key: "p", version: "0.1.0"}, nil); err == nil {
		t.Error("TaskMonitor without a Platform accepted")
	}
}

// Every plugin server takes gRPC's maximum: no plugin states a size limit.
func TestServerReceiveLimitIsTheMaximum(t *testing.T) {
	s := NewGRPCServer(nil)
	defer s.Stop()
	v := reflect.ValueOf(s).Elem().FieldByName("opts").FieldByName("maxReceiveMessageSize")
	if !v.IsValid() {
		t.Fatal("grpc.Server no longer has opts.maxReceiveMessageSize")
	}
	if v.Int() != math.MaxInt32 {
		t.Fatalf("receive limit %d", v.Int())
	}
}
