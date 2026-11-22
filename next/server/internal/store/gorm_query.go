package store

import (
	"database/sql"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Query building helpers for the GORM path.

// Conds are WHERE conditions gathered one at a time and applied together.
// The zero value holds none.
type Conds []func(*gorm.DB) *gorm.DB

// And adds cond, whose "?" placeholders take args in order; the conditions
// are ANDed. An array argument is an Int64s, Strings or Times, as for Where.
func (c *Conds) And(cond string, args ...any) {
	*c = append(*c, func(db *gorm.DB) *gorm.DB { return db.Where(cond, args...) })
}

// Scope applies the conditions, for db.Scopes.
func (c Conds) Scope() func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		for _, cond := range c {
			db = cond(db)
		}
		return db
	}
}

// SetIf puts *v into set under column unless v is nil: set is the column map
// of a partial UPDATE, in which a nil field is one the request left out and
// the column keeps its value.
func SetIf[T any](set map[string]any, column string, v *T) {
	if v != nil {
		set[column] = *v
	}
}

// UpsertOn is the upsert of a row on its unique columns keys, for
// db.Clauses: the columns of set take the inserted values over the row
// there, if any, and updated_at moves to now().
func UpsertOn(keys []string, set ...string) clause.OnConflict {
	columns := make([]clause.Column, len(keys))
	for i, k := range keys {
		columns[i] = clause.Column{Name: k}
	}
	touch := clause.Assignment{Column: clause.Column{Name: "updated_at"}, Value: clause.Expr{SQL: "now()"}}
	return clause.OnConflict{Columns: columns, DoUpdates: append(clause.AssignmentColumns(set), touch)}
}

// The row lock strengths that gorm's clause package has no constant for
// (it has clause.LockingStrengthUpdate and clause.LockingStrengthShare).
const (
	LockingStrengthNoKeyUpdate = "NO KEY UPDATE"
	LockingStrengthKeyShare    = "KEY SHARE"
)

// RowLock locks the rows a query reads until the transaction ends, for
// db.Clauses: Lock or ForUpdate builds one, Of and SkipLocked refine it.
// Each returns a new value, so no package can change the lock another one
// takes (a shared clause.Locking variable could be rewritten for the whole
// process with one assignment).
type RowLock struct{ clause.Locking }

// Lock is a row lock of strength: clause.LockingStrengthUpdate or Share,
// LockingStrengthNoKeyUpdate or KeyShare.
func Lock(strength string) RowLock { return RowLock{clause.Locking{Strength: strength}} }

// ForUpdate is Lock(clause.LockingStrengthUpdate): FOR UPDATE.
func ForUpdate() RowLock { return Lock(clause.LockingStrengthUpdate) }

// Of limits l to the rows of one table of a join, by its name or alias:
// FOR UPDATE OF a.
func (l RowLock) Of(table string) RowLock {
	l.Table = clause.Table{Name: table}
	return l
}

// SkipLocked makes l skip the rows another transaction holds instead of
// waiting for them: FOR UPDATE SKIP LOCKED.
func (l RowLock) SkipLocked() RowLock {
	l.Options = clause.LockingOptionsSkipLocked
	return l
}

// Reading rows by position, for statements whose columns have no names of
// their own (expressions) or the same name twice, or whose rows are folded
// as they come rather than mapped onto a struct.

// RowScanner is a *sql.Row or *sql.Rows: a scan function written against it
// reads the one row of Row() and each row of Rows() (EachRow, CollectRows).
type RowScanner interface{ Scan(dest ...any) error }

// EachRow runs db, a query, and calls fn on each row it returns, in order.
// The first error of fn stops the read and is returned; the rows are closed
// either way.
func EachRow(db *gorm.DB, fn func(*sql.Rows) error) error {
	rows, err := db.Rows()
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CollectRows runs db, a query, and returns what scan reads of each row, in
// order: never nil, so that no rows is an empty list.
func CollectRows[T any](db *gorm.DB, scan func(*sql.Rows) (T, error)) ([]T, error) {
	out := []T{}
	err := EachRow(db, func(rows *sql.Rows) error {
		v, err := scan(rows)
		if err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateFrom is the FROM list of an UPDATE, for db.Clauses: UPDATE t SET
// ... FROM <sql> WHERE ..., which GORM's PostgreSQL dialect places but only
// builds from table names. sql, a constant, takes vars as Where does
// (array parameters are Int64s, Strings, Times); it typically turns arrays
// into rows (unnest), so that one statement writes each row its own values,
// joined to the table in Where.
func UpdateFrom(sql string, vars ...any) clause.Interface {
	return updateFrom{clause.Expr{SQL: sql, Vars: vars}}
}

type updateFrom struct{ clause.Expr }

func (updateFrom) Name() string                   { return "FROM" }
func (f updateFrom) MergeClause(c *clause.Clause) { c.Expression = f.Expr }

// OneRow is a query of one row and no columns, for a Select of expressions
// that read no table of their own (scalar subqueries, COALESCE((?), 0)).
func OneRow(db *gorm.DB) *gorm.DB { return db.Table("(SELECT) AS one") }
