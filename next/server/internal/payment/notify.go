package payment

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Kinds of payment_events rows.
const (
	eventNotifyOK         = "NOTIFY_OK"
	eventSignatureInvalid = "SIGNATURE_INVALID"
	eventOrderNotFound    = "ORDER_NOT_FOUND"
	eventGatewayMismatch  = "GATEWAY_MISMATCH"
	eventMerchantMismatch = "MERCHANT_MISMATCH"
	eventMethodMismatch   = "METHOD_MISMATCH"
	eventSessionMismatch  = "SESSION_MISMATCH"
	eventAmountMismatch   = "PAYMENT_AMOUNT_MISMATCH"
	eventTradeNoMissing   = "TRADE_NO_MISSING"
	eventTradeNoReused    = "TRADE_NO_REUSED"
	eventFieldTooLong     = "FIELD_TOO_LONG"
	eventStatusUnknown    = "STATUS_UNKNOWN"
	eventDuplicate        = "DUPLICATE"
	eventSecondPayment    = "SECOND_PAYMENT"
	eventNotPayable       = "STATUS_NOT_PAYABLE"
	eventGatewayGone      = "GATEWAY_UNAVAILABLE"
	eventStatusPrefix     = "NOTIFY_" // + the gateway's status (NOTIFY_PENDING ...)
)

// The unique constraints a settlement may hit (migration 0126).
const (
	uniqueEventID = "payment_events_gateway_event_id_key"
	uniqueTradeNo = "payment_orders_gateway_trade_no_key"
)

// verdict is how the core judged a payment result: the outcome the gateway
// answers with, the event recorded (reason: why an unverified one failed),
// and whether the order is now paid and to be credited.
type verdict struct {
	outcome core.PaymentOutcome
	kind    string
	reason  string
	orderID *int64
	credit  bool
}

func handled(kind string) verdict  { return verdict{outcome: core.PaymentHandled, kind: kind} }
func rejected(kind string) verdict { return verdict{outcome: core.PaymentRejected, kind: kind} }

// errNoResult: a gateway reported neither a result nor an error.
var errNoResult = errors.New("payment gateway returned no result")

// notify takes a gateway's notification: the gateway verifies it and says
// which order it is about and what was paid; the core checks that against
// the order and credits it. Anyone can call this route, so nothing in the
// request is trusted before the gateway verified it.
func (s *Service) notify(c *gin.Context) {
	if !s.admitNotify(c) {
		return
	}
	ctx := c.Request.Context()
	id := c.Param("gateway")
	gw, ok := s.gateway(id)
	if !ok {
		s.gatewayGone(c, id)
		return
	}
	n, err := readNotification(c)
	if err != nil {
		reply(c, core.StatusPaymentReply(core.PaymentRejected))
		return
	}
	res, respond, err := gw.ParseNotification(ctx, n)
	if errors.Is(err, core.ErrPaymentSignature) {
		s.recordUnverified(c, id, verdict{kind: eventSignatureInvalid, reason: x.Truncate(err.Error(), maxReasonBytes)})
		reply(c, respond(core.PaymentRejected))
		return
	}
	outcome, _ := s.process(ctx, id, res, err)
	reply(c, respond(outcome))
}

// process settles a verified notification (or query result) and credits
// the order it paid; paid tells whether the order is paid now.
func (s *Service) process(ctx context.Context, gateway string, res *core.PaymentResult, err error) (core.PaymentOutcome, bool) {
	if err == nil && res == nil {
		err = errNoResult
	}
	if err != nil {
		slog.WarnContext(ctx, "payment: notification not parsed", "gateway", gateway, "err", err)
		return core.PaymentRetry, false
	}
	v, err := s.settle(ctx, gateway, res)
	if err != nil {
		slog.ErrorContext(ctx, "payment: settle notification", "gateway", gateway, "order", res.OutTradeNo, "err", err)
		return core.PaymentRetry, false
	}
	if v.credit {
		if err := s.credit(ctx, *v.orderID); err != nil {
			slog.ErrorContext(ctx, "payment: credit order", "order", res.OutTradeNo, "err", err)
			return core.PaymentRetry, true
		}
	}
	return v.outcome, v.credit
}

// settle judges res against its order under the order's row lock, turns a
// matching payment's order paid, and records the event, all in one
// transaction: a failure leaves nothing behind for the retry to trip on.
func (s *Service) settle(ctx context.Context, gateway string, res *core.PaymentResult) (verdict, error) {
	var v verdict
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		o, err := lockOrder(ctx, tx, numbered(res.OutTradeNo))
		if err != nil {
			return err
		}
		if v, err = s.judge(ctx, tx, gateway, o, res); err != nil {
			return err
		}
		if v.kind == eventNotifyOK {
			if err := s.markPaid(ctx, tx, o, res); err != nil {
				return err
			}
		}
		return s.record(ctx, tx, gateway, v, res)
	})
	switch {
	case store.IsUniqueViolation(err, uniqueEventID):
		return handled(eventDuplicate), nil
	case store.IsUniqueViolation(err, uniqueTradeNo):
		// The rolled back verdict still names the order it was about.
		v = verdict{outcome: core.PaymentRejected, kind: eventTradeNoReused, orderID: v.orderID}
		return v, s.record(ctx, s.db.Gorm, gateway, v, res)
	}
	return v, err
}

// record writes the payment_events row of a notification: the whitelisted
// fields the gateway gave and what the core made of them, never the
// signature, a secret or the body. Only a processed payment keeps its event
// id, which makes a repeat of it a duplicate.
func (s *Service) record(ctx context.Context, q *gorm.DB, gateway string, v verdict, res *core.PaymentResult) error {
	ev := model.PaymentEvent{OrderID: v.orderID, Gateway: gateway, Kind: v.kind, Verified: res != nil}
	detail := map[string]string{}
	if v.reason != "" {
		detail["error"] = v.reason
	}
	if res != nil {
		addFields(detail, map[string]string{"out_trade_no": res.OutTradeNo, "trade_no": res.TradeNo,
			"status": string(res.Status), "amount": strconv.FormatInt(res.Amount, 10), "currency": res.Currency})
		addFields(detail, res.Fields)
		if v.kind == eventNotifyOK && res.EventID != "" {
			ev.EventID = &res.EventID
		}
	}
	ev.Detail, _ = json.Marshal(detail)
	err := store.G(ctx, q).Create(&ev).Error
	if err != nil && !store.InTx(q) {
		slog.WarnContext(ctx, "payment: record event", "gateway", gateway, "kind", v.kind, "err", err)
	}
	return err
}

// Bounds of the fields kept on an event: a plugin gateway decides what it
// reports, and an unverified error text is anyone's; the table stays small.
const (
	maxEventFields     = 32
	maxEventFieldBytes = 256
	maxReasonBytes     = 200
)

// addFields adds fields to detail, without overriding what it holds,
// within the bounds.
func addFields(detail, fields map[string]string) {
	for k, val := range fields {
		if _, taken := detail[k]; taken || len(detail) >= maxEventFields || len(k) > maxEventFieldBytes {
			continue
		}
		detail[k] = x.Truncate(val, maxEventFieldBytes) // never splits a character
	}
}
