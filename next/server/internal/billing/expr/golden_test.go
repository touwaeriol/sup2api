package expr

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/generate_golden.json holds configs and the expressions generated
// from them. The console generates the same text (web
// src/views/prices/priceExpr.ts, checked by priceExpr.spec.ts against this
// file): it shows what the server will store, byte for byte.
func TestGenerateGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/generate_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string          `json:"name"`
		Mode       string          `json:"mode"`
		Config     json.RawMessage `json:"config"`
		Expression string          `json:"expression"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Generate(c.Mode, c.Config)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Expression {
				t.Errorf("Generate:\n got %s\nwant %s", got, c.Expression)
			}
		})
	}
}
