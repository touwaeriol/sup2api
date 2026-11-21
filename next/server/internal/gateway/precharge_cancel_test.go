package gateway

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// A client that hangs up before the usage event pays the pre-upstream
// estimate (CONTRACTS §25.8); every other outcome is billed as before.
func TestFinalizeBillabilityCanceledStreamBillsPrecharge(t *testing.T) {
	est := &prechargeEstimate{tokens: core.UsageTokens{Input: 120, Output: 4000}, semantics: "exclusive"}
	perToken := &core.PriceRule{Mode: "visual"}
	canceled := func() *core.UsageRecord {
		return &core.UsageRecord{ErrorType: errTypeClientCanceled, StatusCode: statusClientClosed, Price: perToken}
	}

	t.Run("cut before any usage", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true}
		rec := canceled()
		c.finalizeBillability(context.Background(), rec, "paid")
		if !rec.Billable || rec.Price == nil {
			t.Fatalf("not billable: %+v", rec)
		}
		if rec.Tokens != est.tokens || rec.UsageSemantics != "exclusive" || rec.UsageExtract != core.UsageExtractPrecharge {
			t.Fatalf("tokens = %+v semantics = %q extract = %q", rec.Tokens, rec.UsageSemantics, rec.UsageExtract)
		}
	})
	t.Run("cut after message_start keeps the reported prompt", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true}
		rec := canceled()
		rec.Tokens = core.UsageTokens{Input: 150, Output: 1, CacheRead: 50}
		rec.UsageSemantics = "inclusive"
		c.finalizeBillability(context.Background(), rec, "paid")
		want := core.UsageTokens{Input: 150, Output: 4000, CacheRead: 50}
		if !rec.Billable || rec.Tokens != want || rec.UsageSemantics != "inclusive" {
			t.Fatalf("billable = %v tokens = %+v semantics = %q", rec.Billable, rec.Tokens, rec.UsageSemantics)
		}
	})
	t.Run("cached prompt is not billed twice", func(t *testing.T) {
		// The estimate holds the whole prompt as input; message_start said
		// most of it was a 1h cache write. Input excludes cache here, so a
		// field-wise maximum would charge the prompt as input and as cache.
		c := &call{precharged: &prechargeEstimate{tokens: core.UsageTokens{Input: 100000, Output: 4000}, semantics: "exclusive"}, upstreamResponded: true}
		rec := canceled()
		rec.Tokens = core.UsageTokens{Input: 10, Output: 1, CacheRead: 20, CacheCreation: 30, CacheCreation1h: 99940}
		rec.UsageSemantics = "exclusive"
		c.finalizeBillability(context.Background(), rec, "paid")
		want := core.UsageTokens{Input: 10, Output: 4000, CacheRead: 20, CacheCreation: 30, CacheCreation1h: 99940}
		if !rec.Billable || rec.Tokens != want || rec.UsageExtract != core.UsageExtractPrecharge {
			t.Fatalf("billable = %v tokens = %+v extract = %q", rec.Billable, rec.Tokens, rec.UsageExtract)
		}
	})
	t.Run("read to its end: billed as reported", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true, drained: true}
		rec := canceled()
		rec.Tokens = core.UsageTokens{Input: 10, Output: 7, CacheRead: 50}
		c.finalizeBillability(context.Background(), rec, "paid")
		if !rec.Billable || rec.Tokens != (core.UsageTokens{Input: 10, Output: 7, CacheRead: 50}) || rec.UsageExtract != "" {
			t.Fatalf("billable = %v tokens = %+v extract = %q", rec.Billable, rec.Tokens, rec.UsageExtract)
		}
	})
	t.Run("read to its end: per-request price is owed", func(t *testing.T) {
		c := &call{upstreamResponded: true, drained: true}
		rec := canceled()
		rec.Price = &core.PriceRule{Mode: "visual", PerRequest: true}
		c.finalizeBillability(context.Background(), rec, "paid")
		if !rec.Billable {
			t.Fatalf("not billable: %+v", rec)
		}
	})
	t.Run("free policy bills nothing", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true, disconnect: core.DisconnectFree}
		rec := canceled()
		rec.Tokens = core.UsageTokens{Input: 150, CacheRead: 50}
		c.finalizeBillability(context.Background(), rec, "paid")
		if rec.Billable || rec.Price != nil || rec.UsageExtract != "" {
			t.Fatalf("billed under the free policy: %+v", rec)
		}
		if rec.Tokens != (core.UsageTokens{Input: 150, CacheRead: 50}) {
			t.Fatalf("reported usage was rewritten: %+v", rec.Tokens)
		}
		// A plugin's own accounting is not the policy's to waive, also on a
		// record of the same request finalized after a waived one.
		settled := canceled()
		settled.Replacement = []core.PricedUsage{{Price: perToken, Tokens: core.UsageTokens{Input: 10}}}
		c.finalizeBillability(context.Background(), settled, "paid")
		if !settled.Billable {
			t.Fatalf("the waiver of one record reached a plugin-settled one: %+v", settled)
		}
	})
	t.Run("free policy leaves a completed request alone", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true, disconnect: core.DisconnectFree}
		rec := &core.UsageRecord{Success: true, StatusCode: 200, Price: perToken, Tokens: core.UsageTokens{Input: 10, Output: 5}}
		c.finalizeBillability(context.Background(), rec, "paid")
		if !rec.Billable {
			t.Fatalf("a served request went free: %+v", rec)
		}
	})
	t.Run("per-request price is owed", func(t *testing.T) {
		c := &call{precharged: &prechargeEstimate{}, upstreamResponded: true}
		rec := canceled()
		rec.Price = &core.PriceRule{Mode: "visual", PerRequest: true}
		c.finalizeBillability(context.Background(), rec, "paid")
		if !rec.Billable {
			t.Fatalf("not billable: %+v", rec)
		}
	})
	t.Run("left while queueing: nothing consumed", func(t *testing.T) {
		c := &call{precharged: est}
		rec := canceled()
		c.finalizeBillability(context.Background(), rec, "paid")
		if rec.Billable || rec.Tokens != (core.UsageTokens{}) || rec.UsageExtract != "" {
			t.Fatalf("billed a request no upstream saw: %+v", rec)
		}
	})
	t.Run("free endpoint", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true}
		rec := canceled()
		c.finalizeBillability(context.Background(), rec, "free")
		if rec.Billable || rec.Price != nil {
			t.Fatalf("billed a free endpoint: %+v", rec)
		}
	})
	t.Run("upstream failure is not a hang-up", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true}
		rec := &core.UsageRecord{ErrorType: errTypeUpstream, StatusCode: 502, Price: perToken}
		c.finalizeBillability(context.Background(), rec, "paid")
		if rec.Billable || rec.Tokens != (core.UsageTokens{}) {
			t.Fatalf("billed an upstream failure: %+v", rec)
		}
	})
	t.Run("a plugin's own accounting stands", func(t *testing.T) {
		c := &call{precharged: est, upstreamResponded: true}
		rec := canceled()
		rec.Replacement = []core.PricedUsage{{Price: perToken, Tokens: core.UsageTokens{Input: 10}}}
		c.finalizeBillability(context.Background(), rec, "paid")
		if rec.Tokens != (core.UsageTokens{}) || rec.UsageExtract != "" {
			t.Fatalf("estimate overrode the plugin's attempts: %+v", rec)
		}
	})
}
