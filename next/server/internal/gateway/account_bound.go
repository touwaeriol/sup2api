package gateway

// attemptHeaders is ExecuteRequest.inbound_headers for an attempt on
// account id: the client headers rt hands the plugin (passHeaders), less
// the account-bound ones (manifest accountBoundHeaders, CONTRACTS §141.4)
// on any other account than the owner's. The owner is the account the
// request first built an attempt for, or the one a WebSocket session's
// earlier turns were on (wsSession.owner); this call records it when there
// is none yet. A retry on the owner (a failure policy's, a 401 recovery)
// keeps them.
func (c *call) attemptHeaders(rt *typeRoute, id int64) map[string]string {
	h := c.passHeaders(rt.passHeaders)
	if c.firstAccount == 0 {
		c.firstAccount = id
	}
	if id != c.firstAccount {
		for _, name := range rt.boundHeaders {
			delete(h, headerLower(name))
		}
	}
	return h
}
