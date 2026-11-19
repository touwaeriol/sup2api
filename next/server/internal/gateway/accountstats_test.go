package gateway

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type panicStatsLimiter struct{ core.AccountLimiter }

func (panicStatsLimiter) TryHit(context.Context, core.AccountRef, string, string) (bool, error) {
	panic("admission panic")
}

// Usage reports nothing: the balancing reads it before the admission.
func (panicStatsLimiter) Usage(context.Context, []core.AccountRef) (map[int64]core.RateUsage, error) {
	return nil, nil
}

func TestAccountStatsAdmissionPanicReleasesSlots(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		e := newEnv(t)
		e.gw.d.Limiter = panicStatsLimiter{}
		raw, err := json.Marshal(body(testModel, false))
		require.NoError(t, err)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(raw)))
		c.Request.Header.Set("x-api-key", testKey)
		require.Panics(t, func() { e.gw.Middleware()(c) })
		n, _ := e.slots.InUse(context.Background(), "account", 1)
		require.Zero(t, n)
		n, _ = e.slots.InUse(context.Background(), core.KeySlots, 5)
		require.Zero(t, n)
	})
	t.Run("websocket", func(t *testing.T) {
		e := newWSEnv(t)
		e.gw.d.Limiter = panicStatsLimiter{}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
		pf := builtinPlatform(t, "openai")
		base := &call{g: e.gw, c: c, gen: e.gen, ep: endpointOf(t, pf, "openai.responses_ws"), platform: "openai", pf: pf,
			principal: e.auth.keys[testKey], gw: defaultGatewaySettings(), autoDisable: defaultAutoDisableSettings()}
		s := &wsSession{g: e.gw, base: base, id: "panic-session"}
		raw, err := json.Marshal(create(wsModel, "hello"))
		require.NoError(t, err)
		require.Panics(t, func() { _, _ = s.begin(context.Background(), wsMessage{data: raw}) })
		n, _ := e.slots.InUse(context.Background(), "account", 11)
		require.Zero(t, n)
		n, _ = e.slots.InUse(context.Background(), core.KeySlots, 5)
		require.Zero(t, n)
	})
}

func TestAccountStatsTokenSources(t *testing.T) {
	for _, tc := range []struct {
		name    string
		record  core.UsageRecord
		tokens  int64
		pending bool
	}{
		{"primary", core.UsageRecord{Tokens: core.UsageTokens{Input: 5, Output: 3, CacheRead: 2}}, 10, false},
		{"replacement and additional", core.UsageRecord{Tokens: core.UsageTokens{Input: 999}, Replacement: []core.PricedUsage{{Tokens: core.UsageTokens{Input: 7, Output: 4}}, {Tokens: core.UsageTokens{CacheRead: 3}}}, Additional: []core.PricedUsage{{Tokens: core.UsageTokens{Input: 2}}}}, 16, false},
		{"overflow", core.UsageRecord{Tokens: core.UsageTokens{Input: math.MaxInt64, Output: math.MaxInt64, CacheRead: 20}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &statsCapture{ch: make(chan accountStatsSample, 1)}
			id := int64(1)
			tc.record.AccountID, tc.record.RequestID, tc.record.Success = &id, "tokens", true
			c := &call{g: &Gateway{d: Deps{Observations: capture}, now: time.Now}, statsCompletedAt: time.Now()}
			c.observeRequest(observation.UsageFinalized, &tc.record)
			v := capture.next(t)
			require.Equal(t, tc.tokens, v.Tokens)
			require.Equal(t, tc.pending, v.UsagePending)
		})
	}
}

type statsCapture struct{ ch chan accountStatsSample }

type accountStatsSample struct {
	RequestID    string
	AccountID    int64
	CompletedAt  time.Time
	Tokens       int64
	Success      bool
	Canceled     bool
	UsagePending bool
}

func (s *statsCapture) PublishObservation(e *pluginv1.RequestObservation) {
	if e.Phase != observation.UsageFinalized {
		return
	}
	s.ch <- accountStatsSample{RequestID: e.RequestId, AccountID: e.AccountId, CompletedAt: time.UnixMilli(e.CompletedAtUnixMs), Tokens: e.TotalTokens, Success: e.Success, Canceled: e.Canceled, UsagePending: e.UsagePending}
}

func (s *statsCapture) next(t *testing.T) accountStatsSample {
	t.Helper()
	select {
	case v := <-s.ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("missing account statistics")
		return accountStatsSample{}
	}
}

func TestAccountStatsSuccessfulRequestsAndRelease(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		stream, broken bool
		account        int64
		success        bool
	}{
		{"json", 0, false, false, 1, true},
		{"sse", 0, true, false, 1, true},
		{"failover", 429, false, false, 2, true},
		{"rejected", 400, false, false, 1, false},
		{"broken stream", 0, true, true, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			capture := &statsCapture{ch: make(chan accountStatsSample, 8)}
			e.gw.d.Observations = capture
			e.up.set("acc-1", &upstreamRule{status: tc.status, breakStream: tc.broken})
			e.messages(body(testModel, tc.stream))
			r := e.record()
			v := capture.next(t)
			require.Equal(t, tc.success, v.Success)
			require.Equal(t, tc.account, v.AccountID)
			require.Equal(t, r.RequestID, v.RequestID)
			require.Equal(t, r.Tokens.Total(), v.Tokens)
			require.NotZero(t, v.CompletedAt)
			for _, id := range []int64{1, 2, 3} {
				n, err := e.slots.InUse(context.Background(), "account", id)
				require.NoError(t, err)
				require.Zero(t, n)
			}
			n, _ := e.slots.InUse(context.Background(), core.KeySlots, 5)
			require.Zero(t, n)
			require.Empty(t, capture.ch)
		})
	}
}

// A relay that waits commits its usage through the execution records, not
// the settler's Submit: the account statistics see the request once, with
// the plugin's usage.
func TestAccountStatsWaitedRelay(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	capture := &statsCapture{ch: make(chan accountStatsSample, 8)}
	e.gw.d.Observations = capture
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		return report(321, 123), nil
	}
	r := e.do("/pu/v1/chat", body(testModel, false), nil)
	require.Equal(t, http.StatusOK, r.status)
	e.record()
	v := capture.next(t)
	require.True(t, v.Success)
	require.EqualValues(t, 444, v.Tokens)
	require.EqualValues(t, 9, v.AccountID)
	require.Equal(t, []string{core.ExecutionStateCommitted}, e.settler.executionStates())
	require.Empty(t, capture.ch)
}

func TestAccountStatsWebSocketPerTurn(t *testing.T) {
	e := newWSEnv(t)
	capture := &statsCapture{ch: make(chan accountStatsSample, 8)}
	e.gw.d.Observations = capture
	c := e.mustDial()
	ids := map[string]bool{}
	for range 2 {
		c.send(create(wsModel, "hello"))
		c.until("response.completed")
		r := e.record()
		v := capture.next(t)
		require.True(t, v.Success)
		require.Equal(t, r.Tokens.Total(), v.Tokens)
		require.False(t, ids[v.RequestID])
		ids[v.RequestID] = true
		n, _ := e.slots.InUse(context.Background(), "account", 11)
		require.Zero(t, n)
	}
}
