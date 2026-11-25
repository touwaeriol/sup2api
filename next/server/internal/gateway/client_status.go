package gateway

// replyStatus is the status a client gets for an upstream error answer: the
// plugin's client_status when it names an error status (CONTRACTS §139),
// else the upstream status. The failure policy matches the upstream status,
// so retries do not depend on it.
func replyStatus(pluginStatus, upstream int) int {
	if pluginStatus >= 400 && pluginStatus <= 599 {
		return pluginStatus
	}
	return upstream
}
