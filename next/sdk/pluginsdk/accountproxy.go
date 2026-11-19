package pluginsdk

import (
	"fmt"
	"io"
	"log/slog"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// AccountProxy is the proxy of an account as the host sends it
// (Account.proxy, CONTRACTS §124): the proxy the call's requests go out
// through. It is data: the host sends every request of the account and
// enforces this proxy, a plugin never connects through it itself, and the
// SDK offers no transport or dialer for it.
//
// It carries the password in clear and must stay out of logs and error
// messages; String, GoString, Format and LogValue redact it for the cases
// where it is formatted by accident. Read the proxy through AccountProxyOf
// rather than formatting the Account or AccountProxy message.
type AccountProxy struct {
	ID       int64
	Protocol string // http | https | socks5
	Host     string
	Port     int
	Username string
	Password string
}

// AccountProxyOf returns the proxy of acc; nil when the account has none
// (its requests go out directly).
func AccountProxyOf(acc *pluginv1.Account) *AccountProxy {
	p := acc.GetProxy()
	if p == nil {
		return nil
	}
	return &AccountProxy{ID: p.GetId(), Protocol: p.GetProtocol(), Host: p.GetHost(), Port: int(p.GetPort()),
		Username: p.GetUsername(), Password: p.GetPassword()}
}

// String describes the proxy without its password.
func (p AccountProxy) String() string {
	return fmt.Sprintf("pluginsdk.AccountProxy{id=%d protocol=%s host=%s port=%d username=%s password=%t}",
		p.ID, p.Protocol, p.Host, p.Port, p.Username, p.Password != "")
}

// GoString keeps %#v from printing the password.
func (p AccountProxy) GoString() string { return p.String() }

// Format keeps every verb (%d, %x ...) from printing the password.
func (p AccountProxy) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, p.String()) }

// LogValue keeps slog from printing the password.
func (p AccountProxy) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", p.ID), slog.String("protocol", p.Protocol), slog.String("host", p.Host),
		slog.Int("port", p.Port), slog.String("username", p.Username), slog.Bool("password", p.Password != ""))
}
