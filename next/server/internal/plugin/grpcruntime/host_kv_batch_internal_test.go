package grpcruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/cluster"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

var kvGrant = registry.Grants{"kv": json.RawMessage(`{}`)}

// redisCalls records every command the host sends to Redis: one entry is
// one round trip.
type redisCalls struct {
	mu   sync.Mutex
	cmds []string
}

func (c *redisCalls) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *redisCalls) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.mu.Lock()
		c.cmds = append(c.cmds, cmd.Name())
		c.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (c *redisCalls) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		c.mu.Lock()
		c.cmds = append(c.cmds, fmt.Sprintf("pipeline(%d)", len(cmds)))
		c.mu.Unlock()
		return next(ctx, cmds)
	}
}

// take returns the commands recorded since the last take.
func (c *redisCalls) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.cmds
	c.cmds = nil
	return out
}

// kvRedis is a miniredis with a client that records its commands.
func kvRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, *redisCalls) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	calls := &redisCalls{}
	rdb.AddHook(calls)
	return mr, rdb, calls
}

// kvEnv is a hostServer for plugin key on rdb, without a process. db may be
// nil: the grant is then only checked against the instance's settings.
func kvEnv(t *testing.T, key string, grants registry.Grants, rdb redis.UniversalClient, db *store.DB) *hostServer {
	t.Helper()
	m := registrytest.Manifest(key, "1.0.0")
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	rt := &Runtime{o: Options{Node: staticNode{"n", "b"}, Concurrency: Concurrency{Hot: 4, Console: 4, Background: 4},
		DataDir: t.TempDir(), Redis: rdb, DB: db}, log: slog.Default()}
	i := newInstance(rt, pkg, "", "", &settings{configJSON: "{}", grants: grants})
	return &hostServer{i: i}
}

func setItem(key, value string, ttl int64) *pluginv1.KVSetManyItem {
	return &pluginv1.KVSetManyItem{Key: key, Value: []byte(value), TtlSeconds: ttl}
}

func kvSetMany(t *testing.T, h *hostServer, ns string, items ...*pluginv1.KVSetManyItem) {
	t.Helper()
	if _, err := h.KVSetMany(context.Background(), &pluginv1.KVSetManyRequest{Namespace: ns, Items: items}); err != nil {
		t.Fatalf("KVSetMany: %v", err)
	}
}

func kvGetMany(t *testing.T, h *hostServer, ns string, keys ...string) []*pluginv1.KVGetManyItem {
	t.Helper()
	r, err := h.KVGetMany(context.Background(), &pluginv1.KVGetManyRequest{Namespace: ns, Keys: keys})
	if err != nil {
		t.Fatalf("KVGetMany: %v", err)
	}
	return r.GetItems()
}

// show renders the answer of KVGetMany: "-" for a missing key.
func show(items []*pluginv1.KVGetManyItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = "-"
		if it.GetFound() {
			parts[i] = "(" + string(it.GetValue()) + ")"
		}
	}
	return strings.Join(parts, " ")
}

// kvBatchCodes runs the three batch calls with the same namespace and keys
// and returns their status codes.
func kvBatchCodes(h *hostServer, ns string, keys []string) [3]codes.Code {
	ctx := context.Background()
	items := make([]*pluginv1.KVSetManyItem, len(keys))
	for i, k := range keys {
		items[i] = setItem(k, "v", 0)
	}
	_, e1 := h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: ns, Keys: keys})
	_, e2 := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: ns, Items: items})
	_, e3 := h.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: ns, Keys: keys})
	return [3]codes.Code{status.Code(e1), status.Code(e2), status.Code(e3)}
}

func allCodes(c codes.Code) [3]codes.Code { return [3]codes.Code{c, c, c} }

func numberedKeys(prefix string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s%04d", prefix, i)
	}
	return keys
}

