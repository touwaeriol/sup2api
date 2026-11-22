package core

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
)

// GroupWritePart is a part of a group write that another module owns
// (CONTRACTS §97): POST /groups and PATCH /groups/:id take its fields in the
// same body and write them in the same transaction as the group row.
type GroupWritePart interface {
	// PrepareGroupWrite reads and checks the fields it owns in body (the
	// request object by key); groupID is 0 on a create. It returns the write
	// to run once the group row is written (nil when body has none of its
	// fields), or the field errors naming each wrong item by its path
	// ("model_prices.2.rule"). It writes nothing.
	PrepareGroupWrite(ctx context.Context, groupID int64, body map[string]json.RawMessage) (GroupWrite, []FieldError, error)
}

// GroupWrite writes a prepared part of a group write in tx, after the group
// row (groupID is the new id on a create). AfterCommit, when set, runs once
// the transaction committed (cache drops, broadcasts).
type GroupWrite struct {
	Apply       func(ctx context.Context, tx *gorm.DB, groupID int64) error
	AfterCommit func(ctx context.Context, groupID int64)
}
