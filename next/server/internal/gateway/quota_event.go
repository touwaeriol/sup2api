package gateway

import "github.com/tidwall/gjson"

// observeQuotaEvent hands one upstream WebSocket message to the quota
// observer when the account type declares a quota event for its "type"
// (CONTRACTS §142), with the observer and window names of the quota headers
// (observeQuota). Types without quota events cost a nil check; the message is
// relayed either way and never disables the account.
func (g *Gateway) observeQuotaEvent(rt *typeRoute, accountID int64, msg []byte) {
	if g.d.Quota == nil || rt == nil {
		return
	}
	q := rt.binding.Type.Quota
	if q == nil || len(q.Events) == 0 {
		return
	}
	if ev := q.Event(gjson.GetBytes(msg, "type").String()); ev != nil {
		g.d.Quota.ObserveQuotaEvent(accountID, *ev, msg)
	}
}
