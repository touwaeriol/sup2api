package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// pending is everything needed to settle one usage row.
type pending struct {
	RequestID string
	UserID    int64
	APIKeyID  int64
	GroupID   int64
	AccountID *int64
	PluginKey string
	Platform  string
	Protocol  string
	// AccountType and UpstreamProtocol are the account type (declared by
	// PluginKey) and the protocol sent upstream (ARCHITECTURE 6.6).
	AccountType      string
	UpstreamProtocol string
	Model            string
	Success          bool
	StatusCode       int
	ErrorType        string
	Tokens           core.UsageTokens
	Metrics          map[string]any
	LatencyMs        int
	CreatedAt        time.Time
	Rate             decimal.Decimal

	PriceID    int64
	Mode       string
	Expression string
	Inputs     pendingInputs
	Attempts   int
}

func fromRecord(rec *core.UsageRecord) *pending {
	p := &pending{
		RequestID: trunc(rec.RequestID, 64), UserID: rec.UserID, APIKeyID: rec.APIKeyID, GroupID: rec.GroupID,
		AccountID: rec.AccountID, PluginKey: rec.PluginKey, Platform: rec.Platform, Protocol: rec.Protocol,
		AccountType: trunc(rec.AccountType, 50), UpstreamProtocol: trunc(rec.UpstreamProtocol, 100),
		Model: rec.Model, Success: rec.Success, StatusCode: rec.StatusCode, ErrorType: rec.ErrorType,
		Tokens: rec.Tokens, Metrics: core.CloneUsageMetrics(rec.Metrics), LatencyMs: rec.LatencyMs, CreatedAt: rec.CreatedAt,
		Rate:   rec.RateMultiplier,
		Inputs: recordInputs(rec),
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if rec.Price != nil {
		p.PriceID, p.Mode, p.Expression = rec.Price.ID, rec.Price.Mode, rec.Price.Expression
	}
	return p
}

// frozenRule is the price rule a row was priced by, as frozen in its inputs
// (core.PricingTrace): what priceOne needs of it beyond the expression. A
// retry and a reconcile rebuild it from the row alone, so a rule changed
// after the request does not change the charge.
type frozenRule struct {
	// base is the global price a custom rule replaced; nil without one.
	base *core.PriceBase
	// scale is the multiplier of a multiplier rule, frozen with rows priced
	// before core migration 0124 turned those rules into model rates; nil
	// on any other row. Such a row settles as it was priced: charged at its
	// rate times scale, its list cost its base cost times scale. Nothing
	// sets a scale any more: pricingTrace only carries the one of a frozen
	// item into its trace, and this is where any is applied. Compatibility,
	// removed with core.PriceRule.Scale once no unsettled row has one
	// (CONTRACTS §97.4).
	scale *decimal.Decimal
}

// rule is the price rule frozen in the row's inputs.
func (p *pending) rule() frozenRule {
	if trace := p.Inputs.Pricing; trace != nil {
		return frozenRule{base: trace.Base, scale: trace.Scale}
	}
	return frozenRule{}
}

// applied reports whether a rule priced the row: its base and list costs
// may then differ.
func (r frozenRule) applied() bool { return r.scale != nil || r.base != nil }

// rate is what the expression's result is multiplied by to charge the row
// at rate: rate itself, times the scale of a multiplier rule.
func (r frozenRule) rate(rate decimal.Decimal) decimal.Decimal {
	if r.scale == nil {
		return rate
	}
	return rate.Mul(*r.scale)
}

// BillingDetail is stored in usage_logs.billing_detail once billed. It holds
// billing inputs and results only: the observability markers that used to
// share it live in usage_logs.anomalies (core.Anomaly*).
type BillingDetail struct {
	Additional  []AdditionalBillingDetail `json:"additional,omitempty"`
	Replacement []AdditionalBillingDetail `json:"replacement,omitempty"`
	ExprVersion int                       `json:"expr_version"`
	Tier        string                    `json:"tier"`
	Rules       []expr.RuleResult         `json:"rules"`
	Breakdown   expr.Breakdown            `json:"breakdown"`
	Cost        decimal.Decimal           `json:"cost"`                // at the global price, before the rate
	ListCost    *decimal.Decimal          `json:"list_cost,omitempty"` // at the list price, before the rate; only when a price rule applied
	// BaseFallback marks a Cost that is the list cost: the global price a
	// custom rule replaced could not be evaluated (priceOne).
	BaseFallback   bool            `json:"base_fallback,omitempty"`
	RateMultiplier decimal.Decimal `json:"rate_multiplier"`
	TotalCost      decimal.Decimal `json:"total_cost"` // charged
	// The primary usage's own Cost, ListCost and TotalCost, when the record
	// has additional items and its primary usage is priced (not replaced):
	// Cost, ListCost and TotalCost above then sum it with every item.
	PrimaryCost      *decimal.Decimal `json:"primary_cost,omitempty"`
	PrimaryListCost  *decimal.Decimal `json:"primary_list_cost,omitempty"`
	PrimaryTotalCost *decimal.Decimal `json:"primary_total_cost,omitempty"`
	LedgerID         *int64           `json:"ledger_id,omitempty"`
	Inputs           pendingInputs    `json:"inputs"`
	Attempts         int              `json:"attempts"`
}

// amounts are the three figures of priced usage (CONTRACTS §97): base is its
// cost at the global price and list its cost at the list price, both before
// the rate; total is what is charged. Without a price rule base and list are
// the same.
type amounts struct{ base, list, total decimal.Decimal }

func (a amounts) add(b amounts) amounts {
	return amounts{base: a.base.Add(b.base), list: a.list.Add(b.list), total: a.total.Add(b.total)}
}

// priceOne runs the administrator's price expression over one pending row and
// returns what it costs (amounts), the breakdown that records how, and the
// hash of the expression used. It is the single place a usage figure becomes
// money: settlement, a plugin's reservation and a reconcile that brings back
// the real usage all go through it, so none of them can price a request
// differently from the others.
func priceOne(ctx context.Context, q core.Quoter, p *pending) (amounts, BillingDetail, string, error) {
	if p.Expression == "" {
		return amounts{}, BillingDetail{}, "", errors.New("price expression not found")
	}
	prog, err := expr.CompileCached(p.Expression)
	if err != nil {
		return amounts{}, BillingDetail{}, "", fmt.Errorf("compile price expression: %w", err)
	}
	rule := p.rule()
	amount, err := quote(ctx, q, p, rule)
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}
	// The detail comes from a second, compile-free evaluation (quote).
	res, err := evaluate(prog, p)
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}
	cost := amounts{base: res.Cost, list: res.Cost, total: amount}
	detail := BillingDetail{
		ExprVersion: prog.Version(), Tier: res.Tier, Rules: res.Rules, Breakdown: res.Breakdown,
		Cost: res.Cost, RateMultiplier: p.Rate, TotalCost: amount, Inputs: p.Inputs, Attempts: p.Attempts + 1,
	}
	if rule.applied() {
		cost, detail = withRule(ctx, p, rule, cost, detail)
	}
	return cost, detail, prog.Hash(), nil
}

