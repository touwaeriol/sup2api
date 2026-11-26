package epay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// queryServer answers api.php with status and body and checks the request.
func queryServer(t *testing.T, status int, body string) (*Gateway, func()) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/api.php" || q.Get("act") != "order" ||
			q.Get("pid") != "1001" || q.Get("key") != testKey || q.Get("out_trade_no") != "s2a_order1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	cfg := testConfig()
	cfg.Endpoint = srv.URL
	return newTestGateway(cfg, srv.Client()), srv.Close
}

func query(g *Gateway) (*core.PaymentResult, error) {
	return g.QueryPayment(context.Background(), core.PaymentQuery{OutTradeNo: "s2a_order1", MerchantRef: "1001"})
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "api.php")) {
		t.Fatalf("error leaks the request: %v", err)
	}
}

func TestQueryPaymentAnswers(t *testing.T) {
	cases := map[string]struct {
		body   string
		status core.PaymentStatus
		amount int64
	}{
		"numeric status paid":            {`{"code":1,"msg":"ok","trade_no":"T1","out_trade_no":"s2a_order1","type":"alipay","pid":"1001","money":"12.34","status":1}`, core.PaymentPaid, 1234},
		"string code, trade_status paid": {`{"code":"1","trade_no":"T1","out_trade_no":"s2a_order1","type":"alipay","money":12.34,"trade_status":"TRADE_SUCCESS"}`, core.PaymentPaid, 1234},
		"trade_status wins over status":  {`{"code":1,"trade_no":"T1","out_trade_no":"s2a_order1","type":"alipay","money":"12.34","status":1,"trade_status":"WAIT"}`, core.PaymentPending, 1234},
		"unpaid without money":           {`{"code":1,"out_trade_no":"s2a_order1","status":0}`, core.PaymentPending, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g, stop := queryServer(t, http.StatusOK, c.body)
			defer stop()
			got, err := query(g)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != c.status || got.Amount != c.amount || got.Currency != "CNY" ||
				got.MerchantRef != "1001" || got.OutTradeNo != "s2a_order1" || got.EventID != "" {
				t.Fatalf("result = %+v", got)
			}
		})
	}
}

func TestQueryPaymentUnsupported(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"code 0 echoing the key": {200, `{"code":0,"msg":"bad key ` + testKey + `"}`},
		"not JSON":               {200, `<html>not here</html>`},
		"no api.php":             {404, ``},
		"not implemented":        {501, ``},
		"other order":            {200, `{"code":1,"out_trade_no":"s2a_other","status":1,"money":"1.00"}`},
		"other merchant":         {200, `{"code":1,"pid":"2002","out_trade_no":"s2a_order1","status":1,"money":"1.00"}`},
		"paid without money":     {200, `{"code":1,"out_trade_no":"s2a_order1","status":1}`},
		"paid with bad money":    {200, `{"code":1,"out_trade_no":"s2a_order1","status":1,"money":"1.001"}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g, stop := queryServer(t, c.status, c.body)
			defer stop()
			_, err := query(g)
			if !errors.Is(err, core.ErrPaymentQueryUnsupported) {
				t.Fatalf("err = %v", err)
			}
			assertNoSecret(t, err)
		})
	}
}

func TestQueryPaymentHTTPError(t *testing.T) {
	g, stop := queryServer(t, http.StatusBadGateway, `oops`)
	defer stop()
	_, err := query(g)
	if err == nil || errors.Is(err, core.ErrPaymentQueryUnsupported) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, err)
}

func TestQueryPaymentConnectionError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cfg := testConfig()
	cfg.Endpoint = "https://" + addr
	_, err = query(newTestGateway(cfg, &http.Client{}))
	if err == nil || errors.Is(err, core.ErrPaymentQueryUnsupported) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, err)
}

func TestQueryPaymentCanceledContext(t *testing.T) {
	g, stop := queryServer(t, http.StatusOK, `{}`)
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: "s2a_order1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, err)
}

func TestQueryPaymentOtherMerchantOrder(t *testing.T) {
	g := newTestGateway(testConfig(), nil)
	_, err := g.QueryPayment(context.Background(), core.PaymentQuery{OutTradeNo: "s2a_order1", MerchantRef: "2002"})
	if !errors.Is(err, core.ErrPaymentQueryUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteMessageCapped(t *testing.T) {
	got := remoteMessage(strings.Repeat("错", 100)+testKey, testKey)
	if len(got) > maxRemoteMessage+len("…") || strings.Contains(got, testKey) {
		t.Fatalf("remoteMessage = %q", got)
	}
}