func TestKVGetManyKeepsRequestOrderAndReportsMissingKeys(t *testing.T) {
	_, rdb, _ := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	ctx := context.Background()
	for k, v := range map[string]string{"a": "1", "b": "2", "c": "3", "empty": ""} {
		if _, err := h.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "n", Key: k, Value: []byte(v)}); err != nil {
			t.Fatal(err)
		}
	}
	got := kvGetMany(t, h, "n", "c", "missing", "a", "a", "empty", "b")
	if want := "(3) - (1) (1) () (2)"; show(got) != want {
		t.Fatalf("items = %s, want %s", show(got), want)
	}
	// No keys: an empty answer, not an error.
	if got := kvGetMany(t, h, "n"); len(got) != 0 {
		t.Fatalf("no keys: %s", show(got))
	}
}

// KVSet takes the ttl of the batch write: 0 up to protocol.KVMaxTTLSeconds.
// A larger one used to overflow time.Duration and store the key without an
// expiry.
func TestKVSetRefusesATTLThatOverflows(t *testing.T) {
	mr, rdb, calls := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	ctx := context.Background()
	set := func(ttl int64) error {
		_, err := h.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "n", Key: "k", Value: []byte("v"), TtlSeconds: ttl})
		return err
	}
	for _, ttl := range []int64{-1, protocol.KVMaxTTLSeconds + 1, 18446744073, 1 << 62} {
		if err := set(ttl); status.Code(err) != codes.InvalidArgument {
			t.Errorf("ttl_seconds %d: %v, want InvalidArgument", ttl, err)
		}
	}
	if got := calls.take(); len(got) != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("a refused KVSet reached Redis: %v, keys %v", got, mr.Keys())
	}
	if err := set(protocol.KVMaxTTLSeconds); err != nil {
		t.Fatalf("the largest ttl: %v", err)
	}
	if got := mr.TTL("plugin:kv:pa:n:k"); got != time.Duration(protocol.KVMaxTTLSeconds)*time.Second {
		t.Fatalf("largest ttl = %v", got)
	}
}

func TestKVSetManyWritesEveryItemWithItsOwnTTL(t *testing.T) {
	mr, rdb, _ := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	ctx := context.Background()
	// "keep" has a ttl that a batch write without one must clear, exactly as
	// KVSet does.
	if _, err := h.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "n", Key: "keep", Value: []byte("old"), TtlSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	kvSetMany(t, h, "n", setItem("short", "s", 60), setItem("keep", "new", 0), setItem("long", "l", 86400), setItem("dup", "first", 10), setItem("dup", "last", 20))
	for key, want := range map[string]time.Duration{"short": time.Minute, "keep": 0, "long": 24 * time.Hour, "dup": 20 * time.Second} {
		if got := mr.TTL("plugin:kv:pa:n:" + key); got != want {
			t.Errorf("ttl of %s = %v, want %v", key, got, want)
		}
	}
	// The single calls read what the batch wrote, and the other way round.
	if r, err := h.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "n", Key: "keep"}); err != nil || string(r.GetValue()) != "new" {
		t.Fatalf("KVGet keep = %q %v", r.GetValue(), err)
	}
	if got, want := show(kvGetMany(t, h, "n", "short", "keep", "long", "dup")), "(s) (new) (l) (last)"; got != want {
		t.Fatalf("items = %s, want %s", got, want)
	}
	mr.FastForward(61 * time.Second)
	if got, want := show(kvGetMany(t, h, "n", "short", "keep", "long", "dup")), "- (new) (l) -"; got != want {
		t.Fatalf("after 61s = %s, want %s", got, want)
	}
	// The largest ttl is accepted by Redis.
	kvSetMany(t, h, "n", setItem("forever", "f", protocol.KVMaxTTLSeconds))
	if got := mr.TTL("plugin:kv:pa:n:forever"); got != time.Duration(protocol.KVMaxTTLSeconds)*time.Second {
		t.Fatalf("largest ttl = %v", got)
	}
}

