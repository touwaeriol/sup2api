package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
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
	eventDuplicate        = "DUPLICATE"
	eventSecondPayment    = "SECOND_PAYMENT"
	eventNotPayable       = "STATUS_NOT_PAYABLE"
	eventGatewayGone      = "GATEWAY_UNAVAILABLE"
	eventStatusPrefix     = "NOTIFY_" // + the gateway's status (NOTIFY_PENDING ...)
)

// The unique constraints a settlement may hit (migration 0126).
const (
	uniqueEventID = "payment_events_gateway_event_id_key"
	uniqueTradeNo = "payment_orders_trade_no_key"
)

// verdict is how the core judged a payment result: the outcome the gateway
// answers with, the event recorded, and whether the order is now paid and
// to be credited.
type verdict struct {
	outcome core.PaymentOutcome
	kind    string
	orderID *int64
	credit  bool
}

func handled(kind string) verdict  { return verdict{outcome: core.PaymentHandled, kind: kind} }
func rejected(kind string) verdict { return verdict{outcome: core.PaymentRejected, kind: kind} }

// notify takes a gateway's notification: the gateway verifies it and says
// which order it is about and what was paid; the core checks that against
// the order and credits it. Anyone can call this route, so nothing in the
// request is trusted before the gateway verified it.
func (s *Service) notify(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("gateway")
	gw, ok := s.gateway(id)
	if !ok {
		s.gatewayGone(c, id)
		return
	}
	n, err := readNotification(c)
	if err != nil {
		reply(c, gw.Respond(core.PaymentRejected))
		return
	}
	res, err := gw.ParseNotification(ctx, n)
	reply(c, gw.Respond(s.handleResult(ctx, id, res, err)))
}

// readNotification reads the request with its body capped and without the
// headers that carry anyone's credentials.
func readNotification(c *gin.Context) (core.PaymentNotification, error) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, notifyBodyLimit))
	h := c.Request.Header.Clone()
	for _, k := range []string{"Cookie", "Authorization", "Proxy-Authorization"} {
		h.Del(k)
	}
	return core.PaymentNotification{Method: c.Request.Method, RawQuery: c.Request.URL.RawQuery, Header: h, Body: body}, err
}

func reply(c *gin.Context, r core.PaymentReply) {
	c.Data(r.Status, r.ContentType, r.Body)
}

// gatewayGone answers a notification for a gateway that is not there: 503
// while orders of it may still be paid (its plugin may come back), else 404.
func (s *Service) gatewayGone(c *gin.Context, id string) {
	ctx := c.Request.Context()
	open, err := hasPayableOrders(ctx, s.db.Gorm, id, s.now())
	if err != nil || open {
		s.record(ctx, s.db.Gorm, id, verdict{kind: eventGatewayGone}, nil)
		c.Data(http.StatusServiceUnavailable, "text/plain; charset=utf-8", []byte("retry later"))
		return
	}
	c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("not found"))
}

// handleResult settles a parsed notification (or query result) and credits
// the order it paid.
func (s *Service) handleResult(ctx context.Context, gateway string, res *core.PaymentResult, err error) core.PaymentOutcome {
	if errors.Is(err, core.ErrPaymentSignature) {
		s.record(ctx, s.db.Gorm, gateway, verdict{kind: eventSignatureInvalid}, nil)
		return core.PaymentRejected
	}
	if err != nil {
		slog.WarnContext(ctx, "payment: notification not parsed", "gateway", gateway, "err", err)
		return core.PaymentRetry
	}
	v, err := s.settle(ctx, gateway, res)
	if err != nil {
		slog.ErrorContext(ctx, "payment: settle notification", "gateway", gateway, "order", res.OutTradeNo, "err", err)
		return core.PaymentRetry
	}
	if v.credit {
		if err := s.credit(ctx, *v.orderID); err != nil {
			slog.ErrorContext(ctx, "payment: credit order", "order", res.OutTradeNo, "err", err)
			return core.PaymentRetry
		}
	}
	return v.outcome
}

// settle judges res against its order under the order's row lock, turns a
// matching payment's order paid, and records the event, all in one
// transaction: a failure leaves nothing behind for the retry to trip on.
func (s *Service) settle(ctx context.Context, gateway string, res *core.PaymentResult) (verdict, error) {
	var v verdict
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		o, err := lockOrder(ctx, tx, res.OutTradeNo)
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
		v = rejected(eventTradeNoReused)
		return v, s.record(ctx, s.db.Gorm, gateway, v, res)
	}
	return v, err
}

// record writes the payment_events row of a notification: the whitelisted
// fields the gateway gave and what the core made of them, never the
// signature, a secret or the body. Only a processed payment keeps its event
// id, which makes a repeat of it a duplicate.
func (s *Service) record(ctx context.Context, q *gorm.DB, gateway string, v verdict, res *core.PaymentResult) error {
	ev := model.PaymentEvent{OrderID: v.orderID, Gateway: gateway, Kind: v.kind, Verified: res != nil, Detail: json.RawMessage("{}")}
	if res != nil {
		detail := map[string]string{"out_trade_no": res.OutTradeNo, "trade_no": res.TradeNo, "status": string(res.Status),
			"amount": strconv.FormatInt(res.Amount, 10), "currency": res.Currency}
		addFields(detail, res.Fields)
		ev.Detail, _ = json.Marshal(detail)
		if v.kind == eventNotifyOK && res.EventID != "" {
			ev.EventID = &res.EventID
		}
	}
	err := store.G(ctx, q).Create(&ev).Error
	if err != nil && !store.InTx(q) {
		slog.WarnContext(ctx, "payment: record event", "gateway", gateway, "kind", v.kind, "err", err)
	}
	return err
}

// Bounds of the gateway's fields kept on an event: a plugin gateway decides
// what it reports, the event table stays small.
const (
	maxEventFields     = 32
	maxEventFieldBytes = 256
)

// addFields adds the gateway's whitelisted fields to detail, without
// overriding the core's own, within the bounds.
func addFields(detail, fields map[string]string) {
	for k, val := range fields {
		if _, taken := detail[k]; taken || len(detail) >= maxEventFields || len(k) > maxEventFieldBytes {
			continue
		}
		if len(val) > maxEventFieldBytes {
			val = val[:maxEventFieldBytes]
		}
		detail[k] = val
	}
}