// quote is what the row is charged: its expression over its usage at its
// rate (frozenRule.rate), rounded.
//
// priceOne evaluates the expression a second time for billing_detail on
// purpose: Quote returns only the final amount (that is all core.Quoter
// promises), while billing_detail needs the tier, rules and per-token
// breakdown, which only prog.Eval exposes. Do not "optimise" the Quote call
// away: it applies the rate multiplier and rounding, so amount and res.Cost
// are not interchangeable. A future core.Quoter method returning both would
// remove this second pass.
func quote(ctx context.Context, q core.Quoter, p *pending, rule frozenRule) (decimal.Decimal, error) {
	params := make(map[string]any, len(p.Inputs.Params))
	for k, v := range p.Inputs.Params {
		params[k] = v
	}
	return q.Quote(ctx, core.QuoteInputs{
		Expression:     p.Expression,
		UsageSemantics: p.Inputs.Semantics,
		Tokens:         p.Tokens,
		Metrics:        p.Metrics,
		PriceParams:    params,
		PriceHeaders:   p.Inputs.Headers,
		RateMultiplier: rule.rate(p.Rate),
		CreatedAt:      p.CreatedAt,
	})
}

// withRule turns the amounts and detail of the row's expression into those
// of the rule. A multiplier rule scales the global price: tier, rules and
// breakdown stay the global price's. A custom rule replaces it: they are the
// rule's, and the global price is evaluated for the base cost alone. The
// base cost is shown, not charged: a global price that cannot be evaluated
// leaves it at the list cost rather than block the charge.
func withRule(ctx context.Context, p *pending, rule frozenRule, cost amounts, detail BillingDetail) (amounts, BillingDetail) {
	if rule.scale != nil {
		cost.list = cost.base.Mul(*rule.scale)
	}
	if rule.base != nil {
		var err error
		if cost.base, err = baseCost(rule.base, p); err != nil {
			slog.WarnContext(ctx, "usage: base price not evaluated, base cost = list cost", "request_id", p.RequestID, "err", err)
			cost.base, detail.BaseFallback = cost.list, true
		}
	}
	detail.Cost, detail.ListCost = cost.base, &cost.list
	return cost, detail
}

// evaluate runs prog over the row's usage, normalized for the token classes
// prog prices.
func evaluate(prog *expr.Program, p *pending) (*expr.Result, error) {
	vars := expr.Normalize(p.Inputs.Semantics, expr.Tokens(p.Tokens), prog.Uses)
	return prog.Eval(expr.Input{Vars: vars, Metrics: p.Metrics, Params: p.Inputs.Params, Headers: p.Inputs.Headers, At: p.CreatedAt})
}

// baseCost is what the row's usage costs at the global price a custom rule
// replaced. The usage is normalized again: the two expressions may price
// different cache classes.
func baseCost(base *core.PriceBase, p *pending) (decimal.Decimal, error) {
	prog, err := expr.CompileCached(base.Expression)
	if err != nil {
		return decimal.Zero, fmt.Errorf("compile base price expression: %w", err)
	}
	res, err := evaluate(prog, p)
	if err != nil {
		return decimal.Zero, err
	}
	return res.Cost, nil
}
