package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// fakeGateway is a gateway the tests drive: a notification's body is the
// PaymentResult as JSON, header X-Bad-Sign fails verification (its value is
// the reason), and queries answer from paid (or are unsupported, or fail
// with queryErr, or block until their context ends).
type fakeGateway struct {
	mu        sync.Mutex
	merchant  string
	paid      map[string]*core.PaymentResult
	created   []core.PaymentCreate
	fail      bool
	answer    *core.PaymentCreated // replaces the created payment
	nilResult bool
	queryErr  error
	block     bool
	queries   int
	deadline  time.Duration // how long the last query's context had
}

func (g *fakeGateway) CreatePayment(_ context.Context, in core.PaymentCreate) (*core.PaymentCreated, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail {
		return nil, fmt.Errorf("gateway down")
	}
	g.created = append(g.created, in)
	if g.answer != nil {
		return g.answer, nil
	}
	return &core.PaymentCreated{PayURL: "https://pay.test/" + in.OutTradeNo, QRCode: "qr:" + in.OutTradeNo,
		SessionID: "sess_" + in.OutTradeNo, MerchantRef: g.merchant}, nil
}

func (g *fakeGateway) ParseNotification(_ context.Context, n core.PaymentNotification) (*core.PaymentResult, error) {
	if reason := n.Header.Get("X-Bad-Sign"); reason != "" {
		return nil, fmt.Errorf("fake: %w: %s", core.ErrPaymentSignature, reason)
	}
	if g.nilResult {
		return nil, nil
	}
	if n.Header.Get("Cookie") != "" || n.Header.Get("Authorization") != "" {
		return nil, fmt.Errorf("fake: credentials reached the gateway")
	}
	var res core.PaymentResult
	if err := json.Unmarshal(n.Body, &res); err != nil {
		return nil, fmt.Errorf("fake: %w", core.ErrPaymentSignature)
	}
	return &res, nil
}

func (g *fakeGateway) QueryPayment(ctx context.Context, q core.PaymentQuery) (*core.PaymentResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.queries++
	if at, ok := ctx.Deadline(); ok {
		g.deadline = time.Until(at)
	}
	if g.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if res, ok := g.paid[q.OutTradeNo]; ok {
		return res, nil
	}
	if g.queryErr != nil {
		return nil, g.queryErr
	}
	return nil, core.ErrPaymentQueryUnsupported
}

func (g *fakeGateway) Respond(o core.PaymentOutcome) core.PaymentReply {
	status := map[core.PaymentOutcome]int{core.PaymentHandled: 200, core.PaymentRejected: 400, core.PaymentRetry: 503}[o]
	return core.PaymentReply{Status: status, ContentType: "text/plain", Body: []byte(strconv.Itoa(status))}
}

// fakeGeneration is a plugin generation with payment gateways.
type fakeGeneration struct {
	core.Generation
	gateways []core.PaymentGatewayBinding
}

func (g fakeGeneration) PaymentGateways() []core.PaymentGatewayBinding { return g.gateways }

type fakeRegistry struct {
	mu  sync.Mutex
	gen core.Generation
}

func (r *fakeRegistry) Current() core.Generation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}
func (r *fakeRegistry) OnChange(func(core.Generation)) func() { return func() {} }

func (r *fakeRegistry) set(bindings ...core.PaymentGatewayBinding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen = fakeGeneration{gateways: bindings}
}

