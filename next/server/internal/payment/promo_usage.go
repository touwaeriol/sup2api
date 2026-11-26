package payment

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// usageView is one use of a promo code (the console's PromoUsage).
type usageView struct {
	ID         int64           `json:"id"`
	UserID     int64           `json:"user_id"`
	UserEmail  string          `json:"user_email,omitempty"`
	OutTradeNo string          `json:"out_trade_no"`
	Bonus      decimal.Decimal `json:"bonus"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UsedAt     *time.Time      `json:"used_at,omitempty"`
}

// promoUsages lists the uses of a promo code, newest first: who, by which
// order, how much, held (reserved) or credited (used).
func (s *Service) promoUsages(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	page, size := httpapi.Pagination(c)
	db := store.G(c.Request.Context(), s.db.Gorm).Table("promo_code_usages AS u").
		Joins("LEFT JOIN users ON users.id = u.user_id").
		Joins("LEFT JOIN payment_orders AS o ON o.id = u.order_id").
		Where("u.promo_code_id = ?", id)
	rows, total, err := store.FindPage[usageView](db.Select(usageColumns).Order("u.id DESC"), page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, rows, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// usageColumns are the columns of a usageView.
const usageColumns = "u.id, u.user_id, coalesce(users.email, '') AS user_email, coalesce(o.out_trade_no, '') AS out_trade_no, " +
	"u.bonus_amount AS bonus, u.status, u.created_at, u.used_at"
