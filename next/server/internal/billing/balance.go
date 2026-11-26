package billing

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// LedgerEntry is the API view of a balance_ledger row.
type LedgerEntry struct {
	ID             int64           `json:"id"`
	UserID         int64           `json:"user_id"`
	UserEmail      string          `json:"user_email,omitempty"`
	UserName       string          `json:"user_name,omitempty"`
	Delta          decimal.Decimal `json:"delta"`
	BalanceAfter   decimal.Decimal `json:"balance_after"`
	Kind           string          `json:"kind"`
	RefType        string          `json:"ref_type"`
	RefID          string          `json:"ref_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	OperatorID     *int64          `json:"operator_id"`
	PluginKey      *string         `json:"plugin_key"`
	Note           string          `json:"note"`
	CreatedAt      time.Time       `json:"created_at"`
}

// LedgerEntrySelf is the simplified DTO for self-service ledger queries.
type LedgerEntrySelf struct {
	ID           int64           `json:"id"`
	Delta        decimal.Decimal `json:"delta"`
	BalanceAfter decimal.Decimal `json:"balance_after"`
	Kind         string          `json:"kind"`
	Note         string          `json:"note"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (s *Service) registerBalanceRoutes(r *httpapi.Router) {
	r.Perm("GET", "/me/balance", "balance:self:read", s.myBalance)
	r.Perm("GET", "/me/ledger", "balance:self:read", s.myLedger)
	r.Perm("GET", "/ledger", "balance:all:read", s.allLedger)
	r.Perm("POST", "/users/:id/balance/adjust", "balance:adjust", s.adjustBalance)
}

func (s *Service) myBalance(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var bal decimal.Decimal
	err := store.G(ctx, s.db.Gorm).Table("user_balances").Select("balance").Where("user_id = ?", uid).Scan(&bal).Error
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"balance": bal})
}

func (s *Service) myLedger(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	page, size := httpapi.Pagination(c)
	f, err := ledgerConds(c, &uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	g := store.G(ctx, s.db.Gorm)
	rows := ledgerRows(g, f).Select("l.id, l.delta, l.balance_after, l.kind, l.note, l.created_at").Order("l.id DESC")
	items, total, err := store.FindPage[LedgerEntrySelf](rows, page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// ledgerConds reads ?kind=&from=&to= (RFC 3339) of a ledger list into
// conditions on balance_ledger l, of userID's rows only unless it is nil.
func ledgerConds(c *gin.Context, userID *int64) (store.Conds, error) {
	var f store.Conds
	if userID != nil {
		f.And("l.user_id = ?", *userID)
	}
	if v := c.Query("kind"); v != "" {
		f.And("l.kind = ?", v)
	}
	for _, x := range []struct{ q, cond string }{{"from", "l.created_at >= ?"}, {"to", "l.created_at < ?"}} {
		if v := c.Query(x.q); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return nil, core.ErrInvalidArgument.WithMessage("invalid " + x.q)
			}
			f.And(x.cond, t)
		}
	}
	return f, nil
}

// ledgerRows is the balance_ledger rows (alias l) f selects.
func ledgerRows(db *gorm.DB, f store.Conds) *gorm.DB {
	return db.Table("balance_ledger l").Scopes(f.Scope())
}

func (s *Service) allLedger(c *gin.Context) {
	var uid *int64
	if v := c.Query("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("invalid user_id"))
			return
		}
		uid = &id
	}
	s.listLedger(c, uid)
}

// listLedger serves ?kind=&from=&to= (RFC 3339) with pagination.
func (s *Service) listLedger(c *gin.Context, userID *int64) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	f, err := ledgerConds(c, userID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	g := store.G(ctx, s.db.Gorm)
	rows := ledgerRows(g, f).Select(`l.id, l.user_id, COALESCE(u.email, '') AS user_email,
		COALESCE(u.display_name, '') AS user_name, l.delta, l.balance_after, l.kind, l.ref_type, l.ref_id,
		l.idempotency_key, l.operator_id, l.plugin_key, l.note, l.created_at`).
		Joins("LEFT JOIN users u ON u.id = l.user_id").Order("l.id DESC")
	items, total, err := store.FindPage[LedgerEntry](rows, page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// POST /users/:id/balance/adjust {amount, credit, note}; an optional
// Idempotency-Key header makes retries safe.
func (s *Service) adjustBalance(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Amount decimal.Decimal `json:"amount"`
		Credit bool            `json:"credit"`
		Note   string          `json:"note"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	// Limit to numeric(20,8) max to prevent database overflow
	maxAmount := decimal.RequireFromString("999999999999.99999999")
	if in.Amount.GreaterThan(maxAmount) {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("amount exceeds maximum allowed"))
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = uuid.NewString()
	}
	if len(key) > 100 {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("Idempotency-Key too long"))
		return
	}
	operator, _ := core.UserID(ctx)
	// Only users the operator manages, itself only as a superuser (M7, FU6);
	// beyond plain users only with balance:adjust:any.
	if err := core.GuardOrDeny(s.Users).CanActOnUsers(ctx, operator, core.ActOnBalance, id); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// Scope the idempotency key to target user and operator to prevent cross-user reuse
	scopedKey := fmt.Sprintf("admin_adjust:%d:%d:%s", id, operator, key)
	res, err := s.Apply(ctx, core.LedgerChange{
		UserID:         id,
		Amount:         in.Amount,
		Credit:         in.Credit,
		Kind:           core.LedgerKindAdminAdjust,
		RefType:        "admin",
		RefID:          x.Itoa64(operator),
		IdempotencyKey: scopedKey,
		OperatorID:     nullID(operator),
		Note:           in.Note,
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"ledger_id": res.LedgerID, "balance_after": res.BalanceAfter, "duplicate": res.Duplicate})
}
