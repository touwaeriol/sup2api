package protocol

import (
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These are the rule tables of the plugin KV. The host, the SDK and the fake
// host call the functions tested here, so their own tests only check that
// they do, and what each adds.

func TestKVEntryRules(t *testing.T) {
	for ns, ok := range map[string]bool{
		"n": true, "cache.v2-a_b/c": true, strings.Repeat("n", 64): true,
		"": false, strings.Repeat("n", 65): false,
		"a:b": false, "a*": false, "a?": false, "[a": false, "a]": false, `a\b`: false,
	} {
		if err := CheckKVNamespace(ns); (err == nil) != ok || (!ok && status.Code(err) != codes.InvalidArgument) {
			t.Errorf("CheckKVNamespace(%q) = %v", ns, err)
		}
	}
	for key, ok := range map[string]bool{
		"k": true, "a:b*?[]": true, strings.Repeat("k", 512): true,
		"": false, strings.Repeat("k", 513): false,
	} {
		if err := CheckKVKey(key); (err == nil) != ok {
			t.Errorf("CheckKVKey(%d bytes) = %v", len(key), err)
		}
	}
	for n, ok := range map[int]bool{0: true, KVMaxValueBytes: true, KVMaxValueBytes + 1: false} {
		if err := CheckKVValue(n); (err == nil) != ok {
			t.Errorf("CheckKVValue(%d) = %v", n, err)
		}
	}
	for ttl, ok := range map[int64]bool{
		0: true, 1: true, KVMaxTTLSeconds: true,
		-1: false, KVMaxTTLSeconds + 1: false, math.MaxInt64: false, math.MinInt64: false,
	} {
		if err := CheckKVTTL(ttl); (err == nil) != ok {
			t.Errorf("CheckKVTTL(%d) = %v", ttl, err)
		}
	}
	// Every ttl a write accepts is a time.Duration, and every non-negative
	// time.Duration is a ttl a write accepts.
	if d := time.Duration(KVMaxTTLSeconds) * time.Second; d <= 0 || int64(d/time.Second) != KVMaxTTLSeconds {
		t.Errorf("KVMaxTTLSeconds overflows time.Duration: %v", d)
	}
	if err := CheckKVTTL(int64(time.Duration(math.MaxInt64) / time.Second)); err != nil {
		t.Errorf("the largest time.Duration is not a valid ttl: %v", err)
	}
}

type kvTestItem struct {
	key   string
	value int
	ttl   int64
}

func checkKVTestItems(ns string, items ...kvTestItem) error {
	return CheckKVBatchItems(ns, len(items), func(i int) (string, int, int64) { return items[i].key, items[i].value, items[i].ttl })
}

// wantKVError compares the whole message: it is what a plugin reads, from
// the host, the SDK and the fake host alike. want "" is no error; any other
// is INVALID_ARGUMENT.
func wantKVError(t *testing.T, what string, err error, want string) {
	t.Helper()
	code := codes.InvalidArgument
	if want == "" {
		code = codes.OK
	}
	if got := status.Convert(err); got.Code() != code || got.Message() != want {
		t.Errorf("%s: %v %q, want %v %q", what, got.Code(), got.Message(), code, want)
	}
}

func TestKVBatchRules(t *testing.T) {
	const badNS = `namespace must be 1..64 bytes without ':*?[]\'`
	keys := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "k"
		}
		return out
	}
	ok := kvTestItem{"k", 1, 0}
	items := func(n int, it kvTestItem) []kvTestItem {
		out := make([]kvTestItem, n)
		for i := range out {
			out[i] = it
		}
		return out
	}

	// Keys of a read or a delete.
	wantKVError(t, "no keys", CheckKVBatchKeys("ns", nil), "")
	wantKVError(t, "the most keys", CheckKVBatchKeys("ns", keys(KVBatchMaxKeys)), "")
	wantKVError(t, "too many keys", CheckKVBatchKeys("ns", keys(KVBatchMaxKeys+1)), "at most 512 keys per call")
	wantKVError(t, "bad namespace, no keys", CheckKVBatchKeys("a:b", nil), badNS)
	wantKVError(t, "empty key", CheckKVBatchKeys("ns", []string{"a", "b", "", ""}), "keys[2]: key must be 1..512 bytes")
	wantKVError(t, "long key", CheckKVBatchKeys("ns", []string{strings.Repeat("k", 513)}), "keys[0]: key must be 1..512 bytes")
	// The namespace comes first, then the number of keys, then the keys.
	wantKVError(t, "bad namespace and too many keys", CheckKVBatchKeys("", keys(KVBatchMaxKeys+1)), badNS)
	wantKVError(t, "too many keys, one of them bad", CheckKVBatchKeys("ns", append([]string{""}, keys(KVBatchMaxKeys)...)), "at most 512 keys per call")

	// Items of a write.
	wantKVError(t, "no items", checkKVTestItems("ns"), "")
	wantKVError(t, "the most items", checkKVTestItems("ns", items(KVBatchMaxKeys, ok)...), "")
	wantKVError(t, "too many items", checkKVTestItems("ns", items(KVBatchMaxKeys+1, ok)...), "at most 512 items per call")
	wantKVError(t, "bad namespace, no items", checkKVTestItems("*"), badNS)
	wantKVError(t, "the limits of one item", checkKVTestItems("ns", kvTestItem{strings.Repeat("k", 512), KVMaxValueBytes, KVMaxTTLSeconds}), "")
	for want, bad := range map[string]kvTestItem{
		"items[1]: key must be 1..512 bytes":          {"", 1, 0},
		"items[1]: value exceeds 65536 bytes":         {"k", KVMaxValueBytes + 1, 0},
		"items[1]: ttl_seconds must be 0..9223372036": {"k", 1, -1},
	} {
		wantKVError(t, want, checkKVTestItems("ns", ok, bad, ok), want)
	}
	wantKVError(t, "ttl too large", checkKVTestItems("ns", kvTestItem{"k", 1, KVMaxTTLSeconds + 1}), "items[0]: ttl_seconds must be 0..9223372036")
	// The first item that breaks a rule is the one named, whatever the rule;
	// within one item the key comes first, then the value, then the ttl.
	wantKVError(t, "two bad items", checkKVTestItems("ns", ok, kvTestItem{"k", KVMaxValueBytes + 1, 0}, kvTestItem{"", 1, 0}), "items[1]: value exceeds 65536 bytes")
	wantKVError(t, "one item, all bad", checkKVTestItems("ns", kvTestItem{"", KVMaxValueBytes + 1, -1}), "items[0]: key must be 1..512 bytes")
	wantKVError(t, "one item, value and ttl bad", checkKVTestItems("ns", kvTestItem{"k", KVMaxValueBytes + 1, -1}), "items[0]: value exceeds 65536 bytes")

	// Payload of a write: keys plus values. 48 values of 64 KiB are the
	// limit without their keys, so 47 are the most that fit with them.
	full := kvTestItem{"k", KVMaxValueBytes, 0}
	fits := KVBatchMaxBytes / KVMaxValueBytes
	wantKVError(t, "47 full values", checkKVTestItems("ns", items(fits-1, full)...), "")
	wantKVError(t, "48 full values and their keys", checkKVTestItems("ns", items(fits, full)...), "keys and values exceed 3145728 bytes; write fewer items per call")
	exact := append(items(fits-1, full), kvTestItem{"k", KVMaxValueBytes - fits, 0})
	wantKVError(t, "exactly the payload limit", checkKVTestItems("ns", exact...), "")
	// An invalid item is reported before the payload.
	wantKVError(t, "payload and a bad item", checkKVTestItems("ns", append(items(fits, full), kvTestItem{"", 1, 0})...), "items[48]: key must be 1..512 bytes")

	// Values of an answer: 48 of 64 KiB always fit.
	wantKVError(t, "answer at the limit", CheckKVBatchAnswer(fits*KVMaxValueBytes), "")
	if got, want := status.Convert(CheckKVBatchAnswer(KVBatchMaxBytes+1)), "the values of these keys exceed 3145728 bytes; ask for fewer keys per call"; got.Code() != codes.ResourceExhausted || got.Message() != want {
		t.Errorf("answer above the limit: %v %q, want ResourceExhausted %q", got.Code(), got.Message(), want)
	}
}
