package gateway

import (
	"context"
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/tokenizer"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// pluginPromptBytes caps the prompt text a plugin estimator is given (CONTRACTS
// §63): an estimate needs no more, and it keeps the EstimateUsage call small. It
// cuts a derived copy only; the request itself is not refused.
const pluginPromptBytes = 1 << 20

// localTokenizerEstimate is the fallback estimate when the plugin does not
// implement EstimateUsage (or the endpoint has none): the local tokenizer
// counts the prompt and the caller's pre-consume floor is kept.
func localTokenizerEstimate(ctx context.Context, prompt string, floor int64) (*pluginv1.UsageReport, error) {
	n, _, err := tokenizer.Count(ctx, prompt, "")
	if err != nil {
		return nil, err
	}
	return &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{InputTokens: max(n, floor)}}, nil
}

// precharge runs only after billing type admission and before scheduling.
// The plugin reports units; the core owns the price snapshot and ledger.
func (c *call) precharge(ctx context.Context) error {
	gate := c.g.d.Balance
	if gate == nil {
		return fmt.Errorf("billing precharger unavailable")
	}
	floor, err := gate.PreConsumeTokens(ctx)
	if err != nil {
		return err
	}
	fields := usageRequestFields(c.ep.UsageRequestFields, c.body)
	var report *pluginv1.UsageReport
	if pb, exists := c.gen.Platform(c.platform); exists && pb.Client != nil {
		// The local tokenizer counts the prompt up to tokenizer.MaxTextBytes.
		prompt := c.promptText(tokenizer.MaxTextBytes)
		in := &pluginv1.EstimateUsageRequest{Meta: c.meta(), Fields: fields, PreConsumeTokens: floor, Prompt: truncateUTF8(prompt, pluginPromptBytes)}
		estimator, ok := pb.Client.(interface {
			EstimateUsage(context.Context, *pluginv1.EstimateUsageRequest) (*pluginv1.UsageReport, error)
		})
		if !ok {
			return fmt.Errorf("plugin does not implement EstimateUsage")
		}
		estctx, cancel := context.WithTimeout(ctx, c.gw.hotpathTimeout())
		defer cancel()
		report, err = estimator.EstimateUsage(estctx, in)
		if err != nil {
			// Handle Unimplemented: non-task.submit endpoints fall back to local tokenizer
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
				if c.ep.TaskSubmit() {
					return fmt.Errorf("task estimator unavailable: %w", err)
				}
				// Fall back to local tokenizer for non-task endpoints
				report, err = localTokenizerEstimate(ctx, prompt, floor)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}
	} else {
		if c.ep.TaskSubmit() {
			return fmt.Errorf("task estimator unavailable")
		}
		// Built-in platforms count locally; run() may already have done it
		// off the request path (prompt_count.go).
		report, err = c.localPromptEstimate(ctx, floor)
		if err != nil {
			return err
		}
	}
	if report == nil || report.Reserve != nil {
		return fmt.Errorf("invalid pre-upstream estimate")
	}
	t := report.GetTokens()
	for _, n := range []int64{t.GetInputTokens(), t.GetOutputTokens(), t.GetCacheReadTokens(), t.GetCacheCreationTokens(), t.GetCacheCreation_1HTokens()} {
		if n < 0 || n > 1_000_000_000_000 {
			return fmt.Errorf("invalid estimated token count")
		}
	}

	// Reserve output tokens to prevent concurrent overdraft (CONTRACTS §25.8)
	outputReserve := int64(0)
	if bg, ok := gate.(interface {
		OutputReserveTokens(context.Context) (int64, error)
	}); ok {
		if reserve, err := bg.OutputReserveTokens(ctx); err == nil && reserve > 0 {
			outputReserve = reserve
		}
	}

	rec := *c.rec
	rec.Tokens = usagerules.Tokens(
		t.GetInputTokens(),
		max(t.GetOutputTokens(), outputReserve),
		t.GetCacheReadTokens(),
		t.GetCacheCreationTokens(),
		t.GetCacheCreation_1HTokens(),
	)
	rules := c.pf.Usage
	if c.ep.Usage != nil {
		rules = *c.ep.Usage
	}
	rec.UsageSemantics = rules.Semantics
	rec.Additional = c.estimatedAdditional(rec.Tokens, rules.Semantics)
	rec.Metrics = map[string]any{}
	for key, raw := range report.GetFacts() {
		fact, ok := rules.Facts[key]
		if !ok {
			return fmt.Errorf("undeclared estimated fact %q", key)
		}
		value, ok := usagerules.Fact(fact, raw)
		if !ok {
			return fmt.Errorf("invalid estimated fact %q", key)
		}
		if number, ok := value.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
			return fmt.Errorf("non-finite estimated fact %q", key)
		}
		rec.Metrics[key] = value
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := gate.Precharge(pctx, &rec); err != nil {
		return err
	}
	c.precharged = &prechargeEstimate{tokens: rec.Tokens, semantics: rec.UsageSemantics, additional: rec.Additional, metrics: rec.Metrics}
	return nil
}

// prechargeEstimate is what precharge reserved money for: the usage a client
// that hangs up before the usage event is billed instead (CONTRACTS §25.8).
type prechargeEstimate struct {
	tokens     core.UsageTokens
	semantics  string
	additional []core.PricedUsage
	metrics    map[string]any
}
