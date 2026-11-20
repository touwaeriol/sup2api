package codex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// failingKV fails every call, as the host KV does when Redis is down.
type failingKV struct{}

var errKVDown = errors.New("kv unavailable")

func (failingKV) Get(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, errKVDown
}
func (failingKV) Set(context.Context, string, string, []byte, time.Duration) error { return errKVDown }
func (failingKV) Delete(context.Context, string, string) error                     { return errKVDown }
func (failingKV) List(context.Context, string, string, string, int) ([]string, string, error) {
	return nil, "", errKVDown
}

// batchKV serves the host KV and counts the single and batch reads.
type batchKV struct {
	pluginsdk.KV
	gets, getManys atomic.Int32
}

func (k *batchKV) Get(ctx context.Context, ns, key string) ([]byte, bool, error) {
	k.gets.Add(1)
	return k.KV.Get(ctx, ns, key)
}

func (k *batchKV) GetMany(ctx context.Context, ns string, keys []string) ([]pluginsdk.KVValue, error) {
	k.getManys.Add(1)
	return pluginsdk.KVGetMany(ctx, k.KV, ns, keys)
}

func (k *batchKV) SetMany(ctx context.Context, ns string, items []pluginsdk.KVItem) error {
	return pluginsdk.KVSetMany(ctx, k.KV, ns, items)
}

func (k *batchKV) DeleteMany(ctx context.Context, ns string, keys []string) error {
	return pluginsdk.KVDeleteMany(ctx, k.KV, ns, keys)
}

// summary asks POST /reset-credits/summary for ids and returns the counts.
func (e *resetEnv) summary(t *testing.T, ids ...int64) map[string]ResetCount {
	t.Helper()
	r := e.h.Do("POST", "/reset-credits/summary", nil, map[string]any{"account_ids": ids})
	if r.GetStatus() != 200 {
		t.Fatalf("summary = %d %s", r.GetStatus(), r.GetBody())
	}
	var got ResetSummaryResponse
	mustData(t, r.GetBody(), &got)
	if got.Accounts == nil {
		t.Fatalf("summary without accounts: %s", r.GetBody())
	}
	return got.Accounts
}

func TestResetSummary(t *testing.T) {
	e := newResetEnv(t)
	ctx := context.Background()
	e.p.storeResetCount(ctx, 29, 2)
	e.p.storeResetCount(ctx, 30, 0)

	got := e.summary(t, 29, 29, -1, 0, 30, 31)
	if len(got) != 2 || got["29"].AvailableCount != 2 || got["29"].UpdatedAt != testNow.Unix() || got["30"] != (ResetCount{UpdatedAt: testNow.Unix()}) {
		t.Fatalf("summary = %+v", got)
	}
	if got := e.summary(t); len(got) != 0 {
		t.Fatalf("empty summary = %+v", got)
	}

	// At most MaxAccountBatch distinct ids; duplicates do not count.
	ids := make([]int64, 0, manifest.MaxAccountBatch+1)
	for i := range manifest.MaxAccountBatch {
		ids = append(ids, int64(i+1))
	}
	if got := e.summary(t, append(ids, 1, 2, 3)...); len(got) != 2 {
		t.Fatalf("full batch = %+v", got)
	}
	r := e.h.Do("POST", "/reset-credits/summary", nil, map[string]any{"account_ids": append(ids, int64(manifest.MaxAccountBatch+1))})
	if eb := decodeError(t, r); r.GetStatus() != 400 || len(eb.Error.Details.Fields) != 1 || eb.Error.Details.Fields[0].Field != "account_ids" {
		t.Fatalf("over the batch = %d %s", r.GetStatus(), r.GetBody())
	}
	if r := e.h.Do("POST", "/reset-credits/summary", nil, "{"); r.GetStatus() != 400 {
		t.Fatalf("invalid json = %d %s", r.GetStatus(), r.GetBody())
	}

	// One summary is one batch read, however many accounts it lists.
	kv := &batchKV{KV: e.host.Host.KV()}
	e.host.kv = kv
	if got := e.summary(t, 29, 30, 31); len(got) != 2 || got["29"].AvailableCount != 2 {
		t.Fatalf("batch summary = %+v", got)
	}
	if kv.getManys.Load() != 1 || kv.gets.Load() != 0 {
		t.Fatalf("summary made %d batch and %d single reads, want 1 and 0", kv.getManys.Load(), kv.gets.Load())
	}

	// A value that does not decode leaves its account out; the others stay.
	e.host.kv = kv.KV
	if err := kv.Set(ctx, kvResetCount, resetCountKey(31), []byte("{not json"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := e.summary(t, 29, 31, 30); len(got) != 2 || got["29"].AvailableCount != 2 || got["30"].UpdatedAt != testNow.Unix() {
		t.Fatalf("summary with an undecodable value = %+v", got)
	}

	// An unreadable KV leaves the accounts out; the route still answers.
	e.host.kv = failingKV{}
	if got := e.summary(t, 29); len(got) != 0 {
		t.Fatalf("summary with a failing KV = %+v", got)
	}
	if n := len(e.client.requests()); n != 0 {
		t.Fatalf("summary sent %d upstream requests", n)
	}
}

func TestResetCacheFollowsListAndConsume(t *testing.T) {
	e := newResetEnv(t)
	consumeCode := "nothing_to_reset"
	e.backend = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"available_count":2,"credits":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":"`+consumeCode+`","windows_reset":1}`)
	}
	key := resetCountKey(resetDeviceID)
	list := func() {
		t.Helper()
		if r := e.h.Do("POST", "/reset-credits/list", nil, map[string]any{"account_id": resetDeviceID}); r.GetStatus() != 200 {
			t.Fatalf("list = %d %s", r.GetStatus(), r.GetBody())
		}
	}

	list()
	if got := e.summary(t, resetDeviceID)[key]; got.AvailableCount != 2 {
		t.Fatalf("after list: %+v", got)
	}
	// Nothing spent: the count stays.
	if r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}}); r.GetStatus() != 409 {
		t.Fatalf("consume = %d %s", r.GetStatus(), r.GetBody())
	}
	if _, ok := e.summary(t, resetDeviceID)[key]; !ok {
		t.Fatal("a refused consume dropped the count")
	}
	// A spent (or, for an unknown code, possibly spent) reset drops the
	// stale count.
	for code, want := range map[string]int32{outcomeReset: 200, outcomeAlreadyRedeemed: 200, "brand_new": 502} {
		list()
		consumeCode = code
		if r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}}); r.GetStatus() != want {
			t.Fatalf("consume %s = %d %s", code, r.GetStatus(), r.GetBody())
		}
		if got, ok := e.summary(t, resetDeviceID)[key]; ok {
			t.Fatalf("after %s: cached %+v, want none", code, got)
		}
	}
	// A failing KV never fails the consume.
	consumeCode = outcomeReset
	e.host.kv = failingKV{}
	if r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}}); r.GetStatus() != 200 {
		t.Fatalf("consume with a failing KV = %d %s", r.GetStatus(), r.GetBody())
	}
}
