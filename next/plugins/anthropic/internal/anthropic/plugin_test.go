package anthropic

import (
	"slices"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

func start(t *testing.T) *pluginsdktest.Harness {
	t.Helper()
	return pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithInfo("anthropic", "0.3.1")}})
}

// TestCapabilities: the process only serves the model catalog; the account
// type's upstream is the core's (manifest accountTypes[].upstream).
func TestCapabilities(t *testing.T) {
	if got := start(t).Info.GetCapabilities(); !slices.Equal(got, []string{manifest.CapHTTPRoutes}) {
		t.Fatalf("capabilities = %v", got)
	}
}
