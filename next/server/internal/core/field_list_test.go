package core

import (
	"context"
	"encoding/json"
	"testing"
)

type listItem struct {
	Model string    `json:"model"`
	Rate  RateInput `json:"rate"`
}

// A list decodes item by item; a value that is no such list is one field
// error on the list, in the caller's language.
func TestDecodeList(t *testing.T) {
	ctx := context.Background()
	items, fe := DecodeList[listItem](ctx, json.RawMessage(`[{"model": "a", "rate": "1.5"}, {"model": "b", "rate": null}, {"model": "c"}]`), "model_rates", "model rates", "模型倍率")
	if fe != nil || len(items) != 3 {
		t.Fatalf("got %v, %v", items, fe)
	}
	if a := items[0].Rate; !a.Set || a.Invalid || a.Value.String() != "1.5" {
		t.Errorf("a rate: %+v", a)
	}
	if b, c := items[1].Rate, items[2].Rate; !b.Set || b.Value != nil || c.Set || c.Value != nil {
		t.Errorf("null and missing rates: %+v %+v", b, c)
	}
	for _, raw := range []string{`{"model": "a"}`, `[{"rate": 1.5}]`} {
		_, fe = DecodeList[listItem](WithLocale(ctx, "zh"), json.RawMessage(raw), "model_rates", "model rates", "模型倍率")
		if len(fe) != 1 || fe[0] != (FieldError{Field: "model_rates", Code: "invalid", Message: "必须为模型倍率的列表"}) {
			t.Errorf("%s: got %v", raw, fe)
		}
	}
}

// An item's field is named by its position; a rate that is no rate is
// RateError there.
func TestRateInputErrors(t *testing.T) {
	ctx := context.Background()
	var in RateInput
	if err := json.Unmarshal([]byte(`"0.00001"`), &in); err != nil || !in.Invalid {
		t.Fatalf("got %+v, %v", in, err)
	}
	path := ItemField("model_rates", 2, "rate")
	if fe := in.Errors(ctx, path); len(fe) != 1 || fe[0] != RateError(ctx, "model_rates.2.rate") {
		t.Errorf("got %v", fe)
	}
	if fe := (RateInput{}).Errors(ctx, path); fe != nil {
		t.Errorf("a missing rate is no error here: %v", fe)
	}
}
