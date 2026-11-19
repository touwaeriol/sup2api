// Package netguard is the core's single answer to "may the server connect
// there?" - the SSRF guard used by every outbound path: the gateway's
// upstream requests, the reconcile loop, account tests, proxies, price sync
// sources and the plugin egress tunnel.
//
// It lives in its own package because having two of these is how one of them
// drifts, and the drift is always silent: the weaker copy keeps working, on
// exactly the requests it should have refused.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	// ErrBadURL is an address that is not an absolute http(s) URL.
	ErrBadURL = errors.New("upstream url must be an absolute http(s) url")
	// ErrPrivate is an address that resolves somewhere inside the
	// deployment's own network.
	ErrPrivate = errors.New("upstream address is private, loopback or link-local")
)

// Lookup resolves a host name. Callers pass DefaultLookup outside tests.
type Lookup func(ctx context.Context, host string) ([]net.IP, error)

// DefaultLookup resolves through the process resolver.
func DefaultLookup(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

// CheckURL validates a URL the server connects to directly: CheckURLVia
// without a proxy.
func CheckURL(ctx context.Context, raw string, allowPrivate bool, lookup Lookup) (*url.URL, error) {
	return CheckURLVia(ctx, raw, allowPrivate, false, lookup)
}

// ProxyFunc picks the proxy of a request as http.Transport.Proxy does: nil
// is a direct connection.
type ProxyFunc func(*url.URL) (*url.URL, error)

// CheckURLProxy is CheckURLVia for a request whose proxy, if any, proxy
// picks (nil: always direct).
func CheckURLProxy(ctx context.Context, raw string, allowPrivate bool, proxy ProxyFunc, lookup Lookup) (*url.URL, error) {
	proxied := false
	if proxy != nil {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, ErrBadURL
		}
		p, err := proxy(u)
		if err != nil {
			return nil, err
		}
		proxied = p != nil
	}
	return CheckURLVia(ctx, raw, allowPrivate, proxied, lookup)
}

// CheckURLVia validates a URL a plugin returned: http(s), a host, no
// embedded credentials, and - unless allowPrivate - a public target as
// CheckHostVia judges it for a connection made directly or, when proxied,
// through a proxy.
func CheckURLVia(ctx context.Context, raw string, allowPrivate, proxied bool, lookup Lookup) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return nil, ErrBadURL
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in url", ErrBadURL)
	}
	if allowPrivate {
		return u, nil
	}
	if err := CheckHostVia(ctx, u.Hostname(), proxied, lookup); err != nil {
		return nil, err
	}
	return u, nil
}

// CheckHostVia is the one rule for an upstream host: a direct connection
// checks the addresses the name resolves to (CheckHost); a proxied one is
// resolved by the proxy, which receives the name, so only a literal private
// address or a localhost name is refused and nothing is resolved locally
// (the local resolver neither decides nor learns the name).
func CheckHostVia(ctx context.Context, host string, proxied bool, lookup Lookup) error {
	if !proxied {
		return CheckHost(ctx, host, lookup)
	}
	if LiteralPrivate(host) {
		return ErrPrivate
	}
	return nil
}

// CheckHost reports ErrPrivate when host (an IP literal or a name) is or
// resolves to a non-public address. A name that cannot be resolved is an
// error too: nothing unverified is let through.
func CheckHost(ctx context.Context, host string, lookup Lookup) error {
	host = normalizeHost(host)
	if LiteralPrivate(host) {
		return ErrPrivate
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if lookup == nil {
		lookup = DefaultLookup
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		a, ok := netip.AddrFromSlice(ip)
		if !ok || BlockedAddr(a) {
			return ErrPrivate
		}
	}
	return nil
}

// LiteralPrivate reports, without resolving anything, whether host is a
// non-public IP literal or a localhost name (or empty). Use it where a DNS
// lookup is not wanted (saving a setting); the dial-time DialControl is what
// catches names that resolve inwards.
func LiteralPrivate(host string) bool {
	host = normalizeHost(host)
	if ip, err := netip.ParseAddr(host); err == nil {
		return BlockedAddr(ip)
	}
	lower := strings.ToLower(host)
	return lower == "" || lower == "localhost" || strings.HasSuffix(lower, ".localhost")
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), ".")
}

// blockedPrefixes are non-public ranges the netip predicates in BlockedAddr
// do not cover. It is the union of the two lists that used to exist (this
// package and proxy/dialguard.go).
var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8",       // "this network"; 0.x reaches local services on Linux
	"100.64.0.0/10",   // carrier-grade NAT (RFC 6598)
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.18.0.0/15",   // benchmarking (RFC 2544)
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes 255.255.255.255
	"::/96",           // unspecified, loopback and IPv4-compatible (can embed private IPv4)
	"64:ff9b::/96",    // NAT64 well-known prefix (embeds IPv4)
	"64:ff9b:1::/48",  // local-use NAT64 (RFC 8215)
	"100::/64",        // discard-only
	"2001::/32",       // Teredo (embeds IPv4)
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4 (embeds IPv4)
	"fc00::/7",        // unique local
	"fe80::/10",       // link-local
	"fec0::/10",       // deprecated site-local
	"ff00::/8",        // multicast
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// BlockedAddr reports whether an address belongs to the deployment's own
// network (or a reserved range) rather than the public internet. Invalid
// addresses are blocked.
func BlockedAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
