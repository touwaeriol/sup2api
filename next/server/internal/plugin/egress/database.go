package egress

import (
	"context"
	"net"
)

// DatabaseHost/DatabasePort is the virtual address a plugin's database
// connections go to (the DSN of HostService.GetDSN under strict network).
// The core logs in to PostgreSQL for the plugin and relays the protocol
// (Options.DatabaseProxy, CONTRACTS §115), so the plugin holds no database
// credential.
const (
	DatabaseHost = "db.sub2api"
	DatabasePort = 5432
)

// serveDatabase hands one tunnelled connection to the database proxy and
// returns the end the tunnel pumps.
func (s *server) serveDatabase(ctx context.Context) net.Conn {
	tunnel, proxy := net.Pipe()
	go s.p.opts.DatabaseProxy(ctx, proxy, s.key)
	return tunnel
}
