package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// The ledger's row lock on user_balances serialises every charge of one user,
// and the lock is held until COMMIT. Every client round trip made while it is
// held is paid by everyone else queueing for the same user at a distant
// PostgreSQL (CONTRACTS §63.7). The statements below therefore do a whole
// ledger change in one statement each, a settlement (release and charge)
// included, and skip the balance row's creation for a user who has one:
// ApplyTx and SettleTx are one statement, then the events, then the
// caller's COMMIT.
//
// They run through GORM (store.G) on the caller's transaction. The CTE
// statements keep placeholders that name each parameter once however often
// the CTEs use it ($1 or $user); store.NewNumbered rewrites them for GORM
// once, at start-up.
//
// Creating the balance row on demand. A data-modifying CTE's rows are
// invisible to the rest of its statement, so the row a statement locks must
// exist before it starts, and ensureBalanceSQL used to run first, always.
// For a user whose row the statement's snapshot holds, that INSERT ... ON
// CONFLICT DO NOTHING finds the committed row and does nothing: no row, no
// foreign key check, no row lock. So the statements report whether they
// found the row (the column "present": they locked it, or wanted no lock and
// saw it) and the caller runs ensureBalanceSQL and the statement again only
// when they did not. A statement that did not find the row wrote nothing and
// holds no row lock (every write reads the locked row), so that path is the
// old sequence, ensureBalanceSQL then the statement, after a read.

// ensureBalance creates the balance row a later statement locks, when the
// statement reported the row missing. An unknown user fails here with the
// users foreign key (store.IsForeignKeyViolation → not found).
func ensureBalance(db *gorm.DB, userID int64) error {
	err := ensureBalanceRow(db, userID).Error
	if store.IsForeignKeyViolation(err) {
		return core.ErrNotFound.WithMessage("user not found").WithCause(err)
	}
	return err
}

// The three ledger statements below (applySQL, releaseSQL, settleSQL) are
// assembled from the fragments that follow, so that a settlement's writes
// are a release's and a charge's by construction rather than by a copy.
// The fragments name their parameters ($user, $key ...), and each statement
// lists the names in the order of its arguments (store.NewNumbered).

// ledgerInsert starts the INSERT of a ledger row, ledgerInserted ends it: an
// idempotency key that exists by then (committed after the statement's
// snapshot) is no row, and the row reports its id and balance_after.
const (
	ledgerInsert = `INSERT INTO balance_ledger (user_id, delta, balance_after, kind, ref_type, ref_id,
        idempotency_key, operator_id, plugin_key, note)`
	ledgerInserted = `
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id, balance_after`
)

// reservationCTE is the request's reservation, as the statement's snapshot
// holds it.
const reservationCTE = `reservation AS (
    SELECT 1 FROM request_precharges WHERE request_id = $request::varchar AND user_id = $user::bigint
)`

// hasReservation is the condition under which a release locks the balance
// row.
const hasReservation = `EXISTS (SELECT 1 FROM reservation)`

// releaseCTEs release the request's reservation under the balance lock
// (locked, which holds the row whenever hasReservation does): it is deleted
// under that lock (released, USING locked), the lock order the ledger and
// the expiry sweeper share, and refunded with its amount unless the refund's
// key already exists (refund).
const releaseCTEs = `released AS (
    DELETE FROM request_precharges p USING locked
    WHERE p.request_id = $request::varchar AND p.user_id = $user::bigint
    RETURNING p.amount
), refund AS (
    ` + ledgerInsert + `
    SELECT $user::bigint, r.amount, l.balance + r.amount, 'refund', 'precharge', $request::varchar,
        $refund_key::varchar, NULL, NULL, $refund_note::text
    FROM released r, locked l
    WHERE NOT EXISTS (SELECT 1 FROM balance_ledger WHERE idempotency_key = $refund_key::varchar)` + ledgerInserted + `
)`

// releaseColumns are what a statement reports of the release (releaseRow):
// the refund's row as applied, whether a reservation was deleted and its
// amount.
const releaseColumns = `COALESCE((SELECT refund_id FROM applied), 0),
       COALESCE((SELECT refund_after FROM applied), 0),
       EXISTS (SELECT 1 FROM released),
       COALESCE((SELECT amount FROM released), 0)`

