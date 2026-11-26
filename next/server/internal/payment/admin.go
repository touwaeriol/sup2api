package payment

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// pageOrders reads one page of the orders matching conds, newest first.
func (s *Service) pageOrders(c *gin.Context, conds store.Conds) ([]model.PaymentOrder, httpapi.Page, error) {
	page, size := httpapi.Pagination(c)
	db := store.G(c.Request.Context(), s.db.Gorm).Model(&model.PaymentOrder{}).Scopes(conds.Scope())
	rows, total, err := store.FindPage[model.PaymentOrder](db.Order("id DESC"), page, size)
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
		out[i] = viewOf(&rows[i], emails[rows[i].UserID], s.now())
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
				return nil, core.ErrInvalidArgument.WithMessage(t(c.Request.Context(), "invalid "+f.q+" (RFC 3339)", f.q+" 不是 RFC 3339 时间"))
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
	TodayAmount  decimal.Decimal `json:"today_amount"`
	TodayCount   int64           `json:"today_count"`
	MonthAmount  decimal.Decimal `json:"month_amount"`
	PendingCount int64           `json:"pending_count"`
	FailedCount  int64           `json:"failed_count"`
}

// orderStats sums the completed top-ups (credited amount, bonus excluded)
// of today and this month in the request's time zone (tz=, default UTC;
// the site has no time zone setting), and counts the pending and failed
// orders.
func (s *Service) orderStats(c *gin.Context) {
	ctx := c.Request.Context()
	var out statsView
	err := s.db.TxOptions(ctx, store.ReadOnly(), func(tx *gorm.DB) error { return s.stats(ctx, tx, &out) })
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

func (s *Service) stats(ctx context.Context, tx *gorm.DB, out *statsView) error {
	now := s.now().In(core.TimeZone(ctx))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var err error
	if out.TodayAmount, out.TodayCount, err = completedSince(ctx, tx, day); err != nil {
		return err
	}
	if out.MonthAmount, _, err = completedSince(ctx, tx, month); err != nil {
		return err
	}
	for status, n := range map[string]*int64{statusPending: &out.PendingCount, statusFailed: &out.FailedCount} {
		if err := store.G(ctx, tx).Model(&model.PaymentOrder{}).Where("status = ?", status).Count(n).Error; err != nil {
			return err
		}
	}
	return nil
}

// completedSince sums and counts the orders completed since at.
func completedSince(ctx context.Context, tx *gorm.DB, at time.Time) (decimal.Decimal, int64, error) {
	var row struct {
		Total decimal.NullDecimal
		N     int64
	}
	err := store.G(ctx, tx).Model(&model.PaymentOrder{}).Select("sum(amount) AS total, count(*) AS n").
		Where("status = ? AND completed_at >= ?", statusCompleted, at).Scan(&row).Error
	return row.Total.Decimal, row.N, err
}

// eventView is one payment_events row of an order: what the core made of a
// notification or query, with the whitelisted fields it kept.
type eventView struct {
	ID        int64             `json:"id"`
	Gateway   string            `json:"gateway"`
	Kind      string            `json:"kind"`
	Verified  bool              `json:"verified"`
	EventID   string            `json:"event_id,omitempty"`
	Detail    map[string]string `json:"detail"`
	CreatedAt time.Time         `json:"created_at"`
}

// eventViewOf is the event as the API shows it: the amount (kept in the
// currency's minor unit) in major units.
func eventViewOf(e *model.PaymentEvent) eventView {
	v := eventView{ID: e.ID, Gateway: e.Gateway, Kind: e.Kind, Verified: e.Verified, Detail: map[string]string{}, CreatedAt: e.CreatedAt}
	if e.EventID != nil {
		v.EventID = *e.EventID
	}
	_ = json.Unmarshal(e.Detail, &v.Detail) // the core writes it as a string map
	if minor, ok := v.Detail["amount"]; ok {
		v.Detail["amount"] = majorUnits(minor, v.Detail["currency"])
	}
	return v
}

// majorUnits is a minor-unit amount of a settlement currency in major
// units; anything else (a currency outside the table, not a number) as it
// is.
func majorUnits(minor, currency string) string {
	exp, ok := protocol.PaymentExponent(currency)
	n, err := strconv.ParseInt(minor, 10, 64)
	if !ok || err != nil {
		return minor
	}
	return decimal.New(n, -exp).String()
}

// orderEvents lists the events of an order, newest first: mismatches,
// duplicates and second payments the administrator should see.
func (s *Service) orderEvents(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	page, size := httpapi.Pagination(c)
	db := store.G(c.Request.Context(), s.db.Gorm).Model(&model.PaymentEvent{}).Where("order_id = ?", id)
	rows, total, err := store.FindPage[model.PaymentEvent](db.Order("id DESC"), page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]eventView, len(rows))
	for i := range rows {
		out[i] = eventViewOf(&rows[i])
	}
	httpapi.List(c, out, httpapi.Page{Page: page, PageSize: size, Total: total})
}