func TestKVSetManyWritesNothingWhenOneItemIsInvalid(t *testing.T) {
	mr, rdb, calls := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	kvSetMany(t, h, "n", setItem("old", "before", 0))
	calls.take()
	big := strings.Repeat("x", protocol.KVMaxValueBytes+1)
	for name, bad := range map[string]*pluginv1.KVSetManyItem{
		"empty key":     setItem("", "v", 0),
		"long key":      setItem(strings.Repeat("k", protocol.KVMaxKeyBytes+1), "v", 0),
		"value too big": setItem("big", big, 0),
		"negative ttl":  setItem("neg", "v", -1),
		"ttl too large": setItem("huge", "v", protocol.KVMaxTTLSeconds+1),
		"missing (nil)": nil,
	} {
		// The bad item stands in the middle: the items before it must not be
		// written either.
		items := []*pluginv1.KVSetManyItem{setItem("first", "1", 0), setItem("old", "after", 0), bad, setItem("last", "2", 0)}
		_, err := h.KVSetMany(context.Background(), &pluginv1.KVSetManyRequest{Namespace: "n", Items: items})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(status.Convert(err).Message(), "items[2]") {
			t.Errorf("%s: the error does not name the item: %v", name, err)
		}
		if got := calls.take(); len(got) != 0 {
			t.Fatalf("%s: an invalid batch reached Redis: %v", name, got)
		}
		if keys := mr.Keys(); !slices.Equal(keys, []string{"plugin:kv:pa:n:old"}) {
			t.Fatalf("%s: keys after the rejected batch = %v", name, keys)
		}
		if v, _ := mr.Get("plugin:kv:pa:n:old"); v != "before" {
			t.Fatalf("%s: existing value was overwritten: %q", name, v)
		}
	}
	// The error is the one of protocol.CheckKVBatchItems, which the SDK and
	// the fake host return too: it names the first item that breaks a rule.
	_, err := h.KVSetMany(context.Background(), &pluginv1.KVSetManyRequest{Namespace: "n", Items: []*pluginv1.KVSetManyItem{
		setItem("first", "1", 0), setItem("big", big, 0), setItem("", "v", 0)}})
	if got, want := status.Convert(err).Message(), "items[1]: value exceeds 65536 bytes"; status.Code(err) != codes.InvalidArgument || got != want {
		t.Fatalf("two invalid items: %v, want %q", err, want)
	}
	// A value of exactly the limit is fine.
	kvSetMany(t, h, "n", setItem("first", "1", 0), setItem("exact", big[:protocol.KVMaxValueBytes], 0))
	if v, _ := mr.Get("plugin:kv:pa:n:exact"); len(v) != protocol.KVMaxValueBytes {
		t.Fatalf("value at the limit: %d bytes", len(v))
	}
}

func TestKVBatchLimits(t *testing.T) {
	mr, rdb, calls := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	ctx := context.Background()

	// Keys per call.
	if got := kvBatchCodes(h, "n", numberedKeys("k", protocol.KVBatchMaxKeys)); got != allCodes(codes.OK) {
		t.Fatalf("%d keys: %v", protocol.KVBatchMaxKeys, got)
	}
	calls.take()
	if got := kvBatchCodes(h, "n", numberedKeys("k", protocol.KVBatchMaxKeys+1)); got != allCodes(codes.InvalidArgument) {
		t.Fatalf("%d keys: %v", protocol.KVBatchMaxKeys+1, got)
	}
	if got := calls.take(); len(got) != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("an oversized batch reached Redis: %v, keys %d", got, len(mr.Keys()))
	}

	// Payload of one write: keys plus values.
	value := bytes.Repeat([]byte("v"), protocol.KVMaxValueBytes)
	items := func(keys []string) []*pluginv1.KVSetManyItem {
		out := make([]*pluginv1.KVSetManyItem, len(keys))
		for i, k := range keys {
			out[i] = &pluginv1.KVSetManyItem{Key: k, Value: value}
		}
		return out
	}
	fits := protocol.KVBatchMaxBytes / protocol.KVMaxValueBytes // 48 values of 64 KiB are exactly the limit
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items(numberedKeys("a", fits))}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("write above the payload limit (values at the limit plus keys): %v", err)
	}
	if got := calls.take(); len(got) != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("a write above the payload limit reached Redis: %v, keys %d", got, len(mr.Keys()))
	}
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items(numberedKeys("a", fits-1))}); err != nil {
		t.Fatalf("write under the payload limit: %v", err)
	}
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items(numberedKeys("b", 2))}); err != nil {
		t.Fatal(err)
	}

	// Payload of one answer: the values found.
	keys := append(numberedKeys("a", fits-1), numberedKeys("b", 2)...)
	if got := kvGetMany(t, h, "n", keys[:fits]...); len(got) != fits || !got[fits-1].GetFound() {
		t.Fatalf("answer at the payload limit: %d items", len(got))
	}
	_, err := h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "n", Keys: keys})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("answer above the payload limit: %v", err)
	}
}

