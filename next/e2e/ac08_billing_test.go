package e2e

import (
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
)

// AC 8: per-request, per-token and a two-tier expression price (with one
// request-header surcharge rule): usage cost, matched tier and rule hits agree
// with the price preview; the ledger has matching entries; settling the same
// request twice never charges twice.
func TestAC08_BillingModes(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{Accounts: 1, Balance: "20"})
	acctKey := tn.Accounts[0].Key
	m := e.Mock()
	base := fmt.Sprintf("claude-e2e-%s-bill", e.RunID)

	const exprText = `(len <= 200000 ? tier("standard", p*3 + c*15) : tier("long_context", p*6 + c*22.5))` +
		` * (has(header("anthropic-beta"), "e2e-surcharge") ? 2 : 1)`

	cases := []struct {
		name    string
		price   map[string]any
		model   string
		headers map[string]string
		usage   *map[string]int // mock usage override
		tier    string
		rules   []bool // matched flags of the surcharge rules
		want    *big.Rat
	}{
		{
			name:  "per_request",
			model: base + "-flat",
			price: map[string]any{"mode": "per_request", "config": map[string]any{"price": "0.04"}}, // ASSUMED config shape
			tier:  "base",
			want:  big.NewRat(4, 100),
		},
		{
			name:  "per_token",
			model: base + "-token",
			price: map[string]any{"mode": "per_token", "config": RunPriceConfig()},
			tier:  "base",
			want:  ExpectedTokenCost(RunPrice, MockInputTokens, MockOutputTokens, MockCacheReadTokens, MockCacheCreationTokens, 0, 1),
		},
		{
			name:    "expression standard tier with surcharge",
			model:   base + "-expr",
			price:   map[string]any{"mode": "expression", "expression": exprText},
			headers: map[string]string{"anthropic-beta": "e2e-surcharge"},
			tier:    "standard",
			rules:   []bool{true},
			// The expression prices no cache category, so cache tokens are not
			// billed (ARCHITECTURE 7.3: p is the input without cache).
			want: new(big.Rat).Mul(ExpectedTokenCost(map[string]float64{"p": 3, "c": 15},
				MockInputTokens, MockOutputTokens, 0, 0, 0, 1), big.NewRat(2, 1)),
		},
		{
			name:  "expression long_context tier without surcharge",
			model: base + "-expr",
			usage: &map[string]int{"input_tokens": 250000, "output_tokens": 42},
			tier:  "long_context",
			rules: []bool{false},
			want:  ExpectedTokenCost(map[string]float64{"p": 6, "c": 22.5}, 250000, 42, 0, 0, 0, 1),
		},
	}

	prices := map[string]int64{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := e.With(t)
			if c.price != nil {
				// Prices are global per model: no platform (ARCHITECTURE 7.3).
				body := map[string]any{"model": c.model}
				for k, v := range c.price {
					body[k] = v
				}
				v := admin.OK(t, http.MethodPost, "/prices/validate", body)
				if !v.Get("ok").Bool() || v.Get("expression").String() == "" {
					t.Fatalf("validate: %s", v.Raw)
				}
				row := e.CreatePrice(admin, body)
				if row.Get("platform").Exists() {
					t.Fatalf("price still has a platform: %s", row.Raw)
				}
				prices[c.model] = row.Get("id").Int()
			}
			priceID := prices[c.model]

			if c.usage != nil {
				m.SetRule(t, MockRule{APIKey: acctKey, Usage: c.usage, Remaining: 1})
			}
			g := e.MustMessages(tn.APIKey, MessagesBody(c.model, "bill me", false), c.headers)
			u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
			if u.Get("billing_status").String() != "billed" {
				t.Fatalf("billing_status: %s", u.Raw)
			}
			AssertMoney(t, "usage.total_cost", u.Get("total_cost").String(), c.want)
			if u.Get("matched_tier").String() != c.tier {
				t.Fatalf("matched_tier %q, want %q", u.Get("matched_tier").String(), c.tier)
			}
			if u.Get("price_id").Int() != priceID || u.Get("expr_hash").String() == "" {
				t.Fatalf("price trace: %s", u.Raw)
			}

			// Preview with the recorded usage and headers gives the same result.
			usage := map[string]any{
				"p": u.Get("input_tokens").Int(), "c": u.Get("output_tokens").Int(),
				"cr": u.Get("cache_read_tokens").Int(), "cc": u.Get("cache_creation_tokens").Int() - u.Get("cache_creation_1h_tokens").Int(),
				"cc1h": u.Get("cache_creation_1h_tokens").Int(),
			}
			pv := admin.OK(t, http.MethodPost, "/prices/preview", map[string]any{
				"price_id": priceID, "usage": usage, "headers": c.headers, "params": map[string]any{}, "group_id": tn.GroupID,
			})
			AssertMoney(t, "preview.cost", pv.Get("cost").String(), c.want)
			if pv.Get("tier").String() != c.tier {
				t.Fatalf("preview tier %q, want %q", pv.Get("tier").String(), c.tier)
			}
			pr := pv.Get("rules").Array()
			if len(pr) != len(c.rules) {
				t.Fatalf("preview rules: %s", pv.Get("rules").Raw)
			}
			detailRules := u.Get("billing_detail.rules").Array()
			for i, want := range c.rules {
				if pr[i].Get("matched").Bool() != want {
					t.Fatalf("preview rule %d matched=%v, want %v", i, pr[i].Get("matched").Bool(), want)
				}
				if i < len(detailRules) && detailRules[i].Get("matched").Bool() != want {
					t.Fatalf("usage billing_detail rule %d matched=%v", i, detailRules[i].Get("matched").Bool())
				}
			}

			// Ledger: exactly one usage debit for this request, equal to the cost.
			var hits []string
			for _, l := range e.UsageDebits(admin, tn.User.UserID) {
				if l.Get("ref_id").String() == g.RequestID {
					hits = append(hits, l.Get("delta").String())
				}
			}
			if len(hits) != 1 {
				t.Fatalf("ledger entries for %s: %v", g.RequestID, hits)
			}
			AssertMoney(t, "ledger.delta", hits[0], new(big.Rat).Neg(c.want))
		})
	}

	// Price history keeps every expression by hash.
	for model, id := range prices {
		p := admin.OK(t, http.MethodGet, fmt.Sprintf("/prices/%d", id), nil)
		h := admin.OK(t, http.MethodGet, "/prices/history/"+p.Get("expr_hash").String(), nil)
		// History stores the canonical "v1:<body>" form; prices keep the text as entered.
		canon := func(s string) string { return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v1:")) }
		if h.Get("expr_hash").String() != p.Get("expr_hash").String() || canon(h.Get("expression").String()) != canon(p.Get("expression").String()) {
			t.Fatalf("history for %s: %s", model, h.Raw)
		}
	}

	// Balance = credit - sum(usage debits); a replayed request id is never
	// charged twice (usage_logs.request_id / idempotency key usage:<id>).
	t.Run("idempotent settlement", func(t *testing.T) {
		e := e.With(t)
		g := e.MustMessages(tn.APIKey, MessagesBody(base+"-token", "first", false), map[string]string{"X-Request-Id": "e2e-dup-" + e.RunID})
		e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		g2 := e.Messages(tn.APIKey, MessagesBody(base+"-token", "second", false), map[string]string{"X-Request-Id": "e2e-dup-" + e.RunID})
		if g2.Status == 200 && g2.RequestID == g.RequestID {
			// usage_logs.request_id and the ledger idempotency key derive from the
			// request id: a client-chosen id would make the second call unbillable.
			t.Errorf("gateway reused the client-supplied X-Request-Id %q; request ids must be server generated", g.RequestID)
		}
		n := 0
		for _, l := range e.UsageDebits(admin, tn.User.UserID) {
			if l.Get("ref_id").String() == g.RequestID {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("request %s settled %d times", g.RequestID, n)
		}

		sum := new(big.Rat)
		for _, l := range e.Ledger(admin, tn.User.UserID, "") {
			sum.Add(sum, Money(t, l.Get("delta").String()))
		}
		AssertMoney(t, "balance vs ledger", e.Balance(tn.User), sum)
		if e.DockerHost != "" {
			rows := e.SQL(fmt.Sprintf(`SELECT count(*) FROM balance_ledger WHERE user_id=%d GROUP BY idempotency_key HAVING count(*)>1`, tn.User.UserID))
			if len(rows) != 0 {
				t.Fatalf("duplicate idempotency keys: %v", rows)
			}
		}
	})
}
