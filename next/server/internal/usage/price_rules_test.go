package usage

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Exercise settlement and the persisted log detail together, without a database.
// These are administrator-defined request conditions, not provider tier presets.
func TestPriceOfConditionalMultipliersAndLogDetail(t *testing.T) {
	const expression = `(tier("base", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6)) * (param("speed") == "fast" ? 2 : 1) * (header("x-price-option") == "premium" ? 3 : 1)`
	for _, tc := range []struct {
		name, semantics string
		input           int64
	}{
		{"exclusive", expr.SemanticsExclusive, 1_000_000},
		{"inclusive", expr.SemanticsInclusive, 1_650_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pending{
				Expression: expression,
				Tokens: core.UsageTokens{Input: tc.input, Output: 200_000,
					CacheRead: 500_000, CacheCreation: 100_000, CacheCreation1h: 50_000},
				Rate:      decimal.RequireFromString("1.25"),
				CreatedAt: time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC),
				Inputs: pendingInputs{Semantics: tc.semantics,
					Params:  map[string]string{"speed": `"fast"`},
					Headers: map[string]string{"x-price-option": "premium"}},
			}
			total, detail, hash, err := priceOf(p)
			if err != nil {
				t.Fatal(err)
			}
			if hash == "" || !total.Equal(decimal.RequireFromString("51.1875")) {
				t.Fatalf("total=%s hash=%q", total, hash)
			}
			wantVars := expr.Vars{P: 1_000_000, C: 200_000, CR: 500_000, CC: 100_000, CC1h: 50_000, Len: 1_650_000}
			if detail.Breakdown.Vars != wantVars {
				t.Fatalf("normalized tokens=%+v, want %+v", detail.Breakdown.Vars, wantVars)
			}
			if len(detail.Rules) != 2 || !detail.Rules[0].Matched || !detail.Rules[1].Matched ||
				detail.Rules[0].Multiplier != 2 || detail.Rules[1].Multiplier != 3 {
				t.Fatalf("rules=%+v", detail.Rules)
			}
			raw, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			// Assert the public JSON shape consumed by usage-log details, including
			// the aggregate conditional multiplier separately from the group rate.
			for path, want := range map[string]string{
				"breakdown.base": "6.825", "breakdown.rules_multiplier": "6",
				"cost": "40.95", "rate_multiplier": "1.25", "total_cost": "51.1875",
				"rules.0.multiplier": "2", "rules.1.multiplier": "3",
				"rules.0.matched": "true", "rules.1.matched": "true",
				"inputs.params.speed": `"fast"`, "inputs.headers.x-price-option": "premium",
				"inputs.semantics": tc.semantics,
			} {
				if got := gjson.GetBytes(raw, path); !got.Exists() || got.String() != want {
					t.Errorf("billing_detail.%s=%s, want %s", path, got.Raw, want)
				}
			}
			var historical BillingDetail
			if err := json.Unmarshal(raw, &historical); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(historical.Inputs, p.Inputs) || !reflect.DeepEqual(historical.Rules, detail.Rules) {
				t.Fatalf("historical inputs/rules lost in JSON round trip: %+v", historical)
			}
			// A subsequent request with different conditions must not rewrite the
			// serialized historical charge; replay its saved inputs independently.
			p.Inputs.Params["speed"] = `"standard"`
			p.Inputs.Headers["x-price-option"] = "normal"
			plain, plainDetail, _, err := priceOf(p)
			if err != nil || !plain.Equal(decimal.RequireFromString("8.53125")) || !plainDetail.Breakdown.RulesMultiplier.Equal(decimal.NewFromInt(1)) {
				t.Fatalf("unmatched rules total=%s detail=%+v err=%v", plain, plainDetail, err)
			}
			p.Inputs = historical.Inputs
			replayed, replayedDetail, replayedHash, err := priceOf(p)
			if err != nil || !replayed.Equal(total) || replayedHash != hash || !replayedDetail.Breakdown.RulesMultiplier.Equal(decimal.NewFromInt(6)) {
				t.Fatalf("historical replay total=%s hash=%q err=%v", replayed, replayedHash, err)
			}
		})
	}
}
