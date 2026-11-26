package payment

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// judge checks a verified payment result against its order (locked), in
// this order: the order exists, the result's references fit the columns,
// the order is of this gateway, and the merchant,
// method and session the gateway reports are the order's; a status other
// than paid is only recorded; the amount and currency equal the order's
// snapshot; a repeat of the order's own payment is a duplicate (credited
// again if crediting has not finished); the event id and the gateway
// transaction number are new; the order may still be paid.
func (s *Service) judge(ctx context.Context, tx *gorm.DB, gateway string, o *model.PaymentOrder, res *core.PaymentResult) (verdict, error) {
	if o == nil {
		return handled(eventOrderNotFound), nil
	}
	v := matchOrder(gateway, o, res)
	if v.kind == "" {
		var err error
		v, err = s.judgeState(ctx, tx, gateway, o, res)
		if err != nil {
			return v, err
		}
	}
	v.orderID = &o.ID
	return v, nil
}

// matchOrder: the result is about this order of this gateway and pays it
// exactly; an empty kind when it does.
func matchOrder(gateway string, o *model.PaymentOrder, res *core.PaymentResult) verdict {
	switch {
	case oversized(res):
		return rejected(eventFieldTooLong)
	case !knownStatuses[res.Status]:
		return rejected(eventStatusUnknown)
	case o.Gateway != gateway:
		return rejected(eventGatewayMismatch)
	case res.MerchantRef != "" && res.MerchantRef != o.MerchantRef:
		return rejected(eventMerchantMismatch)
	case res.MethodType != "" && res.MethodType != o.Method:
		return rejected(eventMethodMismatch)
	case res.SessionID != "" && o.GatewaySessionID != "" && res.SessionID != o.GatewaySessionID:
		return rejected(eventSessionMismatch)
	case res.Status != core.PaymentPaid:
		return handled(eventStatusPrefix + strings.ToUpper(string(res.Status)))
	}
	minor, ok := core.PaymentMinorUnits(o.PayAmount, o.Currency)
	if !ok || minor != res.Amount || !strings.EqualFold(res.Currency, o.Currency) {
		return rejected(eventAmountMismatch)
	}
	if res.TradeNo == "" {
		return rejected(eventTradeNoMissing)
	}
	return verdict{}
}

// judgeState decides a matching payment by the order's state.
func (s *Service) judgeState(ctx context.Context, tx *gorm.DB, gateway string, o *model.PaymentOrder, res *core.PaymentResult) (verdict, error) {
	if o.TradeNo != nil {
		if *o.TradeNo != res.TradeNo {
			return handled(eventSecondPayment), nil
		}
		v := handled(eventDuplicate)
		v.credit = statusOf(o) == statusPaid
		return v, nil
	}
	if !lateOK(o, s.now()) {
		return handled(eventNotPayable), nil
	}
	db := store.G(ctx, tx)
	if res.EventID != "" {
		seen, err := gormpg.Exists(db, db.Model(&model.PaymentEvent{}).Where("gateway = ? AND event_id = ?", gateway, res.EventID))
		if err != nil || seen {
			return handled(eventDuplicate), err
		}
	}
	reused, err := gormpg.Exists(db, db.Model(&model.PaymentOrder{}).Where("gateway = ? AND trade_no = ?", gateway, res.TradeNo))
	if err != nil || reused {
		return rejected(eventTradeNoReused), err
	}
	v := handled(eventNotifyOK)
	v.credit = true
	return v, nil
}

// markPaid turns the order paid with the gateway's transaction number. A
// late payment (expired or cancelled order) takes its promo code's use
// again; when the code no longer applies the order is credited without the
// bonus and says why.
func (s *Service) markPaid(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder, res *core.PaymentResult) error {
	set := map[string]any{"trade_no": res.TradeNo, "paid_at": clause.Expr{SQL: "now()"}}
	if o.GatewaySessionID == "" && res.SessionID != "" {
		set["gateway_session_id"] = res.SessionID
	}
	if statusOf(o) != statusPending {
		keep, err := reacquireUse(ctx, tx, o, s.now())
		if err != nil {
			return err
		}
		if !keep {
			set["bonus"], set["fail_reason"] = 0, "paid late: the promo code no longer applies, its bonus is not credited"
		}
	}
	ok, err := setStatus(ctx, tx, o.ID, statusPaid, set, statusPending, statusExpired, statusCancelled)
	if err == nil && !ok {
		err = errLostRace
	}
	return err
}

// knownStatuses are the statuses of core.PaymentStatus; another one (a
// plugin's) would not even fit the event's kind.
var knownStatuses = map[core.PaymentStatus]bool{core.PaymentPaid: true, core.PaymentPending: true,
	core.PaymentFailed: true, core.PaymentExpired: true}

// oversized: a reference the result carries does not fit its column, so
// it could never be stored; rejecting it beats retrying a failing write.
func oversized(res *core.PaymentResult) bool {
	for _, ref := range []string{res.TradeNo, res.SessionID, res.MerchantRef, res.EventID} {
		if len(ref) > maxGatewayRef {
			return true
		}
	}
	return false
}
