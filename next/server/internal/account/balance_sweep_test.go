package account

import (
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// core.account.balance_sweep reads, at the type's interval, the balances
// due: schedulable accounts whose last read (successful or not) is older
// than it, and accounts never read, disabled ones included. A disabled
// account read before and a deleted one are left alone.
func TestBalanceSweepReadsDueAccounts(t *testing.T) {
	e := setup(t)
	tasks := newFakeTasks()
	if err := e.svc.RegisterTasks(tasks); err != nil {
		t.Fatal(err)
	}
	if spec := tasks.spec(t, BalanceSweepTask); spec.Scope != core.TaskCluster || spec.Every != time.Minute {
		t.Fatalf("spec: %+v", spec)
	}
	up := newBalanceUpstream(t)
	e.withBalance(&fakeBalance{urls: []string{up.srv.URL + "/balance"}}, &manifest.AccountBalance{UpdateInterval: 600})
	ctx := t.Context()
	mk := func(name string, enabled bool, sql string) int64 {
		id := e.mkBalanceAccount(name, up.srv.URL, enabled)
		if sql != "" {
			if _, err := e.db.SQL.ExecContext(ctx, sql, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	snap := func(updated, failed string) string {
		return `INSERT INTO account_balances (account_id, amount_micros, currency, updated_at, error, error_at)
			VALUES ($1, 1, 'USD', now() - interval '` + updated + `', '` + failed + `', ` + map[bool]string{true: "now() - interval '" + failed + "'", false: "NULL"}[failed != ""] + `)`
	}
	due := []int64{
		mk("new", true, ""),
		mk("stale", true, snap("11 minutes", "")),
		mk("disabled-new", false, ""),
	}
	left := []int64{
		mk("fresh", true, snap("1 minute", "")),
		mk("failed-lately", true, snap("20 minutes", "1 minute")),
		mk("disabled-old", false, snap("1 hour", "")),
		mk("deleted", true, `UPDATE accounts SET deleted_at = now() WHERE id = $1`),
	}
	before := time.Now()
	res, err := tasks.run(t, ctx, BalanceSweepTask)
	if err != nil || res.Count != len(due) {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	for _, id := range due {
		if s, _ := e.db.AccountBalance(ctx, id); s == nil || s.UpdatedAt == nil || s.UpdatedAt.Before(before) {
			t.Fatalf("due account %d not read: %+v", id, s)
		}
	}
	for _, id := range left {
		if s, _ := e.db.AccountBalance(ctx, id); s != nil && s.UpdatedAt != nil && s.UpdatedAt.After(before) {
			t.Fatalf("account %d read: %+v", id, s)
		}
	}
	// Nothing is due right after; a manual refresh does not wait.
	if res, err = tasks.run(t, ctx, BalanceSweepTask); err != nil || res.Count != 0 {
		t.Fatalf("second sweep: %+v %v", res, err)
	}
	n := len(up.requests())
	if _, d := e.getBalance(left[0], true); d["error"] != nil || len(up.requests()) != n+1 {
		t.Fatalf("manual refresh: %v", d)
	}
}
