package pluginsdktest

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

func fakeItem(key, value string, ttl int64) *pluginv1.KVSetManyItem {
	return &pluginv1.KVSetManyItem{Key: key, Value: []byte(value), TtlSeconds: ttl}
}

func fakeGetMany(t *testing.T, f *FakeHost, ns string, keys ...string) string {
	t.Helper()
	r, err := f.KVGetMany(context.Background(), &pluginv1.KVGetManyRequest{Namespace: ns, Keys: keys})
	if err != nil {
		t.Fatalf("KVGetMany: %v", err)
	}
	parts := make([]string, len(r.GetItems()))
	for i, it := range r.GetItems() {
		parts[i] = "-"
		if it.GetFound() {
			parts[i] = "(" + string(it.GetValue()) + ")"
		}
	}
	return strings.Join(parts, " ")
}

func fakeKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%04d", i)
	}
	return keys
}

func TestFakeHostKVBatch(t *testing.T) {
	f := NewFakeHost()
	ctx := context.Background()
	if _, err := f.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "ns", Key: "single", Value: []byte("s")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "ns", Items: []*pluginv1.KVSetManyItem{
		fakeItem("a", "1", 0), fakeItem("b", "2", 60), fakeItem("empty", "", 0), fakeItem("a", "last", 0),
	}}); err != nil {
		t.Fatal(err)
	}
	// One answer per key, in request order; the single calls see the same store.
	if got, want := fakeGetMany(t, f, "ns", "b", "missing", "a", "a", "empty", "single"), "(2) - (last) (last) () (s)"; got != want {
		t.Fatalf("items = %s, want %s", got, want)
	}
	if got := fakeGetMany(t, f, "other", "a"); got != "-" {
		t.Fatalf("another namespace reads %s", got)
	}
	if r, err := f.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "ns", Key: "b"}); err != nil || string(r.GetValue()) != "2" {
		t.Fatalf("KVGet of a batch write: %q %v", r.GetValue(), err)
	}
	// Every item has its own ttl: "b" expires, "a" does not.
	f.mu.Lock()
	if it := f.kv[kvKey("ns", "b")]; time.Until(it.expires) < 59*time.Second || time.Until(it.expires) > 60*time.Second {
		t.Errorf("ttl of b = %v", time.Until(it.expires))
	} else {
		it.expires = time.Now().Add(-time.Second)
		f.kv[kvKey("ns", "b")] = it
	}
	if !f.kv[kvKey("ns", "a")].expires.IsZero() {
		t.Error("a has an expiry")
	}
	f.mu.Unlock()
	if got, want := fakeGetMany(t, f, "ns", "a", "b"), "(last) -"; got != want {
		t.Fatalf("after b expired = %s, want %s", got, want)
	}
	if _, err := f.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: "ns", Keys: []string{"a", "missing", "single"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := fakeGetMany(t, f, "ns", "a", "single", "empty"), "- - ()"; got != want {
		t.Fatalf("after delete = %s, want %s", got, want)
	}
	// No keys is valid.
	if got := fakeGetMany(t, f, "ns"); got != "" {
		t.Fatalf("no keys: %q", got)
	}
	want := map[string]int{"KVSet": 1, "KVGet": 1, "KVSetMany": 1, "KVGetMany": 5, "KVDeleteMany": 1}
	if got := f.KVCalls(); !maps.Equal(got, want) {
		t.Fatalf("KVCalls = %v, want %v", got, want)
	}
}

