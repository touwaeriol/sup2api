package usage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// This uses the real-PG fixture; the second Service has no shared memory
// with the registering one.
func TestTaskCrossNodeRegistrationIdentityAndDebit(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	nodeB := New(f.db, f.ledger, f.ledger, nil, Options{})
	prepare := func(request, upstream string, account int64) core.TaskRegistration {
		t.Helper()
		rec := f.reserved(request, upstream, core.UsageTokens{Input: 1000, Output: 100})
		rec.AccountID = &account
		public, err := f.svc.BeginTask(ctx, core.TaskIntent{RequestID: rec.RequestID,
			PluginKey: rec.PluginKey, Kind: "video", Model: rec.Model, Protocol: rec.Protocol,
			UserID: rec.UserID, APIKeyID: rec.APIKeyID, GroupID: rec.GroupID, AccountID: account})
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"id":"` + upstream + `","status":"queued","extra":{"value":9007199254740993}}`)
		if err := f.svc.ReceiveTask(ctx, request, 200, body, ""); err != nil {
			t.Fatal(err)
		}
		return core.TaskRegistration{PublicID: public, UpstreamID: upstream, Kind: "video",
			Snapshot: body, IDPaths: []string{"id"}, NextCheckAfter: time.Minute, Deadline: time.Hour, Record: rec}
	}
	query := func(id string) core.TaskQuery {
		return core.TaskQuery{ID: id, PluginKey: "vid", Kind: "video", SubmitProtocol: "vid.gen",
			UserID: f.user, GroupID: f.group, IDPaths: []string{"id"}}
	}
	assertAbsent := func(q core.TaskQuery) {
		t.Helper()
		got, err := nodeB.FindTask(ctx, q)
		if err == nil || core.AsError(err).Code != core.ErrNotFound.Code {
			t.Fatalf("query must hide task existence: task=%+v err=%v", got, err)
		}
	}

	before := f.balance()
	first := prepare("managed-first", "same-upstream-id", 7)
	if err := commitTask(f.svc, first); err != nil {
		t.Fatal(err)
	}
	snap, err := nodeB.FindTask(ctx, query(first.PublicID))
	if err != nil || snap.PublicID != first.PublicID || snap.UpstreamID != first.UpstreamID ||
		snap.Model != first.Record.Model || string(snap.Body) != string(first.Snapshot) {
		t.Fatalf("second node cannot immediately observe committed task: %+v err=%v", snap, err)
	}
	wire, err := protocol.RewriteTaskID(snap.Body, snap.IDPaths, snap.UpstreamID, snap.PublicID)
	if err != nil || strings.Contains(string(wire), "same-upstream-id") ||
		!strings.Contains(string(wire), first.PublicID) || !strings.Contains(string(wire), "9007199254740993") {
		t.Fatalf("unsafe public snapshot: %s err=%v", wire, err)
	}
	if charged := before.Sub(f.balance()); charged.Sign() <= 0 || !charged.Equal(f.cost(first.Record.RequestID)) {
		t.Fatalf("registration did not atomically charge estimate: %s", charged)
	}
	for _, mutate := range []func(*core.TaskQuery){
		func(q *core.TaskQuery) { q.UserID = f.other },
		func(q *core.TaskQuery) { q.GroupID++ },
		func(q *core.TaskQuery) { q.PluginKey = "another-plugin" },
		func(q *core.TaskQuery) { q.Kind = "another-kind" },
		func(q *core.TaskQuery) { q.ID = first.UpstreamID },
	} {
		q := query(first.PublicID)
		mutate(&q)
		assertAbsent(q)
	}

	chargedBalance := f.balance()
	// Deterministic refusals are marked so that the commit is not retried.
	if err := commitTask(nodeB, first); !errors.Is(err, core.ErrExecutionRefused) {
		t.Fatalf("duplicate request registered twice or not refused: %v", err)
	}
	if !f.balance().Equal(chargedBalance) || f.scalar(`SELECT count(*) FROM async_tasks`) != "1" ||
		f.scalar(`SELECT count(*) FROM pending_settlements`) != "1" ||
		f.scalar(`SELECT count(*) FROM usage_logs`) != "1" {
		t.Fatal("duplicate registration changed billing or task identity")
	}
	if f.scalar(`SELECT account_id FROM async_tasks WHERE public_id=$1`, first.PublicID) != "7" ||
		f.scalar(`SELECT ref_id FROM pending_settlements`) != first.PublicID {
		t.Fatal("original account or public settlement identity was not persisted")
	}

	// A parser/result cannot substitute an account after dispatch was recorded.
	mismatch := prepare("managed-mismatch", "another-upstream-id", 7)
	wrongAccount := int64(8)
	mismatch.Record.AccountID = &wrongAccount
	if err := commitTask(f.svc, mismatch); !errors.Is(err, core.ErrExecutionRefused) {
		t.Fatalf("changed dispatch account accepted or not refused: %v", err)
	}
	assertAbsent(query(mismatch.PublicID))
	if !f.balance().Equal(chargedBalance) || f.scalar(`SELECT count(*) FROM usage_logs`) != "1" {
		t.Fatal("identity mismatch left a debit or usage row behind")
	}

	// Same account/ref cannot overwrite the first owner/record, and a rejected
	// registration rolls its provisional usage and balance changes back.
	collision := prepare("managed-collision", first.UpstreamID, 7)
	if err := commitTask(f.svc, collision); err == nil {
		t.Fatal("same-account upstream ID collision accepted")
	}
	assertAbsent(query(collision.PublicID))
	if !f.balance().Equal(chargedBalance) || f.scalar(`SELECT count(*) FROM usage_logs`) != "1" {
		t.Fatal("identity collision left a debit or usage row behind")
	}

	// The same raw ID on another upstream account is a separate task.
	otherAccount := prepare("managed-other-account", first.UpstreamID, 8)
	if err := commitTask(nodeB, otherAccount); err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.FindTask(ctx, query(otherAccount.PublicID))
	if err != nil || other.PublicID == first.PublicID || other.UpstreamID != first.UpstreamID ||
		f.scalar(`SELECT account_id FROM async_tasks WHERE public_id=$1`, other.PublicID) != "8" {
		t.Fatalf("account namespaces are not isolated: %+v err=%v", other, err)
	}
}