// The charge fragments take charged, the SQL condition under which the
// statement makes a charge: "" for one that always does (applySQL).

// ifCharged is cond, preceded by charged when there is one.
func ifCharged(charged, cond string) string {
	if charged == "" {
		return cond
	}
	return charged + ` AND ` + cond
}

// existingCTE is the row of the charge's idempotency key, as the
// statement's snapshot holds it, when charged.
func existingCTE(charged string) string {
	return `existing AS (
    SELECT id, balance_after, user_id, delta, kind FROM balance_ledger
    WHERE ` + ifCharged(charged, `idempotency_key = $key::varchar`) + `
)`
}

// chargeWanted is the condition under which a statement writes its charge
// and locks the balance row for it: it makes one, and the key is not taken.
func chargeWanted(charged string) string {
	return ifCharged(charged, `NOT EXISTS (SELECT 1 FROM existing)`)
}

// chargeCTE writes the charge's ledger row when chargeWanted: delta added to
// base, the balance it applies to, read from the rows of from (locked l
// among them).
func chargeCTE(charged, base, from string) string {
	return `charge AS (
    ` + ledgerInsert + `
    SELECT $user::bigint, $delta::numeric, ` + base + ` + $delta::numeric, $kind::text, $ref_type::text,
        $ref_id::text, $key::varchar, $operator::bigint, NULLIF($plugin::text, ''), $note::text
    FROM ` + from + `
    WHERE ` + chargeWanted(charged) + ledgerInserted + `
)`
}

// chargeColumns are what a statement reports of the charge (applyRow): the
// charge's row as applied, whether its key existed before the statement
// and that row.
const chargeColumns = `COALESCE((SELECT charge_id FROM applied), 0),
       COALESCE((SELECT charge_after FROM applied), 0),
       EXISTS (SELECT 1 FROM existing),
       COALESCE((SELECT id FROM existing), 0),
       COALESCE((SELECT balance_after FROM existing), 0),
       COALESCE((SELECT user_id FROM existing), 0),
       COALESCE((SELECT delta FROM existing), 0),
       COALESCE((SELECT kind FROM existing), '')`

// lockedCTE locks the user's balance row FOR UPDATE when lock (an SQL
// condition) holds. Every write reads the locked row, so a statement that
// did not lock it writes nothing.
func lockedCTE(lock string) string {
	return `locked AS (
    SELECT balance FROM user_balances
    WHERE user_id = $user::bigint AND (` + lock + `)
    FOR UPDATE
)`
}

// presentColumn is whether the statement found the balance row (see
// ensureBalanceSQL): it locked the row, or it wanted no lock (lock, the
// condition of its lockedCTE, is false) and the row is there.
func presentColumn(lock string) string {
	return `EXISTS (SELECT 1 FROM locked)
           OR (NOT (` + lock + `) AND EXISTS (SELECT 1 FROM user_balances WHERE user_id = $user::bigint))`
}

// applySQL is ApplyTx in one statement: find an existing ledger row for the
// idempotency key; otherwise lock the balance row, insert the ledger row with
// the new balance and update the balance. Arguments: applyArgs (applyNames).
// Columns: chargeColumns (applyRow) and present. Nothing applied and no
// existing row means another transaction inserted the key after this
// statement's snapshot (ON CONFLICT DO NOTHING); the caller re-reads it.
var applySQL = func() string {
	lock := chargeWanted("")
	return `
WITH ` + existingCTE("") + `, ` + lockedCTE(lock) + `, ` + chargeCTE("", "l.balance", "locked l") + `, applied AS (
    UPDATE user_balances SET balance = charge.balance_after, updated_at = now()
    FROM charge
    WHERE user_balances.user_id = $user::bigint
    RETURNING charge.id AS charge_id, charge.balance_after AS charge_after
)
SELECT ` + chargeColumns + `,
       ` + presentColumn(lock)
}()

// applyNames name applySQL's arguments: the user, the signed delta, kind,
// ref_type, ref_id, idempotency key, operator id (NULL), plugin key ("" →
// NULL) and note.
var applyNames = []string{"user", "delta", "kind", "ref_type", "ref_id", "key", "operator", "plugin", "note"}

var applyStmt = store.NewNumbered(applySQL, applyNames...)

