package protocol

import (
	"cmp"
	"math"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Plugin KV rules (HostService.KVGet / KVSet / KVDelete / KVList and the
// batch calls KVGetMany / KVSetMany / KVDeleteMany, CONTRACTS §93). They are
// written once, here: the host enforces them with the Check functions below,
// the SDK applies them before it sends a batch (so that a batch it serves
// with single calls, for a KV without batch calls, follows the same rules) and the
// fake host of pluginsdktest answers like the host.
const (
	// KVMaxNamespaceBytes, KVMaxKeyBytes and KVMaxValueBytes bound one
	// entry.
	KVMaxNamespaceBytes = 64
	KVMaxKeyBytes       = 512
	KVMaxValueBytes     = 64 << 10

	// KVNamespaceForbidden lists the bytes a namespace must not contain.
	KVNamespaceForbidden = `:*?[]\`

	// KVMaxTTLSeconds is the largest ttl_seconds of a write: the largest
	// whole number of seconds a time.Duration holds (about 292 years).
	KVMaxTTLSeconds = int64(math.MaxInt64 / int64(time.Second))

	// KVBatchMaxKeys caps the keys (or items) of one batch call.
	KVBatchMaxKeys = 512
	// KVBatchMaxBytes caps the payload of one batch call: the keys plus the
	// values of a KVSetMany request, and the values of a KVGetMany answer.
	// It is KV's own storage rule, as KVMaxValueBytes is for one value: the
	// HostService call that carries the batch has no smaller bound (the
	// broker receives up to gRPC's math.MaxInt32).
	KVBatchMaxBytes = 3 << 20
)

// The Check functions return nil for an argument that follows the rules and
// otherwise the gRPC status the host answers: INVALID_ARGUMENT
// (CheckKVBatchAnswer: RESOURCE_EXHAUSTED) with a message that says which
// rule is broken. The SDK and the fake host return the same error, so the
// three name the same item with the same words.

// CheckKVNamespace: 1..64 bytes without any of KVNamespaceForbidden.
func CheckKVNamespace(ns string) error {
	if ns == "" || len(ns) > KVMaxNamespaceBytes || strings.ContainsAny(ns, KVNamespaceForbidden) {
		return kvInvalid("namespace must be 1..%d bytes without '%s'", KVMaxNamespaceBytes, KVNamespaceForbidden)
	}
	return nil
}

// CheckKVKey: 1..512 bytes.
func CheckKVKey(key string) error {
	if key == "" || len(key) > KVMaxKeyBytes {
		return kvInvalid("key must be 1..%d bytes", KVMaxKeyBytes)
	}
	return nil
}

// CheckKVValue: a value of valueBytes bytes, at most 64 KiB.
func CheckKVValue(valueBytes int) error {
	if valueBytes > KVMaxValueBytes {
		return kvInvalid("value exceeds %d bytes", KVMaxValueBytes)
	}
	return nil
}

// CheckKVTTL: the ttl_seconds of a write, 0 (no expiry) up to
// KVMaxTTLSeconds.
func CheckKVTTL(ttlSeconds int64) error {
	if ttlSeconds < 0 || ttlSeconds > KVMaxTTLSeconds {
		return kvInvalid("ttl_seconds must be 0..%d", KVMaxTTLSeconds)
	}
	return nil
}

// CheckKVBatchKeys checks one KVGetMany or KVDeleteMany request: the
// namespace, the number of keys, then every key in request order. The error
// of a key names it: "keys[3]: key must be 1..512 bytes".
func CheckKVBatchKeys(ns string, keys []string) error {
	if err := checkKVBatch(ns, "keys", len(keys)); err != nil {
		return err
	}
	for i, key := range keys {
		if err := CheckKVKey(key); err != nil {
			return kvItemError("keys", i, err)
		}
	}
	return nil
}

// CheckKVBatchItems checks one KVSetMany request of n items: the namespace,
// the number of items, then every item in request order (its key, then its
// value, then its ttl), and last the payload of the whole request. item
// returns the key, the length of the value and the ttl_seconds of item i.
// The error of an item names the first one that breaks a rule:
// "items[2]: value exceeds 65536 bytes".
func CheckKVBatchItems(ns string, n int, item func(i int) (key string, valueBytes int, ttlSeconds int64)) error {
	if err := checkKVBatch(ns, "items", n); err != nil {
		return err
	}
	total := 0
	for i := range n {
		key, valueBytes, ttlSeconds := item(i)
		if err := cmp.Or(CheckKVKey(key), CheckKVValue(valueBytes), CheckKVTTL(ttlSeconds)); err != nil {
			return kvItemError("items", i, err)
		}
		total += len(key) + valueBytes
	}
	if total > KVBatchMaxBytes {
		return kvInvalid("keys and values exceed %d bytes; write fewer items per call", KVBatchMaxBytes)
	}
	return nil
}

// CheckKVBatchAnswer checks the values of one KVGetMany answer, valueBytes
// bytes in all (or so far: once it fails, more values do not help).
func CheckKVBatchAnswer(valueBytes int) error {
	if valueBytes > KVBatchMaxBytes {
		return status.Errorf(codes.ResourceExhausted, "the values of these keys exceed %d bytes; ask for fewer keys per call", KVBatchMaxBytes)
	}
	return nil
}

// checkKVBatch checks what every batch request has: the namespace and the
// number n of its keys or items; what names them in the error.
func checkKVBatch(ns, what string, n int) error {
	if err := CheckKVNamespace(ns); err != nil {
		return err
	}
	if n > KVBatchMaxKeys {
		return kvInvalid("at most %d %s per call", KVBatchMaxKeys, what)
	}
	return nil
}

func kvInvalid(format string, a ...any) error {
	return status.Errorf(codes.InvalidArgument, format, a...)
}

// kvItemError is err, the rule that key or item i of a batch breaks, with
// its place in the request.
func kvItemError(what string, i int, err error) error {
	return kvInvalid("%s[%d]: %s", what, i, status.Convert(err).Message())
}
