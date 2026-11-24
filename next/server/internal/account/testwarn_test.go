package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

// fakePriced prices models by group (0: the global price) and records the
// model it was asked about.
type fakePriced struct {
	priced map[int64]bool
	err    error
	asked  string
}

func (f *fakePriced) PricedModels(_ context.Context, groupIDs []int64, model string) (map[int64][]string, error) {
	f.asked = model
	if f.err != nil {
		return nil, f.err
	}
	out := map[int64][]string{}
	for _, id := range groupIDs {
		if f.priced[id] {
			out[id] = []string{model}
		}
	}
	return out, nil
}

// countingUpstream counts the test calls and answers them with testResponse,
// any key but a good one with 401.
func countingUpstream(t *testing.T, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.HasPrefix(r.Header.Get("x-api-key"), "sk-good-key") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(testResponse))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAccountTestPriceWarnings covers CONTRACTS §122.5: the upstream is
// tested whatever the price; a model without a price in a group of the
// account (the global price for an account in no group) only adds a warning,
// also kept as the message of the last test. The client model, before the
// account's mapping, is the one checked.
func TestAccountTestPriceWarnings(t *testing.T) {
	e := setup(t)
	var hits atomic.Int32
	e.plat.testURL = countingUpstream(t, &hits).URL + "/v1/messages"
	g1 := e.exec1(`INSERT INTO groups (name) VALUES ('default') RETURNING id`)
	g2 := e.exec1(`INSERT INTO groups (name) VALUES ('vip') RETURNING id`)
	grouped, lone := e.mkAccount("sk-good-key-123"), e.mkAccount("sk-good-key-456")
	e.patchAccount(grouped, map[string]any{"group_ids": []int64{g1, g2}, "model_mapping": map[string]string{"claude-x": "claude-y"}})

	for _, tc := range []struct {
		name   string
		id     int64
		priced map[int64]bool
		groups []string // nil: no warning
	}{
		{"priced in each group", grouped, map[int64]bool{g1: true, g2: true}, nil},
		{"one group without", grouped, map[int64]bool{g1: true}, []string{"vip"}},
		{"no price", grouped, nil, []string{"default", "vip"}},
		{"no group, global price", lone, map[int64]bool{0: true}, nil},
		{"no group, no price", lone, nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			priced := &fakePriced{priced: tc.priced}
			e.svc.d.Priced = priced
			before := hits.Load()
			d := e.testAccount(tc.id, "claude-x")
			if !d.Get("ok").Bool() || hits.Load() != before+1 || priced.asked != "claude-x" {
				t.Fatalf("upstream not tested as usual: hits %d->%d asked %q %s", before, hits.Load(), priced.asked, d)
			}
			e.checkPriceWarning(tc.id, d, tc.groups)
		})
	}

	// Without a model the plugin's choice is checked (the account has no
	// model list nor mapping then).
	priced := &fakePriced{}
	e.svc.d.Priced = priced
	e.plat.testModel = "claude-default"
	if d := e.testAccount(lone, ""); priced.asked != "claude-default" || len(d.Get("warnings").Array()) != 1 {
		t.Fatalf("plugin default model: asked %q %s", priced.asked, d)
	}
	e.plat.testModel = ""

	// A failed test, a failed price check and no dependency warn of nothing.
	priced.asked = ""
	if d := e.testAccount(e.mkAccount("sk-other-key-1"), "claude-x"); d.Get("ok").Bool() || d.Get("warnings").Exists() || priced.asked != "" {
		t.Fatalf("failed test: asked %q %s", priced.asked, d)
	}
	e.svc.d.Priced = &fakePriced{err: errors.New("snapshot down")}
	if d := e.testAccount(grouped, "claude-x"); !d.Get("ok").Bool() || d.Get("warnings").Exists() {
		t.Fatalf("price check error: %s", d)
	}
	e.svc.d.Priced = nil
	if d := e.testAccount(grouped, "claude-x"); d.Get("warnings").Exists() {
		t.Fatalf("warned without PricedModels: %s", d)
	}
}

// patchAccount updates account id, failing the test on an error.
func (e *env) patchAccount(id int64, body map[string]any) {
	e.t.Helper()
	if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), body); code != 200 {
		e.t.Fatalf("patch: %d %v", code, out)
	}
}

// testAccount tests account id with model and returns the result.
func (e *env) testAccount(id int64, model string) gjson.Result {
	e.t.Helper()
	_, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": model})
	return gjson.GetBytes(raw, "data")
}

// checkPriceWarning checks the price warning of test result d and the last
// test message: none when groups is nil, else one naming groups.
func (e *env) checkPriceWarning(id int64, d gjson.Result, groups []string) {
	e.t.Helper()
	var msg string
	if err := e.db.SQL.QueryRowContext(context.Background(), `SELECT last_test_message FROM accounts WHERE id = $1`, id).Scan(&msg); err != nil {
		e.t.Fatal(err)
	}
	ws := d.Get("warnings").Array()
	if groups == nil {
		if len(ws) != 0 || msg != "" {
			e.t.Fatalf("unexpected warning: %s / %q", d.Get("warnings"), msg)
		}
		return
	}
	if len(ws) != 1 {
		e.t.Fatalf("price warnings: %s", d.Get("warnings"))
	}
	w, got := ws[0], []string{}
	for _, g := range w.Get("details.groups").Array() {
		got = append(got, g.String())
	}
	if w.Get("code").String() != "model_price_not_configured" || w.Get("details.model").String() != "claude-x" ||
		!slices.Equal(got, groups) || w.Get("message").String() != msg || msg == "" {
		e.t.Fatalf("price warning: %s / %q", d.Get("warnings"), msg)
	}
}
