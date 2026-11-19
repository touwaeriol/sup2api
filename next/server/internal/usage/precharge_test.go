package usage

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestPrechargeTransitionsToAsyncReservationAndRefund(t *testing.T) {
	f := reconcileFixture(t)
	ctx := context.Background()
	r := f.reserved("precharge-video", "upstream-video", core.UsageTokens{Input: 1000, Output: 100})
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	reservedBalance := f.balance()
	f.svc.process(ctx, []*core.UsageRecord{r})
	if !f.balance().Equal(reservedBalance) {
		t.Fatal("async registration double-charged precharge")
	}
	if f.scalar(`SELECT count(*) FROM request_precharges`) != "0" {
		t.Fatal("precharge not transferred")
	}
	f.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, Reason: "generation failed"}, nil
	}
	f.due()
	f.svc.ReconcileDue(ctx)
	if !f.balance().Equal(decimal.NewFromInt(10)) {
		t.Fatalf("failed task balance %s", f.balance())
	}
}

func TestPrechargeSettlesOnceAndRefundsFailures(t *testing.T) {
	for _, billable := range []bool{true, false} {
		f := newFixture(t)
		ctx := context.Background()
		est := f.record("precharged", true)
		est.Tokens = core.UsageTokens{Input: 200000}
		if err := f.bill.Precharge(ctx, est); err != nil {
			t.Fatal(err)
		}
		if f.balance().Equal(decimal.NewFromInt(10)) {
			t.Fatal("not debited before response")
		}
		actual := f.record("precharged", billable)
		f.svc.process(ctx, []*core.UsageRecord{actual})
		want := decimal.NewFromInt(10)
		if billable {
			want = want.Sub(f.cost("precharged"))
		}
		if !f.balance().Equal(want) {
			t.Fatalf("balance %s want %s", f.balance(), want)
		}
		f.svc.process(ctx, []*core.UsageRecord{actual})
		if !f.balance().Equal(want) {
			t.Fatal("duplicate usage charged twice")
		}
		if f.scalar(`SELECT count(*) FROM request_precharges`) != "0" {
			t.Fatal("reservation leaked")
		}
		if f.scalar(`SELECT count(*) FROM balance_ledger WHERE idempotency_key='precharge_release:precharged'`) != "1" {
			t.Fatal("refund missing or duplicated")
		}
	}
}

func TestPrechargeReleaseRollsBackWithSettlement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.record("rollback-precharge", true)
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	before := f.balance()
	f.ledger.fail.Store(1)
	f.svc.process(ctx, []*core.UsageRecord{r})
	if !f.balance().Equal(before) || f.scalar(`SELECT count(*) FROM request_precharges`) != "1" {
		t.Fatal("failed settlement leaked a refund")
	}
	if err := f.svc.settle(ctx, fromRecord(r), false); err != nil {
		t.Fatal(err)
	}
	if !f.balance().Equal(decimal.NewFromInt(10).Sub(f.cost(r.RequestID))) {
		t.Fatal("retry charged twice")
	}
}
