package grpcruntime_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

func kvBatchReq(op, ns string, keys []string, size int, ttl int64) *pluginv1.HTTPRequest {
	b, _ := json.Marshal(map[string]any{"op": op, "ns": ns, "keys": keys, "size": size, "ttl": ttl})
	return &pluginv1.HTTPRequest{Method: "POST", Path: "/kvbatch", Body: b}
}

// TestKVBatchRealPlugin: the kv batch calls from a real plugin process, over
// the broker connection, on (mini)Redis under plugin:kv:{key}:{ns}:{key};
// and what the connection itself accepts: the host's gRPC server receives
// requests up to 4 MiB, which is above the payload limit of one batch.
func TestKVBatchRealPlugin(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	inst := e.load(t, e.runtime(t, nil))
	call := func(op, ns string, keys []string, size int, ttl int64) map[string]any {
		t.Helper()
		out, err := httpCall(t, inst, kvBatchReq(op, ns, keys, size, ttl))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return out
	}
	rkey := func(ns, key string) string { return "plugin:kv:" + e.key + ":" + ns + ":" + key }

	if out := call("set", "b", []string{"k1", "k2", "k3"}, 0, 300); out["code"] != "OK" {
		t.Fatalf("set: %v", out)
	}
	for _, k := range []string{"k1", "k2", "k3"} {
		if v, _ := e.mr.Get(rkey("b", k)); v != "v:"+k || e.mr.TTL(rkey("b", k)) != 5*time.Minute {
			t.Fatalf("redis %s = %q ttl %v", rkey("b", k), v, e.mr.TTL(rkey("b", k)))
		}
	}
	out := call("get", "b", []string{"k3", "missing", "k1"}, 0, 0)
	if out["code"] != "OK" || fmt.Sprint(out["found"]) != "[true false true]" || fmt.Sprint(out["values"]) != "[v:k3  v:k1]" {
		t.Fatalf("get: %v", out)
	}
	if out := call("delete", "b", []string{"k1", "missing", "k3"}, 0, 0); out["code"] != "OK" {
		t.Fatalf("delete: %v", out)
	}
	if e.mr.Exists(rkey("b", "k1")) || !e.mr.Exists(rkey("b", "k2")) || e.mr.Exists(rkey("b", "k3")) {
		t.Fatal("delete removed the wrong keys")
	}
	if out := call("get", "bad:ns", []string{"k"}, 0, 0); out["code"] != "InvalidArgument" {
		t.Fatalf("bad namespace: %v", out)
	}

	// The largest batch of full-size values (47 x 64 KiB with their keys is
	// under the 3 MiB payload limit) passes the connection both ways.
	keys := make([]string, 66)
	for i := range keys {
		keys[i] = fmt.Sprintf("big%02d", i)
	}
	const size = protocol.KVMaxValueBytes
	if out := call("set", "b", keys[:47], size, 0); out["code"] != "OK" {
		t.Fatalf("set 47 x 64 KiB: %v", out)
	}
	if out := call("get", "b", keys[:47], 0, 0); out["code"] != "OK" || out["bytes"] != float64(47*size) {
		t.Fatalf("get 47 x 64 KiB: code %v, %v bytes", out["code"], out["bytes"])
	}
	// 48 values with their keys are above the payload limit: the host
	// refuses the batch and writes none of it.
	if out := call("set", "c", keys[:48], size, 0); out["code"] != "InvalidArgument" || e.mr.Exists(rkey("c", keys[0])) {
		t.Fatalf("set 48 x 64 KiB: %v", out)
	}
	// 66 values are 4.1 MiB: the host's gRPC server receives it (it has no
	// limit of its own), and the payload limit refuses it.
	out = call("set", "c", keys, size, 0)
	if out["code"] != "InvalidArgument" || e.mr.Exists(rkey("c", keys[0])) {
		t.Fatalf("set 66 x 64 KiB: %v", out)
	}
}

// TestKVBatchRealPluginNeedsTheGrant: without the kv grant a real plugin's
// batch calls are refused.
func TestKVBatchRealPluginNeedsTheGrant(t *testing.T) {
	grants := registrytest.DefaultGrants()
	delete(grants, "kv")
	e := setup(t, grants)
	inst := e.load(t, e.runtime(t, nil))
	for _, op := range []string{"get", "set", "delete"} {
		out, err := httpCall(t, inst, kvBatchReq(op, "b", []string{"k"}, 0, 0))
		if err != nil || out["code"] != "PermissionDenied" {
			t.Fatalf("%s without the kv grant: %v %v", op, out, err)
		}
	}
	if keys := e.mr.Keys(); len(keys) != 0 {
		for _, k := range keys {
			if strings.HasPrefix(k, "plugin:kv:") {
				t.Fatalf("a call without the grant wrote %s", k)
			}
		}
	}
}
