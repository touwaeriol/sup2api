package rollout

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestHistoryTransitionsAreTransactionalAndRetained(t *testing.T) {
	db := testutil.DB(t)
	c := newTestController(t, &stubRuntime{}, nil, t.TempDir())
	c.o.DB = db
	ctx := audit.WithSource(context.Background(), "builtin")
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins(key,name,status) VALUES('history','{}','enabled')`); err != nil {
		t.Fatal(err)
	}
	from, to := "1.0.0", "2.0.0"
	var id int64
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		id, err = c.insertRollout(ctx, tx, "history", "upgrade", &from, &to, PhasePreparing, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE plugin_rollouts SET coordinator_lease_until=clock_timestamp()+interval '60 seconds' WHERE id=$1`,
		`UPDATE plugin_rollouts SET coordinator_lease_until=clock_timestamp() WHERE id=$1`,
		`UPDATE plugin_rollouts SET coordinator_node_id='other',coordinator_boot_id='other-boot' WHERE id=$1`,
		`UPDATE plugin_rollouts SET phase='activating' WHERE id=$1`,
		`UPDATE plugin_rollouts SET phase='active' WHERE id=$1`,
	} {
		if _, err := db.SQL.ExecContext(ctx, sql, id); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := testutil.Begin(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SQLTx(tx).ExecContext(ctx, `UPDATE plugin_rollouts SET phase='failed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	var states []string
	if err := db.SQL.QueryRowContext(ctx, `SELECT array_agg(state ORDER BY id) FROM plugin_history WHERE rollout_id=$1`, id).Scan(&states); err != nil {
		t.Fatal(err)
	}
	want := []string{"created", "handoff", "takeover", "activating", "active"}
	var source string
	if err := db.SQL.QueryRowContext(ctx, `SELECT message::jsonb->>'source' FROM plugin_history WHERE rollout_id=$1 AND state='created'`, id).Scan(&source); err != nil || source != "builtin" {
		t.Fatal(source, err)
	}
	if len(states) != len(want) {
		t.Fatalf("states=%v", states)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("states=%v", states)
		}
	}
	var n int
	if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action='plugin.upgrade' AND detail->>'source'='builtin'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM plugins WHERE key='history'`); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugin_history WHERE rollout_id=$1`, id).Scan(&n); err != nil || n != len(want) {
		t.Fatal(n, err)
	}
}

func TestNodeHistoryDeduplicatesAndRetriesFailedWrites(t *testing.T) {
	c := newTestController(t, &stubRuntime{}, nil, t.TempDir())
	c.o.DB = testutil.DB(t)
	ctx := context.Background()
	s := core.PluginNodeState{State: "failed", Serving: "1.0.0", Standby: "2.0.0", Fallback: "1.0.0", RolloutID: 42, Error: "first failure"}
	c.recordNodeHistory(ctx, "history", s)
	s.Error = "different text"
	c.recordNodeHistory(ctx, "history", s)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	s.State = "active"
	s.Serving = "2.0.0"
	s.Fallback = ""
	c.recordNodeHistory(cancelled, "history", s)
	c.recordNodeHistory(ctx, "history", s)
	var n int
	if err := c.o.DB.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugin_history WHERE plugin_key='history' AND node_id <> ''`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if _, err := c.o.DB.SQL.ExecContext(ctx, `UPDATE plugin_history SET created_at=now()-interval '31 days' WHERE state='failed'`); err != nil {
		t.Fatal(err)
	}
	c.cleanHistory(ctx)
	if err := c.o.DB.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugin_history WHERE plugin_key='history'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
