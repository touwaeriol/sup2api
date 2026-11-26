package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

func TestFindPage(t *testing.T) {
	db, _ := widgetDB(t)
	g := store.G(context.Background(), db.Gorm)
	for i := range 5 {
		require.NoError(t, g.Exec(`INSERT INTO widgets (name, tag) VALUES (?, ?)`, fmt.Sprintf("w%d", i), fmt.Sprint(i%2)).Error)
	}
	type row struct {
		Name  string
		Upper string
	}
	odd := func() store.Conds {
		var f store.Conds
		f.And("w.tag = ?", "1")
		return f
	}
	f := odd()
	q := g.Table("widgets w").Scopes(f.Scope()).Select("w.name, upper(w.name) AS upper").Order("w.id DESC")
	items, total, err := store.FindPage[row](q, 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "the count drops the select and the order")
	assert.Equal(t, []row{{"w3", "W3"}}, items)
	items, total, err = store.FindPage[row](q, 2, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "q is unchanged by a page read from it")
	assert.Equal(t, []row{{"w1", "W1"}}, items)

	none, total, err := store.FindPage[row](g.Table("widgets w").Where("false"), 3, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.NotNil(t, none, "no rows is the empty list, not nil")
}
