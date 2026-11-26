package billing

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL of the billing statements assembled from parts (see package
// gormtest): the ledger's writes of the request path (TestSQLLedger), the
// rule writes and the ledger list's filter.

// The ledger list counts and pages the rows its query parameters select.
func TestSQLLedgerCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/ledger?kind="+core.LedgerKindUsage+"&from=2026-11-15T08:00:00Z", nil)
	uid := int64(7)
	f, err := ledgerConds(c, &uid)
	if err != nil {
		t.Fatal(err)
	}
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var total int64
		return ledgerRows(db, f).Count(&total)
	})
}

// The model price changes of a group write: the expressions to the
// history, the changed rules locked in the order of their keys, the removed
// ones deleted (one removed and saved again: two changes of one rule), and
// the saved ones upserted on their group and model, the update moving
// updated_at by now().
func TestSQLWriteRules(t *testing.T) {
	customized := core.PriceQuery{GroupID: 3, Model: "gpt-5"}
	gone := core.PriceQuery{GroupID: 3, Model: "claude-haiku-4-5"}
	again := core.PriceQuery{GroupID: 3, Model: "claude-opus-4-5"}
	custom, err := checkRule(context.Background(), Rule{Mode: "visual",
		Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "request", "price": 0}]}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := []ruleChange{{customized, custom}, {gone, nil}, {again, nil}, {again, custom}}
	slices.SortStableFunc(changes, func(a, b ruleChange) int { return cmp.Compare(a.on.Model, b.on.Model) })
	w, err := collapseChanges(changes, 2)
	if err != nil {
		t.Fatal(err)
	}
	gormtest.AssertRun(t, func(db *gorm.DB) error { return writeRules(db.Statement.Context, db, w) })
}

// The model rate changes of a group write: the removed ones deleted, the
// others upserted in the order of their models.
func TestSQLWriteModelRates(t *testing.T) {
	changes := rateChanges{"gpt-5": dec("1.2"), "claude-haiku-4-5": nil, "claude-opus-4-5": dec("0")}
	gormtest.AssertRun(t, func(db *gorm.DB) error { return changes.write(db.Statement.Context, db, 3, 2) })
}

// The ledger statements of the request path: the CTEs bind each use of a
// numbered parameter on its own, with its cast, so the arguments repeat in
// the order the text uses them.
func TestSQLLedger(t *testing.T) {
	operator := int64(9)
	amount, min := decimal.RequireFromString("0.5"), decimal.RequireFromString("0")
	var row struct{ ID int64 }
	cases := map[string]func(*gorm.DB) *gorm.DB{
		"ensure_balance": func(db *gorm.DB) *gorm.DB { return ensureBalanceRow(db, 7) },
		"apply": func(db *gorm.DB) *gorm.DB {
			return applyStmt.Raw(db, int64(7), amount.Neg(), core.LedgerKindAdminAdjust, "manual", "ref-1", "adjust:1", &operator, "", "note").Scan(&row)
		},
		"release": func(db *gorm.DB) *gorm.DB {
			return releaseStmt.Raw(db, "req-1", int64(7), releaseKey("req-1"), "release request precharge").Scan(&row)
		},
		"precharge": func(db *gorm.DB) *gorm.DB {
			return prechargeStmt.Raw(db, int64(7), "req-1", amount, core.LedgerKindUsage, "precharge", "req-1", "precharge:req-1", "request precharge", min).Scan(&row)
		},
		// A precharge's balance row, behind its lock timeout.
		"precharge_ensure": func(db *gorm.DB) *gorm.DB { return ensurePrechargeBalance(db, 7) },
		"settle": func(db *gorm.DB) *gorm.DB {
			ch := core.LedgerChange{UserID: 7, Amount: amount, Kind: core.LedgerKindUsage, RefType: "usage", RefID: "req-1", IdempotencyKey: "usage:req-1", Note: "note"}
			return settleStmt.Raw(db, settleArgs(7, "req-1", &ch)...).Scan(&row)
		},
		// Without a charge the charge's arguments are inert: $5 is false.
		"settle_release_only": func(db *gorm.DB) *gorm.DB {
			return settleStmt.Raw(db, settleArgs(7, "req-1", nil)...).Scan(&row)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) { gormtest.Assert(t, build) })
	}
}