func TestFakeHostKVBatchRulesAndAtomicWrite(t *testing.T) {
	f := NewFakeHost()
	ctx := context.Background()
	big := strings.Repeat("v", protocol.KVMaxValueBytes)
	if _, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "ns", Items: []*pluginv1.KVSetManyItem{fakeItem("old", "before", 0)}}); err != nil {
		t.Fatal(err)
	}
	// The rules are the host's (protocol.CheckKVBatchItems; their table is
	// in sdk/protocol). One item of each kind: nothing is stored.
	for name, bad := range map[string]*pluginv1.KVSetManyItem{
		"empty key":     fakeItem("", "v", 0),
		"value too big": fakeItem("big", big+"x", 0),
		"negative ttl":  fakeItem("neg", "v", -1),
		"missing (nil)": nil,
	} {
		_, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "ns", Items: []*pluginv1.KVSetManyItem{
			fakeItem("first", "1", 0), fakeItem("old", "after", 0), bad, fakeItem("last", "2", 0)}})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := fakeGetMany(t, f, "ns", "first", "old", "last"), "- (before) -"; got != want {
			t.Fatalf("%s: the rejected write left %s, want %s", name, got, want)
		}
	}
	// The error is the host's own: it names the first item that breaks a
	// rule, with the host's words.
	_, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "ns", Items: []*pluginv1.KVSetManyItem{
		fakeItem("first", "1", 0), fakeItem("big", big+"x", 0), fakeItem("", "v", 0)}})
	if got, want := status.Convert(err).Message(), "items[1]: value exceeds 65536 bytes"; status.Code(err) != codes.InvalidArgument || got != want {
		t.Fatalf("two invalid items: %v, want %q", err, want)
	}
	codesOf := func(ns string, keys []string) [3]codes.Code {
		items := make([]*pluginv1.KVSetManyItem, len(keys))
		for i, k := range keys {
			items[i] = fakeItem(k, "v", 0)
		}
		_, e1 := f.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: ns, Keys: keys})
		_, e2 := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: ns, Items: items})
		_, e3 := f.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: ns, Keys: keys})
		return [3]codes.Code{status.Code(e1), status.Code(e2), status.Code(e3)}
	}
	invalid := [3]codes.Code{codes.InvalidArgument, codes.InvalidArgument, codes.InvalidArgument}
	for _, ns := range []string{"", "a:b"} {
		if got := codesOf(ns, []string{"k"}); got != invalid {
			t.Errorf("namespace %q: %v", ns, got)
		}
	}
	if got := codesOf("ns", []string{"ok", ""}); got != invalid {
		t.Errorf("empty key: %v", got)
	}
	if got := codesOf("lim", fakeKeys(protocol.KVBatchMaxKeys+1)); got != invalid {
		t.Errorf("%d keys: %v", protocol.KVBatchMaxKeys+1, got)
	}
	if got := codesOf("lim", fakeKeys(protocol.KVBatchMaxKeys)); got != [3]codes.Code{} {
		t.Errorf("%d keys: %v", protocol.KVBatchMaxKeys, got)
	}

	// Payload: 48 values of 64 KiB are the limit of an answer; with their
	// keys they are above the limit of a write.
	fits := protocol.KVBatchMaxBytes / protocol.KVMaxValueBytes
	fill := func(keys []string) []*pluginv1.KVSetManyItem {
		items := make([]*pluginv1.KVSetManyItem, len(keys))
		for i, k := range keys {
			items[i] = &pluginv1.KVSetManyItem{Key: k, Value: bytes.Repeat([]byte("v"), protocol.KVMaxValueBytes)}
		}
		return items
	}
	keys := fakeKeys(fits + 1)
	if _, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "pay", Items: fill(keys[:fits])}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("write above the payload limit: %v", err)
	}
	if got := fakeGetMany(t, f, "pay", keys[0]); got != "-" {
		t.Fatal("the write above the payload limit stored an item")
	}
	for _, part := range [][]string{keys[:fits-1], keys[fits-1:]} {
		if _, err := f.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "pay", Items: fill(part)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "pay", Keys: keys[:fits]}); err != nil {
		t.Fatalf("answer at the payload limit: %v", err)
	}
	if _, err := f.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "pay", Keys: keys}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("answer above the payload limit: %v", err)
	}
}

type kvPlugin struct{ host pluginsdk.Host }

func (p *kvPlugin) Init(_ context.Context, h pluginsdk.Host) error { p.host = h; return nil }

const kvManifest = `{
  "apiVersion": 1, "key": "kvb", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "hostPermissions": [{"id": "kv", "reason": {"en": "x", "zh": "x"}}]
}`

