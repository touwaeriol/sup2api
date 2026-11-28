package gateway

// inboundHeaders is ExecuteRequest.inbound_headers for attempt n on account
// id: the client headers rt hands the plugin (passHeaders), less the
// account-bound ones (manifest accountBoundHeaders, CONTRACTS §141.4) once
// the request is on another account than its first attempt's. A retry on
// the same account (a failure policy's, a 401 recovery) keeps them.
func (c *call) inboundHeaders(rt *typeRoute, id int64, n int) map[string]string {
	h := c.passHeaders(rt.passHeaders)
	if n == 0 {
		c.firstAccount = id
		return h
	}
	if id != c.firstAccount {
		for _, name := range rt.boundHeaders {
			delete(h, headerLower(name))
		}
	}
	return h
}