func TestKVBatchIsScopedToThePluginAndNamespace(t *testing.T) {
	mr, rdb, _ := kvRedis(t)
	pa := kvEnv(t, "pa", kvGrant, rdb, nil)
	pb := kvEnv(t, "pb", kvGrant, rdb, nil)
	ctx := context.Background()
	kvSetMany(t, pa, "n", setItem("k1", "a1", 0), setItem("k2", "a2", 0))
	kvSetMany(t, pb, "n", setItem("k2", "b2", 0))
	kvSetMany(t, pa, "other", setItem("k1", "o1", 0))
	if want := []string{"plugin:kv:pa:n:k1", "plugin:kv:pa:n:k2", "plugin:kv:pa:other:k1", "plugin:kv:pb:n:k2"}; !slices.Equal(mr.Keys(), want) {
		t.Fatalf("redis keys = %v, want %v", mr.Keys(), want)
	}
	if got, want := show(kvGetMany(t, pb, "n", "k1", "k2")), "- (b2)"; got != want {
		t.Fatalf("plugin pb reads %s, want %s", got, want)
	}
	if got, want := show(kvGetMany(t, pa, "other", "k1", "k2")), "(o1) -"; got != want {
		t.Fatalf("namespace other reads %s, want %s", got, want)
	}
	if _, err := pb.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: "n", Keys: []string{"k1", "k2", "missing"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := show(kvGetMany(t, pa, "n", "k1", "k2")), "(a1) (a2)"; got != want {
		t.Fatalf("plugin pb deleted plugin pa's keys: %s", got)
	}
	if got := show(kvGetMany(t, pb, "n", "k2")); got != "-" {
		t.Fatalf("pb's own key after its delete: %s", got)
	}
	// A key cannot leave its namespace: it is stored as given.
	kvSetMany(t, pa, "n", setItem("x:other:k1", "v", 0), setItem("*", "star", 0))
	if v, _ := mr.Get("plugin:kv:pa:other:k1"); v != "o1" {
		t.Fatalf("a key with ':' wrote into another namespace: %q", v)
	}
	// The namespace rule of the single calls (the table is in sdk/protocol),
	// checked even without keys.
	for _, ns := range []string{"", "a:b", strings.Repeat("n", protocol.KVMaxNamespaceBytes+1)} {
		for _, keys := range [][]string{{"k"}, nil} {
			if got := kvBatchCodes(pa, ns, keys); got != allCodes(codes.InvalidArgument) {
				t.Errorf("namespace %q with %d keys: %v", ns, len(keys), got)
			}
		}
	}
	for _, key := range []string{"", strings.Repeat("k", protocol.KVMaxKeyBytes+1)} {
		if got := kvBatchCodes(pa, "n", []string{"ok", key}); got != allCodes(codes.InvalidArgument) {
			t.Errorf("key of %d bytes: %v", len(key), got)
		}
	}
}

func TestKVDeleteManyDeletesOnlyTheGivenKeys(t *testing.T) {
	mr, rdb, _ := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	kvSetMany(t, h, "n", setItem("a", "1", 0), setItem("b", "2", 60), setItem("c", "3", 0))
	if _, err := h.KVDeleteMany(context.Background(), &pluginv1.KVDeleteManyRequest{Namespace: "n", Keys: []string{"a", "missing", "b", "a"}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"plugin:kv:pa:n:c"}; !slices.Equal(mr.Keys(), want) {
		t.Fatalf("keys after delete = %v, want %v", mr.Keys(), want)
	}
	if _, err := h.KVDeleteMany(context.Background(), &pluginv1.KVDeleteManyRequest{Namespace: "n"}); err != nil {
		t.Fatalf("no keys: %v", err)
	}
}

func TestKVBatchNeedsTheGrant(t *testing.T) {
	mr, rdb, calls := kvRedis(t)
	h := kvEnv(t, "pa", registry.Grants{"lock": json.RawMessage(`{}`)}, rdb, nil)
	if got := kvBatchCodes(h, "n", []string{"k"}); got != allCodes(codes.PermissionDenied) {
		t.Fatalf("without the kv grant: %v", got)
	}
	// The grant is checked before the arguments, as in the single calls.
	if got := kvBatchCodes(h, "bad:ns", nil); got != allCodes(codes.PermissionDenied) {
		t.Fatalf("without the kv grant, bad arguments: %v", got)
	}
	if got := calls.take(); len(got) != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("a call without the grant reached Redis: %v", got)
	}
}

func TestKVBatchUnavailableWhenRedisIsDown(t *testing.T) {
	mr := miniredis.RunT(t)
	// No retries: the test only needs the first refused connection.
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialerRetries: 1, DialTimeout: time.Second})
	t.Cleanup(func() { _ = rdb.Close() })
	h := kvEnv(t, "pa", kvGrant, rdb, nil)
	mr.Close()
	if got := kvBatchCodes(h, "n", []string{"k"}); got != allCodes(codes.Unavailable) {
		t.Fatalf("redis down: %v", got)
	}
}