// releaseSQL is ReleasePrechargeTx in one statement: the balance row is
// locked when the request has a reservation, which releaseCTEs delete and
// refund. Arguments: the request id, the user, the refund's idempotency key
// and note. Columns: releaseColumns (releaseRow). It reports no presence:
// prechargeSQL reserves only under the user's balance row, so without the
// row there is nothing to release.
var releaseSQL = `
WITH ` + reservationCTE + `, ` + lockedCTE(hasReservation) + `, ` + releaseCTEs + `, applied AS (
    UPDATE user_balances SET balance = refund.balance_after, updated_at = now()
    FROM refund
    WHERE user_balances.user_id = $user::bigint
    RETURNING refund.id AS refund_id, refund.balance_after AS refund_after
)
SELECT ` + releaseColumns

var releaseStmt = store.NewNumbered(releaseSQL, "request", "user", "refund_key", "refund_note")

// settleSQL is SettleTx in one statement: releaseSQL for the request's
// reservation, then applySQL for its charge (when $charge), as one. It
// replaces the two statements SettleTx ran under the balance lock, which
// cost a round trip each while the lock was held.
//
// Why it is the same as releaseSQL followed by applySQL:
//   - The writes. The release and the charge are those statements'
//     fragments (releaseCTEs, chargeCTE), and the lock condition is either
//     of theirs.
//   - The lock. The balance row is locked FOR UPDATE once, when either
//     statement locked it: a reservation is visible (releaseSQL's
//     condition) or a charge whose key is not (applySQL's). Every write reads
//     the locked row, so nothing is written before the lock, and the
//     reservation is deleted under it (USING locked), as before.
//   - The snapshot. Every CTE sees the statement's snapshot. releaseSQL's
//     reads are unchanged. applySQL's one read, the charge key (existing),
//     used to be taken after the release's lock and is now taken before it;
//     a key that commits in between was "existing" and is now a lost ON
//     CONFLICT race (charge empty, existing empty), which the caller already
//     answers with the same row and the same duplicate check
//     (applyRow.resolve), the way ApplyTx always has.
//   - The amounts. The charge's balance_after is the refund's balance_after
//     plus the delta, or the locked balance plus the delta when no refund
//     was written: the balance applySQL read after releaseSQL's update.
//     numeric addition is exact, so the values are those two statements
//     wrote.
//   - The order. The charge reads the refund's RETURNING row (LEFT JOIN
//     refund), so the refund is inserted (and takes its id) before the
//     charge row is formed: ids still ascend refund, charge.
//   - The balance. A row cannot be updated twice in one statement, so the
//     two UPDATEs are one (applied), to the charge's balance_after when it
//     was written, else to the refund's: the value the second of them left.
//     updated_at = now() is the transaction's time either way. applied
//     returns both ledger rows, so a row is reported only with its balance
//     update, as each statement's applied did.
//
// Arguments: settleArgs (settleNames). Columns: releaseColumns,
// chargeColumns and present.
var settleSQL = func() string {
	charged := `$charge::boolean`
	lock := hasReservation + ` OR (` + chargeWanted(charged) + `)`
	return `
WITH ` + existingCTE(charged) + `, ` + reservationCTE + `, ` + lockedCTE(lock) + `, ` + releaseCTEs + `, ` +
		chargeCTE(charged, "COALESCE(r.balance_after, l.balance)", "locked l LEFT JOIN refund r ON true") + `, applied AS (
    UPDATE user_balances SET balance = COALESCE(c.balance_after, r.balance_after), updated_at = now()
    FROM (SELECT 1) one LEFT JOIN refund r ON true LEFT JOIN charge c ON true
    WHERE user_balances.user_id = $user::bigint AND (r.id IS NOT NULL OR c.id IS NOT NULL)
    RETURNING r.id AS refund_id, r.balance_after AS refund_after, c.id AS charge_id, c.balance_after AS charge_after
)
SELECT ` + releaseColumns + `,
       ` + chargeColumns + `,
       ` + presentColumn(lock)
}()

// settleNames name settleSQL's arguments: the user, the request id, the
// refund's idempotency key and note, whether there is a charge, then the
// charge's as applyNames has them, without the user.
var settleNames = append([]string{"user", "request", "refund_key", "refund_note", "charge"}, applyNames[1:]...)

var settleStmt = store.NewNumbered(settleSQL, settleNames...)

