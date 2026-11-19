package pluginsdk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Settings reads a section from the host once, keeps it in the process and
// reads it again after the host says it changed; a change of another section
// keeps it.
func TestSettingsCachedUntilChanged(t *testing.T) {
	ctx := context.Background()
	fh := pluginsdktest.NewFakeHost()
	fh.SetSettings(protocol.StickySettings{Enabled: true, DefaultTTLSeconds: 3600})
	fh.SetSettings(protocol.BillingSettings{MinBalance: "0"})
	p := &locker{}
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})

	sticky := func() protocol.StickySettings {
		t.Helper()
		v, err := pluginsdk.Settings[protocol.StickySettings](ctx, p.host)
		require.NoError(t, err)
		return v
	}
	require.True(t, sticky().Enabled)
	require.True(t, sticky().Enabled)
	require.Equal(t, 1, fh.SettingsReads())

	fh.SetSettings(protocol.StickySettings{Enabled: false, DefaultTTLSeconds: 60})
	require.NoError(t, h.SettingsChanged(protocol.SettingsBilling))
	require.True(t, sticky().Enabled, "a change of another section keeps the cached one")
	require.Equal(t, 1, fh.SettingsReads())

	require.NoError(t, h.SettingsChanged(protocol.SettingsSticky))
	require.Equal(t, protocol.StickySettings{DefaultTTLSeconds: 60}, sticky())
	require.Equal(t, 2, fh.SettingsReads())

	billing, err := pluginsdk.Settings[protocol.BillingSettings](ctx, p.host)
	require.NoError(t, err)
	require.Equal(t, "0", billing.MinBalance)
	require.NoError(t, h.SettingsChanged())
	sticky()
	require.Equal(t, 4, fh.SettingsReads(), "a change without sections drops every section")
}

// A host error is returned as is while no read succeeded, and the host is
// not asked again until it says the section changed (or settingsErrorTTL).
func TestSettingsHostError(t *testing.T) {
	ctx := context.Background()
	fh := pluginsdktest.NewFakeHost()
	p := &locker{}
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})

	_, err := pluginsdk.Settings[protocol.FailurePolicySettings](ctx, p.host)
	require.Equal(t, codes.Unavailable, status.Code(err))
	fh.SetSettings(protocol.FailurePolicySettings{Configured: true, MaxRetries: 2})
	_, err = pluginsdk.Settings[protocol.FailurePolicySettings](ctx, p.host)
	require.Equal(t, codes.Unavailable, status.Code(err), "held back after a failure")
	require.NoError(t, h.SettingsChanged(protocol.SettingsFailurePolicy))
	v, err := pluginsdk.Settings[protocol.FailurePolicySettings](ctx, p.host)
	require.NoError(t, err)
	require.Equal(t, 2, v.MaxRetries)

	_, err = pluginsdk.Settings[protocol.StickySettings](ctx, nil)
	require.ErrorIs(t, err, pluginsdk.ErrHostNotReady)
}