// kvScriptRefusesBadArguments hands the host's script a bad ttl or an odd
// argument list for the keys k1 and k2 (the host never does): it must refuse
// them before it writes, so neither key exists afterwards.
func kvScriptRefusesBadArguments(t *testing.T, rdb redis.UniversalClient, k1, k2 string) {
	t.Helper()
	ctx := context.Background()
	for name, args := range map[string][]any{
		"negative ttl":       {"v1", "0", "v2", "-1"},
		"ttl not a number":   {"v1", "60", "v2", "soon"},
		"fractional ttl":     {"v1", "60", "v2", "1.5"},
		"missing arguments":  {"v1", "0", "v2"},
		"too many arguments": {"v1", "0", "v2", "0", "v3", "0"},
	} {
		if err := kvSetManyScript.Run(ctx, rdb, []string{k1, k2}, args...).Err(); err == nil || !strings.Contains(err.Error(), "kv batch") {
			t.Errorf("script with %s: %v", name, err)
		}
		if n, err := rdb.Exists(ctx, k1, k2).Result(); err != nil || n != 0 {
			t.Fatalf("script with %s wrote %d keys (%v)", name, n, err)
		}
	}
}

// The script checks its arguments before the first write. miniredis runs it
// here; TestKVBatchRealRedis does the same on a real server.
func TestKVSetManyScriptChecksItsArguments(t *testing.T) {
	mr, rdb, _ := kvRedis(t)
	kvScriptRefusesBadArguments(t, rdb, "plugin:kv:pa:n:1", "plugin:kv:pa:n:2")
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("the script wrote %v", keys)
	}
}

