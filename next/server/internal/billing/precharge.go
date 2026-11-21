package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

func (s *Service) PreConsumeTokens(ctx context.Context) (int64, error) {
	st, err := s.Settings(ctx)
	return st.PreConsumeTokens, err
}

func (s *Service) OutputReserveTokens(ctx context.Context) (int64, error) {
	st, err := s.Settings(ctx)
	return st.OutputReserveTokens, err
}

// ClientDisconnectPolicy implements the gateway's optional lookup of what a
// request pays when its client hangs up mid-answer (CONTRACTS §25.8).
func (s *Service) ClientDisconnectPolicy(ctx context.Context) (string, error) {
	st, err := s.Settings(ctx)
	return st.ClientDisconnectPolicy, err
}

func (s *Service) Precharge(ctx context.Context, r *core.UsageRecord) error {
	amount, err := quoteRecord(ctx, s, r)
	if err != nil {
		return err
	}
	if amount.IsZero() {
		return nil
	}
	if amount.IsNegative() {
		return errors.New("invalid negative precharge")
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	ch := core.LedgerChange{UserID: r.UserID, Amount: amount, Kind: KindUsage, RefType: "precharge",
		RefID: r.RequestID, IdempotencyKey: "precharge:" + r.RequestID, Note: "request precharge"}
	// ApplyTx validated and rounded the same change; doing it before the
	// transaction keeps the error and the SQL parameters identical.
	if err := validateChange(&ch); err != nil {
		return err
	}
	ch.Amount = ch.Amount.Round(8)
	release, err := s.acquirePrechargeSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	var lr *core.LedgerResult
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		if lr, err = s.prechargeTx(ctx, tx, ch, st.MinBalance); err != nil {
			if isLockTimeout(err) {
				return core.ErrUnavailable.WithMessage("billing is busy, retry shortly").WithCause(err)
			}
			return err
		}
		if lr == nil || s.events == nil {
			return nil
		}
		if err := s.events.Emit(ctx, tx, core.Event{Type: core.EventBalanceChanged, Payload: map[string]any{
			"user_id": ch.UserID, "ledger_id": lr.LedgerID, "delta": ch.Amount.Neg(), "balance_after": lr.BalanceAfter, "kind": ch.Kind,
		}}); err != nil {
			return fmt.Errorf("emit balance.changed: %w", err)
		}
		return nil
	})
	if err == nil && lr != nil {
		s.CacheBalance(ctx, r.UserID, lr.LedgerID, lr.BalanceAfter)
	}
	return err
}

