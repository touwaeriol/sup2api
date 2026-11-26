package payment

import (
	"context"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The notify route is public: what guards it besides the gateways'
// verification is here. A client address may send notifyRatePerMinute
// notifications a minute, and write unverifiedEventsPerMinute events tied
// to no order; past that such notifications are still answered, only no
// longer recorded one by one (dropLog), so a flood fills neither
// payment_events nor the log.

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

// reply writes an answer to a notification. It is served from the console
// origin, so it can never render as an active document.
func reply(c *gin.Context, r core.PaymentReply) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(r.Status, r.ContentType, r.Body)
}

func plainReply(status int, text string) core.PaymentReply {
	return core.PaymentReply{Status: status, ContentType: "text/plain; charset=utf-8", Body: []byte(text)}
}

// admitNotify counts the notification against its client address. A
// limiter that fails admits: notifications carry users' payments, and the
// gateways' verification does not depend on the limiter.
func (s *Service) admitNotify(c *gin.Context) bool {
	if s.notes == nil {
		return true
	}
	ok, retry, err := s.notes.Admit(c.Request.Context(), addressKey("notify", c.ClientIP()), notifyRatePerMinute, 0)
	if err != nil {
		slog.WarnContext(c.Request.Context(), "payment: notify rate limit unavailable", "err", err)
		return true
	}
	if !ok {
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		reply(c, plainReply(http.StatusTooManyRequests, "too many requests"))
	}
	return ok
}

// gatewayGone answers a notification for a gateway that is not there: 503
// while orders of it may still be paid (its plugin may come back), else 404.
func (s *Service) gatewayGone(c *gin.Context, id string) {
	open, err := hasPayableOrders(c.Request.Context(), s.db.Gorm, id, s.now())
	if err != nil || open {
		s.recordUnverified(c, id, verdict{kind: eventGatewayGone})
		reply(c, plainReply(http.StatusServiceUnavailable, "retry later"))
		return
	}
	reply(c, plainReply(http.StatusNotFound, "not found"))
}

// recordUnverified records an event tied to no order while the client
// address is within its share; past it the event is only counted.
func (s *Service) recordUnverified(c *gin.Context, gateway string, v verdict) {
	ctx := c.Request.Context()
	if s.notes != nil {
		ok, _, err := s.notes.Admit(ctx, addressKey("event", c.ClientIP()), unverifiedEventsPerMinute, 0)
		if err != nil || !ok {
			s.dropped.note(ctx, gateway, v.kind, s.now())
			return
		}
	}
	_ = s.record(ctx, s.db.Gorm, gateway, v, nil)
}

// addressKey is the limiter id of a client address for one purpose.
func addressKey(purpose, addr string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(purpose + "|" + addr))
	return int64(h.Sum64() & math.MaxInt64)
}

// dropLog counts the unverified events not recorded and logs them at most
// once a minute per node, with how many since the last line.
type dropLog struct {
	mu   sync.Mutex
	n    int
	last time.Time
}

func (d *dropLog) note(ctx context.Context, gateway, kind string, now time.Time) {
	d.mu.Lock()
	d.n++
	if now.Sub(d.last) < time.Minute {
		d.mu.Unlock()
		return
	}
	n := d.n
	d.n, d.last = 0, now
	d.mu.Unlock()
	slog.WarnContext(ctx, "payment: unverified notifications not recorded, over the address's share",
		"count", n, "gateway", gateway, "kind", kind)
}