// runLedger runs a ledger statement whose last column is present on db
// with args, scanning the others into dest, and when the statement did not
// find the user's balance row, creates it (ensureBalanceSQL) and runs the
// statement again.
func runLedger(db *gorm.DB, st store.Numbered, userID int64, args []any, dest ...any) error {
	var present bool
	dest = append(dest, &present)
	run := func() error { return st.Raw(db, args...).Row().Scan(dest...) }
	if err := run(); err != nil || present {
		return err
	}
	if err := ensureBalance(db, userID); err != nil {
		return err
	}
	return run()
}

func releaseKey(requestID string) string { return "precharge_release:" + requestID }

// applyRow is what applySQL reports of a change, and settleSQL of its
// charge (chargeColumns).
type applyRow struct {
	applied core.LedgerResult // the new ledger row; LedgerID 0 when none was written
	existed bool              // the key's row was there before the statement
	dup     core.LedgerResult // that row, when existed
}

// dest is the row's scan destinations, in the statements' column order.
func (a *applyRow) dest() []any {
	return []any{&a.applied.LedgerID, &a.applied.BalanceAfter, &a.existed,
		&a.dup.LedgerID, &a.dup.BalanceAfter, &a.dup.UserID, &a.dup.Delta, &a.dup.Kind}
}