// prechargeTx reserves the precharge and writes its ledger entry in one
// statement (prechargeSQL: the lock timeout, the balance row's lock, the
// reservation and its ledger row), followed by the caller's balance.changed
// event and COMMIT: BEGIN, prechargeSQL, the event and COMMIT are four round
// trips. The sufficiency check is expressed in SQL. The row lock is taken
// before anything else touches the user's money, the idempotency key is the
// ledger key, and a duplicate charges nothing and reports no error.
func (s *Service) prechargeTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange, min decimal.Decimal) (*core.LedgerResult, error) {
	g := store.G(ctx, tx)
	p, err := runPrecharge(g, ch, min)
	if err == nil && !p.locked {
		// No balance row to lock (the user's first precharge): the statement
		// wrote nothing and holds no row lock. Create the row and run it
		// again, in the order lock timeout, balance row, lock, reservation.
		// Unlike ApplyTx, Precharge
		// returns an unknown user's foreign key violation as it is.
		if err = ensurePrechargeBalance(g, ch.UserID).Error; err == nil {
			p, err = runPrecharge(g, ch, min)
		}
		if err == nil && !p.locked {
			err = sql.ErrNoRows // what the lock statement read when the row was gone
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case p.ledgerID != 0:
		return &core.LedgerResult{LedgerID: p.ledgerID, BalanceAfter: p.after}, nil
	case p.duplicate:
		// The same request id was precharged before: nothing is charged and
		// Precharge has always answered success here.
		return nil, nil
	case !p.sufficient && p.fresh:
		return nil, core.ErrInsufficientBalance
	}
	return resolvePrecharge(ctx, tx, ch, p.sufficient)
}

// resolvePrecharge answers a precharge that wrote nothing although its
// snapshot did not hold the key. Precharge used to lock the balance row
// first and read the key in a later statement, so it also saw the keys
// committed while it waited for the lock; prechargeSQL's snapshot predates
// the lock. A key of this user committed in between is a duplicate, which
// Precharge answered with success: a ledger row of a user is written under
// that user's lock, so this user's key committed before the lock was ours,
// and the separate statement read it. The rest is as before: a key of
// another user that took the insert is a conflict, and a key that cannot be
// found is an internal error.
//
// sufficient says which case this is. The balance sufficed: the insert lost
// the key to another transaction (ON CONFLICT), the case Precharge re-read
// the key for. It did not, and the balance row changed after the snapshot
// (prechargeRow.fresh false): a transaction holding the lock committed in
// between, maybe with this key.
func resolvePrecharge(ctx context.Context, tx *gorm.DB, ch core.LedgerChange, sufficient bool) (*core.LedgerResult, error) {
	dup, err := findLedger(ctx, tx, ch.IdempotencyKey)
	switch {
	case err != nil:
		return nil, err
	case !sufficient && dup == nil:
		return nil, core.ErrInsufficientBalance
	case !sufficient:
		return nil, nil
	case dup == nil:
		return nil, core.ErrInternal.WithMessage("idempotency key vanished during insert")
	case dup.UserID == ch.UserID:
		return nil, nil
	}
	return checkDuplicate(dup, ch)
}

// prechargeRow is what prechargeSQL reports.
type prechargeRow struct {
	ledgerID   int64           // the new ledger row, 0 when none was written
	after      decimal.Decimal // its balance_after
	duplicate  bool            // the key was in the statement's snapshot
	sufficient bool            // the locked balance allowed the precharge
	locked     bool            // the balance row was locked
	fresh      bool            // and it was the snapshot's version of the row
}

// runPrecharge runs prechargeSQL for a validated, rounded change on db.
func runPrecharge(db *gorm.DB, ch core.LedgerChange, min decimal.Decimal) (prechargeRow, error) {
	var p prechargeRow
	err := prechargeStmt.Raw(db, ch.UserID, ch.RefID, ch.Amount, ch.Kind, ch.RefType, ch.RefID, ch.IdempotencyKey, ch.Note, min).Row().
		Scan(&p.ledgerID, &p.after, &p.duplicate, &p.sufficient, &p.locked, &p.fresh)
	return p, err
}

// prechargeLockTimeoutSQL sets the precharge's lock timeout for the rest of
// the transaction, as SET LOCAL did: the request path must not queue behind
// a long settlement, so past prechargeLockTimeout a lock wait fails (55P03)
// and Precharge answers 503 instead of holding a pool connection. It is a
// condition of the first statement, evaluated on the balance row before the
// statement waits for it or for anything else, so that it costs no round
// trip of its own. The one wait it cannot bound is for a table lock: a
// statement takes those before it evaluates anything, and only DDL holds
// one that conflicts.
const prechargeLockTimeoutSQL = `set_config('lock_timeout', '` + prechargeLockTimeout + `', true) IS NOT NULL`

// ensurePrechargeBalance is ensureBalanceRow behind the lock timeout, for a
// precharge that found no balance row: prechargeSQL did not set the timeout
// then (it is evaluated on the row), and the insert may wait for another
// transaction's insert of the row.
func ensurePrechargeBalance(db *gorm.DB, userID int64) *gorm.DB {
	row := db.Session(&gorm.Session{NewDB: true}).Table("(SELECT ?::bigint AS user_id) AS u", userID).Select("user_id").Where(prechargeLockTimeoutSQL)
	return gormpg.InsertSelect(db, "user_balances", []string{"user_id"}, row, clause.OnConflict{DoNothing: true})
}

// ensureBalanceRow inserts the user's balance row unless it exists.
func ensureBalanceRow(db *gorm.DB, userID int64) *gorm.DB {
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&newBalance{UserID: userID})
}

