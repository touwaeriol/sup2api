package core

import (
	"context"
	"encoding/json"
	"strconv"
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
