package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The request body limit is the gateway setting max_request_body_mb: 128 MB
// by default, 1 to 1024, and an unnormalised or out-of-range stored value
// still yields a usable limit.
func TestMaxRequestBodySetting(t *testing.T) {
	require.Equal(t, 128, defaultGatewaySettings().MaxRequestBodyMB)
	require.EqualValues(t, 128<<20, defaultGatewaySettings().MaxRequestBodyBytes())
	require.EqualValues(t, 128<<20, GatewaySettings{}.MaxRequestBodyBytes(), "unset reads as the default")
	require.EqualValues(t, 3<<20, GatewaySettings{MaxRequestBodyMB: 3}.MaxRequestBodyBytes())
	require.EqualValues(t, int64(1024)<<20, GatewaySettings{MaxRequestBodyMB: 1024}.MaxRequestBodyBytes())

	for stored, want := range map[int]int{0: 128, -5: 128, 1: 1, 64: 64, 1024: 1024, 4096: 1024} {
		require.Equal(t, want, GatewaySettings{MaxRequestBodyMB: stored}.normalized().MaxRequestBodyMB, "stored %d", stored)
	}

	i := func(v int) *int { return &v }
	for _, ok := range []int{1, 128, 1024} {
		require.Empty(t, (&gatewaySettingsInput{MaxRequestBodyMB: i(ok)}).validate(context.Background()), "%d", ok)
	}
	for _, bad := range []int{0, -1, 1025} {
		fe := (&gatewaySettingsInput{MaxRequestBodyMB: i(bad)}).validate(core.WithLocale(context.Background(), "zh"))
		require.Equal(t, []core.FieldError{{Field: "max_request_body_mb", Code: "out_of_range", Message: "取值范围为 1 到 1024"}}, fe, "%d", bad)
	}
	v := defaultGatewaySettings()
	(&gatewaySettingsInput{}).applyTo(&v)
	require.Equal(t, 128, v.MaxRequestBodyMB, "absent keeps the value")
	(&gatewaySettingsInput{MaxRequestBodyMB: i(256)}).applyTo(&v)
	require.Equal(t, 256, v.MaxRequestBodyMB)
}

// The setting alone decides: it lowers the default and raises it, and the
// answer above it is 413.
func TestRequestBodyLimitIsTheGatewaySetting(t *testing.T) {
	e := newEnv(t)
	big := body(testModel, false, "pad", strings.Repeat("x", 1<<20))
	r := e.messages(big)
	require.Equal(t, 200, r.status, "the 128 MB default: %s", r.body)
	e.record()
	require.EqualValues(t, 128<<20, e.gw.settings.get(context.Background()).gateway.MaxRequestBodyBytes())

	e.setSettings(GatewaySettings{MaxRequestBodyMB: 1}, defaultStickySettings())
	require.EqualValues(t, 1<<20, e.gw.settings.get(context.Background()).gateway.MaxRequestBodyBytes())
	r = e.messages(big)
	require.Equal(t, 413, r.status, "1 MB replaces the default: %s", r.body)
	require.Contains(t, string(r.body), "request_too_large")
	e.record()
	r = e.messages(body(testModel, false, "pad", strings.Repeat("x", 200)))
	require.Equal(t, 200, r.status, r.body)
	e.record()

	e.setSettings(GatewaySettings{MaxRequestBodyMB: 2}, defaultStickySettings())
	r = e.messages(big)
	require.Equal(t, 200, r.status, "2 MB lets it through: %s", r.body)
	e.record()
}

// A WebSocket message is bounded by the same setting: one above it closes
// the connection with 1009 (message too big).
func TestWebSocketMessageLimitIsTheGatewaySetting(t *testing.T) {
	e := newWSEnv(t)
	e.setSettings(GatewaySettings{MaxRequestBodyMB: 1}, defaultStickySettings())
	c := e.mustDial()
	c.send(create(wsModel, "hi"))
	c.until("response.completed")
	require.True(t, e.record().Success)
	c.send(create(wsModel, strings.Repeat("x", 1<<20)))
	require.Equal(t, websocket.StatusMessageTooBig, c.closed())
}

// A saved value is stored, reaches plugins' settings reads, and is the limit
// of this node at once; a node told by config:changed reads it too.
func TestMaxRequestBodySettingSavedAndPropagated(t *testing.T) {
	ctx := context.Background()
	e := newDBEnv(t)
	require.EqualValues(t, 128<<20, e.gw.settings.get(ctx).gateway.MaxRequestBodyBytes())
	code, res := e.api("GET", "/settings/gateway", nil)
	require.Equal(t, 200, code, res.Raw)
	require.EqualValues(t, 128, res.Get("data.max_request_body_mb").Int())

	require.EqualValues(t, 0, res.Get("data.max_request_body_safe_mb").Int(), "no cap on plugin memory")

	// A row holding nothing else (what migration 0107 creates) reads the
	// other fields as their defaults.
	_, err := e.db.SQL.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('gateway', '{"max_request_body_mb": 300}')`)
	require.NoError(t, err)
	e.gw.onConfigChanged(nil)
	code, res = e.api("GET", "/settings/gateway", nil)
	require.Equal(t, 200, code, res.Raw)
	require.EqualValues(t, 300, res.Get("data.max_request_body_mb").Int())
	require.EqualValues(t, 3, res.Get("data.max_attempts").Int())
	require.EqualValues(t, 300<<20, e.gw.settings.get(ctx).gateway.MaxRequestBodyBytes())

	code, res = e.api("PUT", "/settings/gateway", map[string]any{"max_request_body_mb": 1025})
	require.Equal(t, 400, code, res.Raw)
	require.Equal(t, "max_request_body_mb", res.Get("error.details.fields.0.field").String())
	require.Equal(t, "out_of_range", res.Get("error.details.fields.0.code").String())

	code, res = e.api("PUT", "/settings/gateway", map[string]any{"max_request_body_mb": 64})
	require.Equal(t, 200, code, res.Raw)
	require.EqualValues(t, 64, res.Get("data.max_request_body_mb").Int())
	require.EqualValues(t, 2000, res.Get("data.platform_call_timeout_ms").Int(), "other fields keep their values")
	require.EqualValues(t, 64<<20, e.gw.settings.get(ctx).gateway.MaxRequestBodyBytes(), "the saving node drops its cache")

	plugin, err := SettingsReaders(e.db)[protocol.SettingsGateway](ctx)
	require.NoError(t, err)
	require.Equal(t, 64, plugin.(protocol.GatewaySettings).MaxRequestBodyMB)

	// Another node saved 256: this one serves its cached value until the
	// broadcast (or the TTL) drops it.
	_, err = e.db.SQL.ExecContext(ctx, `UPDATE settings SET value = jsonb_set(value, '{max_request_body_mb}', '256') WHERE key = 'gateway'`)
	require.NoError(t, err)
	require.EqualValues(t, 64<<20, e.gw.settings.get(ctx).gateway.MaxRequestBodyBytes())
	e.gw.onConfigChanged([]byte(`{"key":"settings.gateway"}`))
	require.EqualValues(t, 256<<20, e.gw.settings.get(ctx).gateway.MaxRequestBodyBytes())
}
