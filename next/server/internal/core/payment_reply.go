package core

import "net/http"

// PaymentOutcome is how the core handled a notification; the notification's
// PaymentResponder turns it into the reply its sender expects.
type PaymentOutcome int

const (
	// PaymentHandled: processed, a duplicate, or about no order of this site.
	PaymentHandled PaymentOutcome = iota
	// PaymentRejected: failed verification or did not match the order.
	PaymentRejected
	// PaymentRetry: the core could not process it now (database, ledger,
	// gateway unavailable); the sender should retry.
	PaymentRetry
)

// HTTPStatus is the status that tells a sender the outcome: 200 handled,
// 400 rejected, 503 retry (also for an outcome this core does not know, so
// nothing is lost).
func (o PaymentOutcome) HTTPStatus() int {
	switch o {
	case PaymentHandled:
		return http.StatusOK
	case PaymentRejected:
		return http.StatusBadRequest
	}
	return http.StatusServiceUnavailable
}

// PaymentReply is the HTTP answer to a notification.
type PaymentReply struct {
	Status      int
	ContentType string
	Body        []byte
}

// PaymentResponder answers one notification for the core's outcome.
type PaymentResponder func(PaymentOutcome) PaymentReply

// PlainTextReplyType is the content type of a plain-text reply.
const PlainTextReplyType = "text/plain; charset=utf-8"

// PlainPaymentReply is a plain-text answer.
func PlainPaymentReply(status int, body string) PaymentReply {
	return PaymentReply{Status: status, ContentType: PlainTextReplyType, Body: []byte(body)}
}

// StatusPaymentReply answers o with its status and the status text: the
// reply of a sender that reads only the status (Stripe), and of a
// notification no gateway has answered for.
func StatusPaymentReply(o PaymentOutcome) PaymentReply {
	s := o.HTTPStatus()
	return PlainPaymentReply(s, http.StatusText(s))
}
