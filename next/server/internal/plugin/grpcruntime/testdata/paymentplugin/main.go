// Command paymentplugin runs the simulated payment gateway of the SDK
// (pluginsdktest/mockpay) as a plugin process, for the runtime tests of the
// plugin gateway path. It is a test fixture, never packaged or published.
package main

import (
	"os"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/mockpay"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// main takes its identity from the host, like testplugin: the test
// packages get random keys.
func main() {
	_, key, _ := manifest.ParsePluginID(os.Getenv(protocol.EnvPluginKey))
	pluginsdk.Serve(mockpay.New(), pluginsdk.WithInfo(key, os.Getenv(protocol.EnvPluginVersion)))
}