// newBalance is a new user_balances row: the user alone, the balance and
// updated_at their defaults, and nothing read back.
type newBalance struct {
	UserID int64 `gorm:"column:user_id"`
}

func (newBalance) TableName() string { return model.UserBalance{}.TableName() }

// prechargeSQL is the whole precharge in one statement. locked takes the
// balance row's FOR UPDATE lock after setting the lock timeout (its
// condition, evaluated on the row before the row is locked); every write
// reads the locked row, and the final SELECT reads it too, so the lock is
// taken whatever else the statement does, as the separate lock statement
// took it, and before anything is written. The guards are the statements
// Precharge used to run in order:
//   - balance - amount must exceed min_balance (meetsMin), else nothing is
//     reserved and the caller reports insufficient balance;
//   - a ledger row with the idempotency key must not exist yet (it answered
//     success and charged nothing when it did) — then ledger yields no row and
//     the caller sees duplicate=true.
//
// The balance is the locked row's: FOR UPDATE returns the row's latest
// version, after waiting for the transaction that held it, which is what
// prechargeSQL read when it ran after the lock statement. The idempotency
// key is read in the statement's snapshot, which is taken before the lock
// and lacks what committed while the statement waited; so the statement
// also reports whether the locked row is the snapshot's version (fresh, by
// ctid). If it is, no transaction that held the lock committed in between,
// so no ledger row of this user did (each one updates the balance row), and
// the key reads as it read after the lock. If it is not, the insert still
// decides (ON CONFLICT sees every committed key) and resolvePrecharge
// answers the cases without an insert.
//
// When both pass, balance_ledger, request_precharges and the balance update
// happen with the values ApplyTx wrote: delta -round(amount, 8) (validateChange
// already rejected a different rounding), balance_after balance - that, kind,
// ref and note unchanged. The update reads the inserted row through RETURNING,
// which is how data-modifying CTEs communicate. Every parameter carries its
// type: a bare $n that Postgres sees in two contexts is an "inconsistent
// types deduced" error, and store.Numbered binds each use of $n as a parameter
// of its own. Parameters: 1 user, 2 request id, 3 amount, 4 kind,
// 5 ref_type, 6 ref_id, 7 idempotency key, 8 note, 9 min_balance. Columns:
// prechargeRow.
const prechargeSQL = `
WITH locked AS (
    SELECT b.ctid AS version, b.balance FROM user_balances b
    WHERE b.user_id = $1::bigint AND ` + prechargeLockTimeoutSQL + `
    FOR UPDATE
), ledger AS (
    INSERT INTO balance_ledger (user_id, delta, balance_after, kind, ref_type, ref_id,
        idempotency_key, operator_id, plugin_key, note)
    SELECT $1::bigint, -round($3::numeric, 8), l.balance - round($3::numeric, 8), $4::text, $5::text,
        $6::text, $7::varchar, NULL, NULL, $8::text
    FROM locked l
    WHERE l.balance - $3::numeric > $9::numeric
      AND NOT EXISTS (SELECT 1 FROM balance_ledger WHERE idempotency_key = $7::varchar)
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id, balance_after
), reserved AS (
    INSERT INTO request_precharges (request_id, user_id, amount)
    SELECT $2::varchar, $1::bigint, $3::numeric WHERE EXISTS (SELECT 1 FROM ledger)
    RETURNING request_id
), applied AS (
    UPDATE user_balances SET balance = ledger.balance_after, updated_at = now()
    FROM ledger
    WHERE user_balances.user_id = $1::bigint
    RETURNING ledger.id, ledger.balance_after
)
SELECT COALESCE((SELECT id FROM applied), 0),
       COALESCE((SELECT balance_after FROM applied), 0),
       EXISTS (SELECT 1 FROM balance_ledger WHERE idempotency_key = $7::varchar),
       COALESCE((SELECT balance - $3::numeric > $9::numeric FROM locked), false),
       EXISTS (SELECT 1 FROM locked),
       COALESCE((SELECT l.version = b.ctid FROM locked l, user_balances b WHERE b.user_id = $1::bigint), false)`

