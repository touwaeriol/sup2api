package manifest

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

// UpstreamHostAllowed decides whether a request the host sends for a plugin
// with the credentials of an account of type accountType may go to host,
// the host name of its URL (any case, without port): the relay of an
// Execute call (HTTP or a WebSocket handshake), a fetch of an Execute or
// Monitor call, and HostService.SendAccountRequest (CONTRACTS §112.17). It
// may when host is one of the manifest's externalServices or a subdomain of
// one (ExternalServiceHost), which the operator reviewed at install, or the
// host of one of the account's own upstream addresses (AccountType.
// AddressHosts of accountDocs, the account's settings and credentials
// JSON), which the operator entered: an account pointed at a relay must be
// able to reach it. Whether the address may be reached at all (the SSRF
// policy, the account's proxy) is not this decision's. The host and
// pluginsdktest's fake host both decide with it.
func (m *Manifest) UpstreamHostAllowed(host, accountType string, accountDocs ...string) bool {
	if m == nil || host == "" {
		return false
	}
	host = strings.ToLower(host)
	if ExternalServiceHost(m.ExternalServices, host) {
		return true
	}
	i := slices.IndexFunc(m.AccountTypes, func(at AccountType) bool { return at.ID == accountType })
	return i >= 0 && slices.Contains(m.AccountTypes[i].AddressHosts(accountDocs...), host)
}

// ExternalServiceHost reports whether host (lower-case, without port) is one
// of services or a subdomain of one. An entry is compared without case, and
// one written with a port matches its host on any port.
func ExternalServiceHost(services []string, host string) bool {
	return slices.ContainsFunc(services, func(svc string) bool {
		svc = strings.ToLower((&url.URL{Host: strings.TrimSpace(svc)}).Hostname())
		return svc != "" && (host == svc || strings.HasSuffix(host, "."+svc))
	})
}

// AddressHosts returns the lower-case host names of the account's upstream
// addresses: the string values of the type's address fields (AddressFields)
// in docs, JSON objects such as the account's settings and credentials. An
// address is a URL or a bare host[:port]. A type that declares no address
// fields has none.
func (at AccountType) AddressHosts(docs ...string) []string {
	fields, _ := at.AddressFields()
	if len(fields) == 0 {
		return nil
	}
	var hosts []string
	for _, doc := range docs {
		var values map[string]any
		if json.Unmarshal([]byte(doc), &values) != nil {
			continue
		}
		for _, f := range fields {
			address, _ := values[f].(string)
			if host := addressHost(address); host != "" {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

// addressHost is the lower-case host name of an upstream address, which is
// a URL or a bare host[:port]; "" when it names none.
func addressHost(address string) string {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "//" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
