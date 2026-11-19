package dbschema

import (
	"context"
	"net"
	"net/url"
	"strconv"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg/pgproxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/egress"
)

// tunnelDSN is the DSN of a plugin behind the egress tunnel: the virtual
// database address, no password. The user and database name only satisfy
// the client's DSN parser; the core logs in with its own configuration.
func tunnelDSN(schema string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User("plugin"),
		Host:     net.JoinHostPort(egress.DatabaseHost, strconv.Itoa(egress.DatabasePort)),
		Path:     "/" + schema,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// ServeTunnel serves one connection of plugin pluginKey to the virtual
// database address (egress.Options.DatabaseProxy): it ensures the plugin's
// schema, logs in to PostgreSQL with the core's configuration and the
// schema as search_path, and relays the protocol (pgproxy).
func (m *Manager) ServeTunnel(ctx context.Context, conn net.Conn, pluginKey string) {
	m.proxyOnce.Do(func() { m.proxy, m.proxyErr = pgproxy.New(m.databaseURL) })
	err := m.proxyErr
	if err == nil {
		err = m.Ensure(ctx, pluginKey)
	}
	if err != nil {
		_ = conn.Close()
		m.log.Error("plugin database connection", "plugin", pluginKey, "err", err)
		return
	}
	schema := SchemaName(pluginKey)
	err = m.proxy.Serve(ctx, conn, pgproxy.Session{Client: pluginKey, Params: map[string]string{
		"search_path":      schema,
		"application_name": "sub2api plugin " + pluginKey,
	}})
	if err != nil && ctx.Err() == nil {
		m.log.Warn("plugin database connection", "plugin", pluginKey, "err", err)
	}
}