// fixture is a payment service on a fresh database, with a real ledger and
// the fake gateway as epay.
type fixture struct {
	t      *testing.T
	db     *store.DB
	svc    *Service
	gw     *fakeGateway
	reg    *fakeRegistry
	admin  int64
	engine *gin.Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testutil.DB(t)
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	cipher, err := secret.New([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	f := &fixture{t: t, db: db, gw: &fakeGateway{merchant: "1001", paid: map[string]*core.PaymentResult{}}, reg: &fakeRegistry{}}
	f.reg.set()
	f.svc = New(Deps{DB: db, Cipher: cipher, Ledger: billing.New(db, rdb, nil, nil, nil), Plugins: f.reg,
		PublicURL: "https://site.test", Builtin: map[string]core.PaymentGateway{core.PaymentGatewayEpay: f.gw, core.PaymentGatewayStripe: &fakeGateway{merchant: "test"}}})
	f.admin = testutil.InsertUser(t, db, "pay-admin@x.test")
	gin.SetMode(gin.TestMode)
	f.engine = gin.New()
	f.svc.RegisterRoutes(httpapi.NewRouter(f.engine, fakeTokens{}, roles{admin: f.admin}))
	return f
}

// adminCtx is a request context of the administrator.
func (f *fixture) adminCtx() context.Context { return core.WithUserID(context.Background(), f.admin) }

// configure saves top-up settings with epay on and one alipay method.
func (f *fixture) configure(edit ...func(*settingsValues)) {
	f.t.Helper()
	v := settingsValues{Settings: defaultSettings(), Methods: []Method{
		{Type: "alipay", Name: "Alipay", Gateway: core.PaymentGatewayEpay, Icon: "alipay", Enabled: true}}}
	v.Enabled, v.Currency, v.Price, v.MaxPendingOrders = true, "CNY", num(decimalOf("7.3")), 3
	v.Discounts = []Discount{{Amount: numInt(100), Rate: num(decimalOf("0.9"))}}
	v.Epay = EpaySettings{Enabled: true, Endpoint: "https://epay.test", MerchantID: "1001", Key: "epay-secret"}
	for _, e := range edit {
		e(&v)
	}
	require.NoError(f.t, f.svc.saveSettings(f.adminCtx(), v))
}

func (f *fixture) user(name string) int64 {
	return testutil.InsertUser(f.t, f.db, name+"@x.test")
}

// order places an order of amount for uid and returns it.
func (f *fixture) order(uid int64, amount string, promo ...string) *model.PaymentOrder {
	f.t.Helper()
	in := quoteInput{Amount: num(decimalOf(amount)), Method: "alipay"}
	if len(promo) > 0 {
		in.PromoCode = promo[0]
	}
	return f.place(uid, in)
}

// place places in for uid and returns the order.
func (f *fixture) place(uid int64, in quoteInput) *model.PaymentOrder {
	f.t.Helper()
	out, err := f.svc.placeOrder(context.Background(), uid, in, "127.0.0.1")
	require.NoError(f.t, err)
	return f.load(out.OutTradeNo)
}

func (f *fixture) load(no string) *model.PaymentOrder {
	f.t.Helper()
	var o model.PaymentOrder
	require.NoError(f.t, f.db.Gorm.Where("out_trade_no = ?", no).Take(&o).Error)
	return &o
}

// paid is the notification paying o in full.
func paid(o *model.PaymentOrder, event string) core.PaymentResult {
	minor, _ := core.PaymentMinorUnits(o.PayAmount, o.Currency)
	return core.PaymentResult{OutTradeNo: o.OutTradeNo, TradeNo: "T" + o.OutTradeNo, SessionID: o.GatewaySessionID,
		Amount: minor, Currency: o.Currency, Status: core.PaymentPaid, EventID: event, MerchantRef: o.MerchantRef, MethodType: o.Method}
}

// notify posts res to the notify route of gateway and returns the status.
func (f *fixture) notify(gateway string, res core.PaymentResult, header ...string) int {
	body, _ := json.Marshal(res)
	return f.notifyRaw(gateway, string(body), header...).Code
}

// notifyRaw posts body to the notify route of gateway.
func (f *fixture) notifyRaw(gateway, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment/notify/"+gateway, strings.NewReader(body))
	req.Header.Set("Cookie", "s=1")
	req.Header.Set("Authorization", "Bearer 1")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

// call sends a console request as uid.
func (f *fixture) call(uid int64, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(uid, 10))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

// ledgerSum is what the ledger credited uid under the key prefix.
func (f *fixture) ledgerSum(uid int64, prefix string) (decimal.Decimal, int64) {
	f.t.Helper()
	var rows []model.BalanceLedger
	require.NoError(f.t, f.db.Gorm.Where("user_id = ? AND idempotency_key LIKE ?", uid, prefix+"%").Find(&rows).Error)
	sum := decimal.Zero
	for _, r := range rows {
		sum = sum.Add(r.Delta)
	}
	return sum, int64(len(rows))
}

// age moves the order's clock back by d (expiry and last status change).
func (f *fixture) age(o *model.PaymentOrder, d time.Duration) {
	f.t.Helper()
	require.NoError(f.t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).
		Updates(map[string]any{"expires_at": o.ExpiresAt.Add(-d), "updated_at": time.Now().Add(-d)}).Error)
}

// concurrently runs fn n times at once.
func concurrently(n int, fn func(i int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
}

type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "")
}

// roles grants the administrator every permission and everyone else
// topup:use only, as the built-in roles do.
type roles struct{ admin int64 }

func (r roles) Can(_ context.Context, uid int64, p string) (bool, error) {
	return uid == r.admin || p == string(perm.TopupUse), nil
}
func (r roles) PermissionSet(_ context.Context, uid int64) (core.PermissionSet, error) {
	if uid == r.admin {
		return core.PermissionSetOf("*"), nil
	}
	return core.PermissionSetOf(string(perm.TopupUse)), nil
}
func (roles) CanGrant(context.Context, int64, []string) error { return nil }
