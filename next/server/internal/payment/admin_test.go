package payment

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// A user with topup:use only reaches none of the administrator's routes.
func TestAdminRoutesNeedTheirPermissions(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("plain")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/topup/orders"}, {http.MethodGet, "/topup/orders/stats"}, {http.MethodGet, "/topup/orders/1/events"},
		{http.MethodGet, "/topup/settings"}, {http.MethodPut, "/topup/settings"}, {http.MethodGet, "/topup/gateways"},
		{http.MethodGet, "/promo-codes"}, {http.MethodPost, "/promo-codes"}, {http.MethodGet, "/promo-codes/1/usages"},
	} {
		require.Equal(t, http.StatusForbidden, f.call(uid, c.method, c.path, "{}").Code, c.path)
	}
	require.Equal(t, http.StatusOK, f.call(uid, http.MethodGet, "/me/topup", "").Code)
}

// The administrator sees an order's events, newest first, with the fields
// the core kept and nothing secret.
func TestOrderEvents(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("events"), "10")
	bad := paid(o, "")
	bad.Amount--
	bad.Fields = map[string]string{"type": "alipay"}
	require.Equal(t, http.StatusBadRequest, f.notify("epay", bad))
	require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "ev-ok")))
	w := f.call(f.admin, http.MethodGet, "/topup/orders/"+itoa(o.ID)+"/events", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Data []eventView
		Page struct{ Total int64 }
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 2)
	require.EqualValues(t, 2, out.Page.Total)
	ok, mismatch := out.Data[0], out.Data[1]
	require.Equal(t, eventNotifyOK, ok.Kind)
	require.Equal(t, "ev-ok", ok.EventID)
	require.True(t, ok.Verified)
	require.Equal(t, eventAmountMismatch, mismatch.Kind)
	require.Equal(t, "alipay", mismatch.Detail["type"])
	require.Equal(t, o.OutTradeNo, mismatch.Detail["out_trade_no"])
	require.NotContains(t, w.Body.String(), "epay-secret")
}