// TestKVBatchChecksTheGrantOnceAndMakesOneRedisRoundTrip is the point of the
// batch calls: whatever the number of keys, one grant query in PostgreSQL
// and one command to Redis.
func TestKVBatchChecksTheGrantOnceAndMakesOneRedisRoundTrip(t *testing.T) {
	base := testutil.DB(t)
	ctx := context.Background()
	if _, err := base.SQL.ExecContext(ctx, `INSERT INTO plugins(key,name,status)VALUES('pa','{}','enabled');INSERT INTO plugin_permission_grants(plugin_key,permission,scope,status,plugin_version,manifest_hash)VALUES('pa','kv','{}','granted','1.0.0','hash')`); err != nil {
		t.Fatal(err)
	}
	db, queries := testutil.Traced(base)
	_, rdb, calls := kvRedis(t)
	h := kvEnv(t, "pa", kvGrant, rdb, db)

	keys := numberedKeys("k", 64)
	items := make([]*pluginv1.KVSetManyItem, len(keys))
	for i, k := range keys {
		items[i] = setItem(k, "v"+k, int64(60+i))
	}
	expect := func(what string, wantRedis ...string) {
		t.Helper()
		if got := queries.Take(); len(got) != 1 || !strings.Contains(got[0], "plugin_permission_grants") {
			t.Errorf("%s: PostgreSQL statements = %q, want the one grant check", what, got)
		}
		if got := calls.take(); !slices.Equal(got, wantRedis) {
			t.Errorf("%s: Redis commands = %v, want %v", what, got, wantRedis)
		}
	}

	// A Redis server that has not seen the host's script yet answers
	// NOSCRIPT to EVALSHA once and gets the script with EVAL.
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items}); err != nil {
		t.Fatal(err)
	}
	expect("first KVSetMany on this Redis", "evalsha", "eval")
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items}); err != nil {
		t.Fatal(err)
	}
	expect("KVSetMany", "evalsha")

	got, err := h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "n", Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	expect("KVGetMany", "mget")
	for i, it := range got.GetItems() {
		if !it.GetFound() || string(it.GetValue()) != "v"+keys[i] {
			t.Fatalf("item %d = %v %q", i, it.GetFound(), it.GetValue())
		}
	}

	if _, err := h.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: "n", Keys: keys}); err != nil {
		t.Fatal(err)
	}
	expect("KVDeleteMany", "del")

	// No keys: the grant is still checked, Redis is not asked.
	if got := kvBatchCodes(h, "n", nil); got != allCodes(codes.OK) {
		t.Fatalf("no keys: %v", got)
	}
	if got := queries.Take(); len(got) != 3 {
		t.Errorf("three calls without keys: %d PostgreSQL statements, want 3", len(got))
	}
	if got := calls.take(); len(got) != 0 {
		t.Errorf("calls without keys reached Redis: %v", got)
	}

	// A grant revoked in PostgreSQL stops the next call, before Redis.
	if _, err := base.SQL.ExecContext(ctx, `UPDATE plugin_permission_grants SET status='revoked'`); err != nil {
		t.Fatal(err)
	}
	if got := kvBatchCodes(h, "n", keys); got != allCodes(codes.PermissionDenied) {
		t.Fatalf("revoked grant: %v", got)
	}
	if got := calls.take(); len(got) != 0 {
		t.Fatalf("a call with a revoked grant reached Redis: %v", got)
	}
}

