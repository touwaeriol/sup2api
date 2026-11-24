// Package protocol holds the go-plugin wiring shared by the host runtime and
// the plugin SDK, so both sides agree on the handshake and plugin name.
package protocol

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

// ProtocolVersion is the plugin wire protocol of this SDK and core: the
// go-plugin handshake version and GetInfoResponse.protocol_version. It rises
// with every incompatible change of the plugin contract, so that a binary
// built for another protocol is refused when its process starts; hostCompat
// alone cannot do that for packages published before the change. Protocol 2
// is the single Execute path (docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2).
const ProtocolVersion = 2

// FirstCoreVersion is the first core release that speaks ProtocolVersion: a
// plugin built for it (hostCompat ">=FirstCoreVersion") loads on every core
// of this protocol. It moves with ProtocolVersion.
const FirstCoreVersion = "0.1.304"

// HostAPIVersion is sent in InitHostRequest.host_api_version (HostInfo). It
// is the runtime contract's, which the shell compares between core releases.
const HostAPIVersion = runtimecontract.HostAPIVersion

// OutdatedContractMessage is the one answer to a plugin package built for an
// older plugin contract, whatever gave it away: a handshake with another
// ProtocolVersion, or a manifest the check rejects with
// check.CodeOutdatedContract. hostVersion is the core's own version; the
// version to look for is FirstCoreVersion, whatever core of the protocol
// refuses. Upload, install and load all report exactly this.
func OutdatedContractMessage(pluginKey, pluginVersion, hostVersion string) string {
	return fmt.Sprintf(outdatedContractFormat, pluginKey, pluginVersion, hostVersion, ProtocolVersion, FirstCoreVersion)
}

const outdatedContractFormat = "plugin %s@%s was built for an older plugin protocol and cannot be loaded by this core (%s, plugin protocol %d); install a version built for core %s or later"

// GetUsersMaxIDs caps the user ids of one HostService.GetUsers call.
const GetUsersMaxIDs = 500

// Handshake is the go-plugin handshake. A binary started without the magic
// cookie prints a hint and exits.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "SUB2API_PLUGIN",
	MagicCookieValue: "sub2api-next-plugin-v1",
}

// PluginName is the single dispensed plugin; all services are registered on
// the same gRPC server.
const PluginName = "sub2api"

// GRPCPlugin implements plugin.GRPCPlugin for both sides.
//   - Plugin side: set Register; it registers the implemented services.
//   - Host side: leave Register nil; Dispense returns *Client.
type GRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	Register func(s *grpc.Server, broker *plugin.GRPCBroker) error
}

func (p *GRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	return p.Register(s, broker)
}

func (p *GRPCPlugin) GRPCClient(_ context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &Client{Conn: c, Broker: broker}, nil
}

// Client is what the host gets from Dispense(PluginName). Build typed
// service clients from Conn (pluginv1.NewPluginServiceClient(conn), ...).
type Client struct {
	Conn   *grpc.ClientConn
	Broker *plugin.GRPCBroker
}

// PluginMap returns the go-plugin map for ClientConfig/ServeConfig.
func PluginMap(p *GRPCPlugin) map[string]plugin.Plugin {
	return map[string]plugin.Plugin{PluginName: p}
}

// DatabaseHost and DatabasePort are the virtual address of a plugin's
// database, which the DSN of HostService.GetDSN names: the core serves it at
// the end of the plugin's egress tunnel, logged in as the plugin's database
// identity, strict network mode or not (CONTRACTS §130).
const (
	DatabaseHost = "db.sub2api"
	DatabasePort = 5432
)

// DatabaseAddress is DatabaseHost:DatabasePort, the address Host.DB dials
// through the tunnel.
var DatabaseAddress = net.JoinHostPort(DatabaseHost, strconv.Itoa(DatabasePort))

// Environment variables the host sets for every plugin process.
// EnvPluginKey holds the plugin id (manifest.PluginID, CONTRACTS §137): the
// bare key of an official plugin, "_<publisher>__<key>" of any other.
const (
	EnvPluginKey     = "SUB2API_PLUGIN_KEY"
	EnvPluginVersion = "SUB2API_PLUGIN_VERSION"
	EnvStrictNetwork = "SUB2API_PLUGIN_STRICT_NETWORK" // "1" when direct sockets are blocked
	EnvDataDir       = "SUB2API_PLUGIN_DATA_DIR"       // the plugin's data directory, set only when it is granted "fs.data" (CONTRACTS §43.8)
)