var prechargeStmt = store.NewNumbered(prechargeSQL)

// ReleasePrechargeTx deletes the request's reservation and refunds it, in one
// statement under the user's balance lock (releaseSQL) plus the event. nil
// means the request had no reservation.
func (s *Service) ReleasePrechargeTx(ctx context.Context, tx *gorm.DB, userID int64, requestID string) (*core.LedgerResult, error) {
	res, amount, err := releasePrecharge(ctx, tx, userID, requestID)
	if err != nil || res == nil || res.Duplicate || s.events == nil {
		return res, err
	}
	if err := s.events.Emit(ctx, tx, balanceChanged(userID, res, amount, KindRefund)); err != nil {
		return nil, fmt.Errorf("emit balance.changed: %w", err)
	}
	return res, nil
}

func (s *Service) ReleaseExpiredPrecharges(ctx context.Context) error {
	var entries []model.RequestPrecharge
	// The oldest expired reservations, 100 at a time. Pending usage is
	// retried by settlement and keeps its reservation.
	if err := store.G(ctx, s.db.Gorm).Table("request_precharges p").Select("p.request_id, p.user_id").
		Where(`p.expires_at < now() AND NOT EXISTS (SELECT 1 FROM usage_logs u
			WHERE u.request_id = p.request_id AND u.billing_status IN ('pending', 'failed'))`).
		Order("p.expires_at").Limit(100).Scan(&entries).Error; err != nil {
		return err
	}
	var failedCount int
	for _, e := range entries {
		var lr *core.LedgerResult
		err := s.db.Tx(ctx, func(tx *gorm.DB) error {
			var err error
			lr, err = s.ReleasePrechargeTx(ctx, tx, e.UserID, e.RequestID)
			return err
		})
		if err != nil {
			slog.WarnContext(ctx, "billing: release expired precharge failed", "request_id", e.RequestID, "user_id", e.UserID, "err", err)
			failedCount++
			continue
		}
		if lr != nil {
			s.CacheBalance(ctx, e.UserID, lr.LedgerID, lr.BalanceAfter)
		}
	}
	if failedCount > 0 {
		slog.ErrorContext(ctx, "billing: some expired precharges failed to release", "failed", failedCount, "total", len(entries))
	}
	return nil
}

// prechargeLockTimeout bounds how long one precharge waits for the user's
// balance lock (PostgreSQL lock_timeout syntax).
const prechargeLockTimeout = "3s"

// prechargeSlotWait bounds how long a precharge waits for a slot before
// answering 503.
const prechargeSlotWait = 3 * time.Second

// acquirePrechargeSlot admits at most half the pool's connections to
// precharges at once, so a queue of them waiting for one user's balance lock
// never starves account selection, plugin calls and everything else that
// shares the pool. The slot is held for the transaction.
func (s *Service) acquirePrechargeSlot(ctx context.Context) (func(), error) {
	s.slotsOnce.Do(func() {
		n := 4
		if half := s.db.MaxConns() / 2; half > n {
			n = half
		}
		s.prechargeSlots = make(chan struct{}, n)
	})
	timer := time.NewTimer(prechargeSlotWait)
	defer timer.Stop()
	select {
	case s.prechargeSlots <- struct{}{}:
		return func() { <-s.prechargeSlots }, nil
	case <-timer.C:
		return nil, core.ErrUnavailable.WithMessage("billing is busy, retry shortly")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
