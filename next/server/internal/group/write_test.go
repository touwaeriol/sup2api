package group

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// fakePart owns "rates": models to give the group a rate of 1 on. A blank
// model is a field error; a model twice fails the write (a duplicate key).
type fakePart struct{ committed []int64 }

func (p *fakePart) PrepareGroupWrite(_ context.Context, _ int64, body map[string]json.RawMessage) (core.GroupWrite, []core.FieldError, error) {
	raw, ok := body["rates"]
	if !ok {
		return core.GroupWrite{}, nil, nil
	}
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil || slices.Contains(models, "") {
		return core.GroupWrite{}, []core.FieldError{{Field: "rates.0", Code: "invalid"}}, nil
	}
	apply := func(ctx context.Context, tx *gorm.DB, id int64) error {
		for _, m := range models {
			if err := store.G(ctx, tx).Create(&model.GroupModelRate{GroupID: id, Model: m, Rate: decimal.NewFromInt(1)}).Error; err != nil {
				return err
			}
		}
		return nil
	}
	return core.GroupWrite{Apply: apply, AfterCommit: func(_ context.Context, id int64) { p.committed = append(p.committed, id) }}, nil, nil
}

// fieldsOf lists the fields of the field errors of a 400.
func fieldsOf(out map[string]any) []any {
	var fields []any
	for _, fe := range out["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any) {
		fields = append(fields, fe.(map[string]any)["field"])
	}
	return fields
}

// POST /groups writes the group and its parts in one transaction, checked
// as one.
func TestGroupCreateParts(t *testing.T) {
	e := setup(t)
	part := &fakePart{}
	e.svc.Parts = []core.GroupWritePart{part}
	rates := func(id int64) int64 { return e.one(`SELECT count(*) FROM group_model_rates WHERE group_id = $1`, id) }

	code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "a", "rates": []string{"x", "y"}})
	require.Equal(t, 201, code, "%v", out)
	id := int64(out["data"].(map[string]any)["id"].(float64))
	require.EqualValues(t, 2, rates(id), "the part writes with the new id")
	require.Equal(t, []int64{id}, part.committed)

	code, out = e.do(e.admin, "POST", "/groups", map[string]any{"name": " ", "rates": []string{""}})
	require.Equal(t, 400, code)
	require.Equal(t, []any{"name", "rates.0"}, fieldsOf(out), "the field errors of the group and the parts in one answer")

	code, _ = e.do(e.admin, "POST", "/groups", map[string]any{"name": "b", "rates": []string{"x", "x"}})
	require.Equal(t, 500, code)
	require.Zero(t, e.one(`SELECT count(*) FROM groups WHERE name = 'b'`), "a failing part leaves no group")
	code, _ = e.do(e.admin, "POST", "/groups", []any{"a"})
	require.Equal(t, 400, code, "a body that is no object")
}

// PATCH /groups/:id writes the group and its parts in one transaction.
func TestGroupUpdateParts(t *testing.T) {
	e := setup(t)
	part := &fakePart{}
	e.svc.Parts = []core.GroupWritePart{part}
	id := e.one(`INSERT INTO groups (name) VALUES ('a') RETURNING id`)
	rates := func() int64 { return e.one(`SELECT count(*) FROM group_model_rates WHERE group_id = $1`, id) }

	code, out := e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", id), map[string]any{"rates": []string{"z"}})
	require.Equal(t, 200, code, "%v", out)
	require.EqualValues(t, 1, rates())
	code, _ = e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", id), map[string]any{"description": "d", "rates": []string{"z"}})
	require.Equal(t, 500, code)
	require.Zero(t, e.one(`SELECT count(*) FROM groups WHERE description = 'd'`), "nor a changed one")
	require.Equal(t, []int64{id}, part.committed, "only committed writes run what comes after")

	code, _ = e.do(e.admin, "PATCH", "/groups/999999", map[string]any{"rates": []string{"z"}})
	require.Equal(t, 404, code)
}
