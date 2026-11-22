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
	for _, raw := range []string{`{"model": "a"}`, `[{"model": 1}]`} {
		_, fe = DecodeList[listItem](WithLocale(ctx, "zh"), json.RawMessage(raw), "model_rates", "model rates", "模型倍率")
		if len(fe) != 1 || fe[0] != (FieldError{Field: "model_rates", Code: "invalid", Message: "必须为模型倍率的列表"}) {
			t.Errorf("%s: got %v", raw, fe)
		}
	}
}

// An item's field is named by its position: a rate that is no rate, a
// string ParseRate refuses or a value that is no string, is RateError
// there, a missing one is required; neither fails the decoding.
func TestRateInputErrors(t *testing.T) {
	ctx := context.Background()
	path := ItemField("model_rates", 2, "rate")
	for _, raw := range []string{`"0.00001"`, `1.5`, `{}`} {
		var in RateInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil || !in.Set || !in.Invalid || in.Value != nil && raw != `"0.00001"` {
			t.Fatalf("%s: got %+v, %v", raw, in, err)
		}
		if fe := in.Errors(ctx, path); len(fe) != 1 || fe[0] != RateError(ctx, "model_rates.2.rate") {
			t.Errorf("%s: got %v", raw, fe)
		}
	}
	if fe := (RateInput{}).Errors(ctx, path); len(fe) != 1 || fe[0].Field != path || fe[0].Code != "required" {
		t.Errorf("a missing rate: %v", fe)
	}
	if fe := (RateInput{Set: true}).Errors(ctx, path); fe != nil {
		t.Errorf("null is no error: %v", fe)
	}
}

// A list whose item has a rate that is no string still decodes; the item
// alone is wrong.
func TestDecodeListBadRate(t *testing.T) {
	items, fe := DecodeList[listItem](context.Background(), json.RawMessage(`[{"model": "a", "rate": 1.5}, {"model": "b", "rate": "2"}]`), "model_rates", "model rates", "模型倍率")
	if fe != nil || len(items) != 2 || !items[0].Rate.Invalid || items[1].Rate.Invalid {
		t.Fatalf("got %+v, %v", items, fe)
	}
}