// resolve turns the row into ApplyTx's result for ch: the new row, or the
// existing one after the duplicate check, re-reading the key after a lost
// insert race.
func (a *applyRow) resolve(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	switch {
	case a.applied.LedgerID != 0:
		return &a.applied, nil
	case a.existed:
		a.dup.Duplicate = true
		return checkDuplicate(&a.dup, ch)
	}
	// Neither a new nor an existing row: another transaction inserted the
	// key after this statement's snapshot (ON CONFLICT DO NOTHING).
	dup, err := findLedger(ctx, tx, ch.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if dup == nil {
		return nil, core.ErrInternal.WithMessage("idempotency key vanished during insert")
	}
	return checkDuplicate(dup, ch)
}

// applyArgs are applySQL's arguments (applyNames) for a validated, rounded
// change.
func applyArgs(ch core.LedgerChange) []any {
	delta := ch.Amount
	if !ch.Credit {
		delta = delta.Neg()
	}
	return []any{ch.UserID, delta, ch.Kind, ch.RefType, ch.RefID, ch.IdempotencyKey, ch.OperatorID, ch.PluginKey, ch.Note}
}

// applyChange runs applySQL for a validated, rounded change on db, creating
// the balance row when the statement reports it missing.
func applyChange(db *gorm.DB, ch core.LedgerChange) (applyRow, error) {
	var a applyRow
	err := runLedger(db, applyStmt, ch.UserID, applyArgs(ch), a.dest()...)
	return a, err
}

// checkDuplicate is the idempotency contract: the same key must carry the
// same user, amount and kind.
func checkDuplicate(dup *core.LedgerResult, ch core.LedgerChange) (*core.LedgerResult, error) {
	delta := ch.Amount
	if !ch.Credit {
		delta = delta.Neg()
	}
	if dup.UserID != ch.UserID || !dup.Delta.Equal(delta) || dup.Kind != ch.Kind {
		return nil, core.ErrConflict.WithMessage("idempotency key reused with different user, amount or kind")
	}
	return dup, nil
}

// releaseRow is what releaseSQL reports of a request's reservation, and
// settleSQL of its release (releaseColumns).
type releaseRow struct {
	refund   core.LedgerResult // the refund's ledger row; LedgerID 0 when none was written
	released bool              // a reservation was deleted
	amount   decimal.Decimal   // its amount
}

// dest is the row's scan destinations, in the statements' column order.
func (r *releaseRow) dest() []any {
	return []any{&r.refund.LedgerID, &r.refund.BalanceAfter, &r.released, &r.amount}
}

// resolve returns the refund; nil means the request had no reservation. A
// reservation that was deleted while its refund key already existed
// (nothing new written) is reported through the existing ledger row, as
// ApplyTx's duplicate.
func (r *releaseRow) resolve(ctx context.Context, tx *gorm.DB, key string) (*core.LedgerResult, error) {
	if !r.released {
		return nil, nil
	}
	if r.refund.LedgerID != 0 {
		return &r.refund, nil
	}
	dup, err := findLedger(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if dup == nil {
		return nil, core.ErrInternal.WithMessage("precharge released without a refund row")
	}
	return dup, nil
}

// releasePrecharge runs releaseSQL for the request's reservation on tx and
// returns the refund and its amount; nil means the request had no
// reservation.
func releasePrecharge(ctx context.Context, tx *gorm.DB, userID int64, requestID string) (*core.LedgerResult, decimal.Decimal, error) {
	key := releaseKey(requestID)
	var r releaseRow
	if err := releaseStmt.Raw(store.G(ctx, tx), requestID, userID, key, "release request precharge").Row().Scan(r.dest()...); err != nil {
		return nil, r.amount, err
	}
	res, err := r.resolve(ctx, tx, key)
	return res, r.amount, err
}

func balanceChanged(userID int64, res *core.LedgerResult, delta decimal.Decimal, kind string) core.Event {
	return core.Event{Type: core.EventBalanceChanged, Payload: map[string]any{
		"user_id": userID, "ledger_id": res.LedgerID, "delta": delta, "balance_after": res.BalanceAfter, "kind": kind,
	}}
}

// settleRow is what settleSQL reports.
type settleRow struct {
	release releaseRow
	charge  applyRow
}

// settleArgs are settleSQL's arguments (settleNames): the release's, then
// the charge's as applySQL takes them (without the user, already first), or
// inert values when there is no charge.
func settleArgs(userID int64, requestID string, charge *core.LedgerChange) []any {
	args := []any{userID, requestID, releaseKey(requestID), "release request precharge", charge != nil}
	if charge == nil {
		return append(args, decimal.Zero, "", "", "", "", (*int64)(nil), "", "")
	}
	return append(args, applyArgs(*charge)[1:]...)
}

// settleLedger runs settleSQL for the request's release and the validated,
// rounded charge (nil: release only) on db, creating the balance row when
// the statement reports it missing.
func settleLedger(db *gorm.DB, userID int64, requestID string, charge *core.LedgerChange) (settleRow, error) {
	var st settleRow
	err := runLedger(db, settleStmt, userID, settleArgs(userID, requestID, charge), append(st.release.dest(), st.charge.dest()...)...)
	return st, err
}

// SettleTx releases the request's precharge (if any) and applies the final
// charge (if any) inside the caller's transaction, in one statement under
// the user's balance lock (settleSQL), then emits the balance.changed events
// in one call. It returns the charge's result, or the refund's when there is
// no charge (nil when nothing moved). The caller refreshes the balance cache
// after committing, as for ApplyTx.
func (s *Service) SettleTx(ctx context.Context, tx *gorm.DB, userID int64, requestID string, charge *core.LedgerChange) (*core.LedgerResult, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	if charge != nil {
		if err := validateChange(charge); err != nil {
			return nil, err
		}
		charge.Amount = charge.Amount.Round(8)
		if charge.UserID != userID {
			return nil, errors.New("settlement charge belongs to another user")
		}
	}
	st, err := settleLedger(store.G(ctx, tx), userID, requestID, charge)
	if err != nil {
		return nil, err
	}
	refunded, err := st.release.resolve(ctx, tx, releaseKey(requestID))
	if err != nil {
		return nil, err
	}
	var charged *core.LedgerResult
	if charge != nil {
		if charged, err = st.charge.resolve(ctx, tx, *charge); err != nil {
			return nil, err
		}
	}
	if s.events != nil {
		var evs []core.Event
		if refunded != nil && !refunded.Duplicate {
			evs = append(evs, balanceChanged(userID, refunded, st.release.amount, core.LedgerKindRefund))
		}
		if charged != nil && !charged.Duplicate {
			delta := charge.Amount
			if !charge.Credit {
				delta = delta.Neg()
			}
			evs = append(evs, balanceChanged(userID, charged, delta, charge.Kind))
		}
		if len(evs) > 0 {
			if err := s.events.Emit(ctx, tx, evs...); err != nil {
				return nil, fmt.Errorf("emit balance.changed: %w", err)
			}
		}
	}
	if charge != nil {
		return charged, nil
	}
	return refunded, nil
}

// isLockTimeout reports PostgreSQL's lock_not_available (lock_timeout hit).
func isLockTimeout(err error) bool { return store.SQLState(err) == "55P03" }
