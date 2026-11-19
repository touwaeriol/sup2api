package usage

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL of the usage statements assembled per call (see package
// gormtest): the filtered usage list, the summary over statRows and the
// batched usage_logs insert.

// sqlFilter is parseFilter over query, for an operator with account:read
// (self nil) or for the user self.
func sqlFilter(t *testing.T, query string, self *int64) store.Conds {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/usage?"+query, nil)
	f, err := parseFilter(c, self, trailViewFor(self, self == nil))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// sqlSink stands for the row a statement reads, which a snapshot does not see.
var sqlSink struct{}

var snapshotTime = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func TestSQLUsageList(t *testing.T) {
	f := sqlFilter(t, "model=claude&api_key_name=ci_%25&success=true&from=2026-11-01T00:00:00Z&to=2026-11-15T00:00:00Z", nil)
	t.Run("count", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var n int64
			return db.Table("usage_logs u").Scopes(f.Scope()).Count(&n)
		})
	})
	t.Run("page", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB { return recordQuery(db, f).Limit(20).Offset(40).Scan(&sqlSink) })
	})
	t.Run("keyset", func(t *testing.T) {
		self := int64(7)
		k := sqlFilter(t, "request_id=r1&from=2026-11-01T00:00:00Z", &self)
		k.And("(u.created_at, u.id) < (?, ?)", time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC), int64(99))
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB { return recordQuery(db, k).Limit(21).Scan(&sqlSink) })
	})
}

// A day summary partly read from the rollup: statRows repeats the filter in
// every part of its UNION, so the filter's values are bound once per part.
func TestSQLUsageSummaryRollup(t *testing.T) {
	f := sqlFilter(t, "group_id=3&from=2026-10-16T00:00:00Z", nil)
	key := clause.Expr{SQL: `to_char(u.created_at AT TIME ZONE ?::text, 'YYYY-MM-DD')`, Vars: []any{"Asia/Shanghai"}}
	sh := summaryShape{outKey: `u.k`, key: key, extra: `''`, lastAt: `NULL::timestamptz`, groupBy: `k`, order: `k`}
	span := &hourSpan{from: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), to: time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)}
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return summaryQuery(db, sh, f, span).Scan(&sqlSink)
	})
}

// The usage_logs insert of a batch. One record (row): each value goes to
// its column, the documents are one bytea parameter each, a native
// request's conversion is NULL, and the INSERT returns the new id. More
// (batch, two records, with the ids NextIDs reserved): one INSERT of every
// row, ids included, that returns nothing.
func TestSQLInsertUsageRow(t *testing.T) {
	account := int64(7)
	rec := &core.UsageRecord{RequestID: "req-1", UserID: 3, APIKeyID: 4, GroupID: 5, AccountID: &account, PluginKey: "anthropic",
		Platform: "anthropic", Protocol: "anthropic.messages", Endpoint: "/v1/messages", Model: "claude-sonnet-4-5",
		StatusCode: 200, Success: true, Attempts: 1, UsageSemantics: "exclusive", Billable: true,
		Tokens: core.UsageTokens{Input: 1200, Output: 800}, RateMultiplier: decimal.NewFromInt(1),
		Price: &core.PriceRule{ID: 9, Mode: "expression", ExprHash: "hash"}, CreatedAt: snapshotTime}
	second := *rec
	second.RequestID, second.AccountID, second.Price, second.Billable = "req-2", nil, nil, false
	t.Run("row", func(t *testing.T) {
		gormtest.AssertRun(t, func(db *gorm.DB) error {
			_, err := insertUsageRows(db, []*core.UsageRecord{rec})
			return err
		})
	})
	t.Run("batch", func(t *testing.T) {
		rows := []usageLogRow{newUsageLogRow(rec), newUsageLogRow(&second)}
		rows[0].ID, rows[1].ID = 101, 102
		gormtest.AssertRun(t, func(db *gorm.DB) error {
			return db.Clauses(skipLoggedRequest).CreateInBatches(&rows, store.InsertChunk).Error
		})
	})
}