// A plugin's batch calls through the SDK: one host call per batch.
func TestFakeHostKVBatchThroughTheSDK(t *testing.T) {
	fh := NewFakeHost()
	p := &kvPlugin{}
	Start(t, p, Options{Host: fh, Manifest: []byte(kvManifest)})
	ctx, kv := context.Background(), p.host.KV()

	keys := fakeKeys(64)
	items := make([]pluginsdk.KVItem, len(keys))
	for i, k := range keys {
		items[i] = pluginsdk.KVItem{Key: k, Value: []byte("v" + k), TTL: time.Hour}
	}
	if err := pluginsdk.KVSetMany(ctx, kv, "cache", items); err != nil {
		t.Fatalf("KVSetMany: %v", err)
	}
	values, err := pluginsdk.KVGetMany(ctx, kv, "cache", append([]string{"missing"}, keys...))
	if err != nil || len(values) != 65 || values[0].Found {
		t.Fatalf("KVGetMany: %d values, %v", len(values), err)
	}
	for i, v := range values[1:] {
		if !v.Found || string(v.Value) != "v"+keys[i] {
			t.Fatalf("value %d = %v %q", i, v.Found, v.Value)
		}
	}
	if err := pluginsdk.KVDeleteMany(ctx, kv, "cache", keys[:32]); err != nil {
		t.Fatalf("KVDeleteMany: %v", err)
	}
	values, err = pluginsdk.KVGetMany(ctx, kv, "cache", []string{keys[0], keys[31], keys[32], keys[63]})
	if err != nil || values[0].Found || values[1].Found || !values[2].Found || !values[3].Found {
		t.Fatalf("after delete: %v %v", values, err)
	}
	// A write the rules reject stores nothing.
	bad := []pluginsdk.KVItem{{Key: "x", Value: []byte("1")}, {Key: "", Value: []byte("2")}}
	if err := pluginsdk.KVSetMany(ctx, kv, "cache", bad); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid write: %v", err)
	}
	if _, found, _ := kv.Get(ctx, "cache", "x"); found {
		t.Fatal("an invalid write stored an item")
	}
	want := map[string]int{"KVSetMany": 1, "KVGetMany": 2, "KVDeleteMany": 1, "KVGet": 1}
	if got := fh.KVCalls(); !maps.Equal(got, want) {
		t.Fatalf("host calls = %v, want %v", got, want)
	}
}

// The single calls of the fake answer a broken rule with the host's error
// too (protocol.CheckKV*), and store nothing then. A ttl beyond
// protocol.KVMaxTTLSeconds used to overflow and store an expired entry.
func TestFakeHostKVSingleCallsFollowTheHostRules(t *testing.T) {
	f, ctx := NewFakeHost(), context.Background()
	set := func(ns, key string, value []byte, ttl int64) error {
		_, err := f.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: ns, Key: key, Value: value, TtlSeconds: ttl})
		return err
	}
	for want, err := range map[string]error{
		`namespace must be 1..64 bytes without ':*?[]\'`: set("a:b", "k", nil, 0),
		"key must be 1..512 bytes":                       set("ns", "", nil, 0),
		"value exceeds 65536 bytes":                      set("ns", "k", make([]byte, protocol.KVMaxValueBytes+1), 0),
		"ttl_seconds must be 0..9223372036":              set("ns", "k", nil, protocol.KVMaxTTLSeconds+1),
	} {
		if got := status.Convert(err); got.Code() != codes.InvalidArgument || got.Message() != want {
			t.Errorf("KVSet: %v %q, want InvalidArgument %q", got.Code(), got.Message(), want)
		}
	}
	if len(f.kv) != 0 {
		t.Fatalf("a refused KVSet stored %d entries", len(f.kv))
	}
	if _, err := f.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "", Key: "k"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("KVGet, empty namespace: %v", err)
	}
	if _, err := f.KVDelete(ctx, &pluginv1.KVDeleteRequest{Namespace: "ns", Key: strings.Repeat("k", protocol.KVMaxKeyBytes+1)}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("KVDelete, long key: %v", err)
	}
	if _, err := f.KVList(ctx, &pluginv1.KVListRequest{Namespace: "*"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("KVList, bad namespace: %v", err)
	}
	// The largest ttl is stored and does not expire at once.
	if err := set("ns", "k", []byte("v"), protocol.KVMaxTTLSeconds); err != nil {
		t.Fatal(err)
	}
	if r, err := f.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "ns", Key: "k"}); err != nil || !r.GetFound() {
		t.Fatalf("the largest ttl: found %v (%v)", r.GetFound(), err)
	}
}
