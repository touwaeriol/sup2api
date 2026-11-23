package core

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Request lists that are checked as a whole (the model prices and rates of
// POST/PATCH /groups and /segments, CONTRACTS §97): every item is decoded
// and checked, a wrong one is named by its path ("<list>.<i>.<field>"), and
// any wrong item fails the write.

// NewFieldError is the field error with the message in the caller's
// language.
func NewFieldError(ctx context.Context, field, code, en, zh string) FieldError {
	return FieldError{Field: field, Code: code, Message: Text(ctx, en, zh)}
}

// ItemField is the path of field of item i of list.
func ItemField(list string, i int, field string) string {
	return list + "." + strconv.Itoa(i) + "." + field
}

// DecodeList decodes raw, the value of the list field of a request body. A
// value that is not a list of T is the field error on the list, saying what
// the list holds: "must be a list of <en>" ("必须为<zh>的列表").
func DecodeList[T any](ctx context.Context, raw json.RawMessage, list, en, zh string) ([]T, []FieldError) {
	var items []T
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, []FieldError{NewFieldError(ctx, list, "invalid", "must be a list of "+en, "必须为"+zh+"的列表")}
	}
	return items, nil
}

// ModelErrors are the field errors of model, the trimmed model of an item
// that prices or rates it: none when it is a complete model id, as a global
// price's model is (manifest.ValidModelID). It need not have a price.
func ModelErrors(ctx context.Context, field, model string) []FieldError {
	switch {
	case model == "":
		return []FieldError{NewFieldError(ctx, field, "required", "model is required", "模型必填")}
	case !manifest.ValidModelID(model):
		return []FieldError{NewFieldError(ctx, field, "invalid", "a complete model id: letters, digits and . _ : / @ + - (no wildcards)",
			"完整的模型 ID：字母、数字和 . _ : / @ + -（不含通配符）")}
	}
	return nil
}
