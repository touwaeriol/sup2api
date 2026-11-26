package payment

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// epaySigned is an epay notification query paying o, signed with key the
// way an epay merchant server signs it (computed here, apart from the
// gateway's own code).
func epaySigned(o *model.PaymentOrder, key string) url.Values {
	q := url.Values{"pid": {"1001"}, "out_trade_no": {o.OutTradeNo}, "trade_no": {"E" + o.OutTradeNo[4:20]},
		"type": {o.Method}, "money": {o.PayAmount.StringFixed(2)}, "trade_status": {"TRADE_SUCCESS"}}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + q.Get(k)
	}
	sum := md5.Sum([]byte(strings.Join(pairs, "&") + key))
	q.Set("sign", hex.EncodeToString(sum[:]))
	q.Set("sign_type", "MD5")
	return q
}

// The built-in epay end to end: the order gets epay's submit address, a
// notification signed with the merchant key credits it once, and one
// signed with another key is refused and recorded.
func TestEpayEndToEnd(t *testing.T) {
	f := newFixture(t)
	f.svc.builtin = builtinGateways(f.svc.storedSettings, &http.Client{})
	f.configure()
	uid := f.user("epay")
	o := f.order(uid, "10")
	require.True(t, strings.HasPrefix(o.PayURL, "https://epay.test/submit.php?"), o.PayURL)
	require.Equal(t, "1001", o.MerchantRef)

	forged := f.epayNotify(epaySigned(o, "other-key"))
	require.Equal(t, http.StatusBadRequest, forged.Code)
	require.Equal(t, "fail", forged.Body.String())
	for i := 0; i < 2; i++ {
		w := f.epayNotify(epaySigned(o, "epay-secret"))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "success", w.Body.String())
	}
	require.Equal(t, statusCompleted, *f.load(o.OutTradeNo).Status)
	sum, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
	require.Equal(t, "10", sum.String())
	var forgedEvents int64
	require.NoError(t, f.db.Gorm.Model(&model.PaymentEvent{}).Where("kind = ?", eventSignatureInvalid).Count(&forgedEvents).Error)
	require.EqualValues(t, 1, forgedEvents)
}

// epayNotify sends q to the epay notify route as epay does (GET).
func (f *fixture) epayNotify(q url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/payment/notify/epay?"+q.Encode(), nil))
	return w
}
