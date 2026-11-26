package payment

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// pageOrders reads one page of the orders matching conds, newest first.
func (s *Service) pageOrders(c *gin.Context, conds store.Conds) ([]model.PaymentOrder, httpapi.Page, error) {
	page, size := httpapi.Pagination(c)
	db := store.G(c.Request.Context(), s.db.Gorm).Model(&model.PaymentOrder{}).Scopes(conds.Scope())
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, httpapi.Page{}, err
	}
	var rows []model.PaymentOrder
	err := db.Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error
	return rows, httpapi.Page{Page: page, PageSize: size, Total: total}, err
}

// adminOrders lists every order: status, method and the created_at range
// (RFC 3339, to exclusive) filter; q finds an order number, a gateway
// transaction number or a user's email.
func (s *Service) adminOrders(c *gin.Context) {
	conds, err := s.adminFilters(c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	rows, page, err := s.pageOrders(c, conds)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	emails, err := s.emails(c.Request.Context(), rows)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]orderView, len(rows))
	for i := range rows {
		out[i] = viewOf(&rows[i], emails[rows[i].UserID])
	}
	httpapi.List(c, out, page)
}

func (s *Service) adminFilters(c *gin.Context) (store.Conds, error) {
	var conds store.Conds
	for _, f := range []struct{ q, cond string }{{"status", "status = ?"}, {"method", "method = ?"}} {
		if v := c.Query(f.q); v != "" {
			conds.And(f.cond, v)
		}
	}
	for _, f := range []struct{ q, cond string }{{"from", "created_at >= ?"}, {"to", "created_at < ?"}} {
		if v := c.Query(f.q); v != "" {
			at, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return nil, core.ErrInvalidArgument.WithMessage("invalid " + f.q + " (RFC 3339)")
			}
			conds.And(f.cond, at)
		}
	}
	if q := c.Query("q"); q != "" {
		like := "%" + store.Like(q) + "%"
		users := store.G(c.Request.Context(), s.db.Gorm).Model(&model.User{}).Select("id").Where("email ILIKE ?", like)
		conds.And("(out_trade_no ILIKE ? OR trade_no ILIKE ? OR user_id IN (?))", like, like, users)
	}
	return conds, nil
}

// emails are the emails of the orders' users.
func (s *Service) emails(ctx context.Context, rows []model.PaymentOrder) (map[int64]string, error) {
	ids := make(store.Int64s, len(rows))
	for i, r := range rows {
		ids[i] = r.UserID
	}
	var users []model.User
	err := store.G(ctx, s.db.Gorm).Select("id", "email").Where("id = ANY(?)", ids).Find(&users).Error
	out := make(map[int64]string, len(users))
	for _, u := range users {
		out[u.ID] = u.Email
	}
	return out, err
}

// statsView is GET /topup/orders/stats (the console's OrderStats).
type statsView struct {
	TodayAmount  Num   `json:"today_amount"`
	TodayCount   int64 `json:"today_count"`
	MonthAmount  Num   `json:"month_amount"`
	PendingCount int64 `json:"pending_count"`
	FailedCount  int64 `json:"failed_count"`
}

// orderStats sums the completed top-ups (credited amount, bonus excluded)
// of today and this month in the request's time zone (tz=, default UTC;
// the site has no time zone setting), and counts the pending and failed
// orders.
func (s *Service) orderStats(c *gin.Context) {
	ctx := c.Request.Context()
	now := s.now().In(core.TimeZone(ctx))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var out statsView
	err := s.db.TxOptions(ctx, store.ReadOnly(), func(tx *gorm.DB) error {
		var err error
		if out.TodayAmount, out.TodayCount, err = completedSince(ctx, tx, day); err == nil {
			out.MonthAmount, _, err = completedSince(ctx, tx, month)
		}
		for _, x := range []struct {
			status string
			n      *int64
		}{{statusPending, &out.PendingCount}, {statusFailed, &out.FailedCount}} {
			if err == nil {
				err = store.G(ctx, tx).Model(&model.PaymentOrder{}).Where("status = ?", x.status).Count(x.n).Error
			}
		}
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// completedSince sums and counts the orders completed since at.
func completedSince(ctx context.Context, tx *gorm.DB, at time.Time) (Num, int64, error) {
	var row struct {
		Total decimal.NullDecimal
		N     int64
	}
	err := store.G(ctx, tx).Model(&model.PaymentOrder{}).Select("sum(amount) AS total, count(*) AS n").
		Where("status = ? AND completed_at >= ?", statusCompleted, at).Scan(&row).Error
	return num(row.Total.Decimal), row.N, err
}