// Against a real server (CI sets TEST_REDIS_URL, Valkey or Redis; skipped
// locally): the host's script on a real Lua engine, MGET and DEL with the
// largest number of keys, real ttls and real expiry.
func TestKVBatchRealRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opt, err := cluster.ParseRedisURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	plugin := "kvt" + hex.EncodeToString(b)
	prefix := "plugin:kv:" + plugin + ":"
	stored := func() []string {
		t.Helper()
		keys, err := rdb.Keys(ctx, prefix+"*").Result()
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(keys)
		return keys
	}
	t.Cleanup(func() {
		if keys, _ := rdb.Keys(context.Background(), prefix+"*").Result(); len(keys) > 0 {
			_ = rdb.Del(context.Background(), keys...).Err()
		}
	})
	calls := &redisCalls{}
	rdb.AddHook(calls)
	h := kvEnv(t, plugin, kvGrant, rdb, nil)

	// The largest batch, three kinds of ttl, binary values.
	keys := numberedKeys("k", protocol.KVBatchMaxKeys)
	ttls := []int64{0, 60, 3600}
	items := make([]*pluginv1.KVSetManyItem, len(keys))
	for i, k := range keys {
		items[i] = &pluginv1.KVSetManyItem{Key: k, Value: []byte{0, 0xff, byte(i), byte(i >> 8), '\n'}, TtlSeconds: ttls[i%3]}
	}
	calls.take()
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items}); err != nil {
		t.Fatalf("KVSetMany: %v", err)
	}
	// EVALSHA, and EVAL once if this server has not seen the script.
	if got := calls.take(); !slices.Equal(got, []string{"evalsha"}) && !slices.Equal(got, []string{"evalsha", "eval"}) {
		t.Fatalf("first KVSetMany: Redis commands = %v", got)
	}
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "n", Items: items}); err != nil {
		t.Fatalf("KVSetMany: %v", err)
	}
	if got := calls.take(); !slices.Equal(got, []string{"evalsha"}) {
		t.Fatalf("KVSetMany: Redis commands = %v, want one EVALSHA", got)
	}
	for i := range 6 {
		ttl, err := rdb.TTL(ctx, prefix+"n:"+keys[i]).Result()
		want := time.Duration(ttls[i%3]) * time.Second
		if err != nil || (want == 0 && ttl != -1) || (want > 0 && (ttl > want || ttl < want-5*time.Second)) {
			t.Errorf("ttl of %s = %v (%v), want %v", keys[i], ttl, err, want)
		}
	}
	calls.take()
	got, err := h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "n", Keys: append([]string{"missing"}, keys[:protocol.KVBatchMaxKeys-1]...)})
	if err != nil {
		t.Fatalf("KVGetMany: %v", err)
	}
	if c := calls.take(); !slices.Equal(c, []string{"mget"}) {
		t.Fatalf("KVGetMany: Redis commands = %v, want one MGET", c)
	}
	if len(got.GetItems()) != protocol.KVBatchMaxKeys || got.GetItems()[0].GetFound() {
		t.Fatalf("KVGetMany: %d items, missing key found = %v", len(got.GetItems()), got.GetItems()[0].GetFound())
	}
	for i, it := range got.GetItems()[1:] {
		if !it.GetFound() || !bytes.Equal(it.GetValue(), items[i].GetValue()) {
			t.Fatalf("item %d = %v %x, want %x", i, it.GetFound(), it.GetValue(), items[i].GetValue())
		}
	}

	// The largest ttl a batch accepts is one the server accepts; a ttl of
	// one second really expires.
	kvSetMany(t, h, "t", setItem("forever", "f", protocol.KVMaxTTLSeconds), setItem("brief", "b", 1), setItem("stays", "s", 0))
	if ttl, err := rdb.TTL(ctx, prefix+"t:forever").Result(); err != nil || ttl < 290*365*24*time.Hour {
		t.Fatalf("largest ttl = %v %v", ttl, err)
	}
	time.Sleep(1200 * time.Millisecond)
	if got, want := show(kvGetMany(t, h, "t", "forever", "brief", "stays")), "(f) - (s)"; got != want {
		t.Fatalf("after 1.2s = %s, want %s", got, want)
	}

	// 47 full-size values: one command each way.
	value := bytes.Repeat([]byte("v"), protocol.KVMaxValueBytes)
	bigKeys := numberedKeys("big", 47)
	big := make([]*pluginv1.KVSetManyItem, len(bigKeys))
	for i, k := range bigKeys {
		big[i] = &pluginv1.KVSetManyItem{Key: k, Value: value, TtlSeconds: 60}
	}
	if _, err := h.KVSetMany(ctx, &pluginv1.KVSetManyRequest{Namespace: "big", Items: big}); err != nil {
		t.Fatalf("KVSetMany, 47 x 64 KiB: %v", err)
	}
	if r, err := h.KVGetMany(ctx, &pluginv1.KVGetManyRequest{Namespace: "big", Keys: bigKeys}); err != nil || len(r.GetItems()[46].GetValue()) != protocol.KVMaxValueBytes {
		t.Fatalf("KVGetMany, 47 x 64 KiB: %v", err)
	}

	// The script's own argument check, on a real Lua engine.
	before := stored()
	kvScriptRefusesBadArguments(t, rdb, prefix+"s:1", prefix+"s:2")
	if after := stored(); !slices.Equal(after, before) {
		t.Fatalf("the script wrote %d keys", len(after)-len(before))
	}

	// One DEL for all keys of a call; other namespaces stay.
	calls.take()
	if _, err := h.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: "n", Keys: keys}); err != nil {
		t.Fatalf("KVDeleteMany: %v", err)
	}
	if c := calls.take(); !slices.Equal(c, []string{"del"}) {
		t.Fatalf("KVDeleteMany: Redis commands = %v, want one DEL", c)
	}
	if _, err := h.KVDeleteMany(ctx, &pluginv1.KVDeleteManyRequest{Namespace: "big", Keys: bigKeys}); err != nil {
		t.Fatalf("KVDeleteMany: %v", err)
	}
	if got, want := stored(), []string{prefix + "t:forever", prefix + "t:stays"}; !slices.Equal(got, want) {
		t.Fatalf("keys left = %v, want %v", got, want)
	}
}
