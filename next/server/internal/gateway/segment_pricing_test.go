package gateway

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// advisorUsageBody is an upstream answer whose usage has one advisor call.
const advisorUsageBody = `{"type":"message","role":"assistant","model":"` + testModel + `","content":[],"usage":{"input_tokens":1,"output_tokens":2,` +
	`"iterations":[{"type":"advisor_message","model":"` + advisorModel + `","input_tokens":3,"output_tokens":4}]}}`

// The list price is asked for the key's group, for the request's model and
// for each model it refers to; each is priced at the rate the owner pays for
// it (core.RateLevels), and the usage record has the owner's segment and the
// request model's rate with its source.
func TestPricingFollowsThePrincipalsRate(t *testing.T) {
	e := pricedAdvisorEnv(t)
	d := decimal.RequireFromString
	p := e.auth.keys[testKey]
	p.SegmentID, p.RateOverride = 12, new(d("1.2"))
	e.pricer.rateSegment = 12
	e.pricer.rates = map[string]core.RateLevels{testModel: {GroupModel: new(d("3"))}, advisorModel: {SegmentModel: new(d("0.6")), GroupModel: new(d("3"))}}
	e.up.set("acc-1", &upstreamRule{status: 200, body: advisorUsageBody})

	require.Equal(t, 200, e.messages(advisorBody(false)).status)
	rec := e.record()
	require.Equal(t, []core.PriceQuery{
		{Model: testModel, GroupID: testGroup},
		{Model: advisorModel, GroupID: testGroup},
	}, e.pricer.queries)
	require.Equal(t, []any{int64(12), core.RateSourceSegment, "1.2"}, []any{rec.SegmentID, rec.RateSource, rec.RateMultiplier.String()})
	require.Len(t, rec.Additional, 1)
	require.Equal(t, "0.6", rec.Additional[0].RateMultiplier.String())
}

// Without a rate of the owner's segment, a model is priced at the group's
// rate for it, else at the group's base rate.
func TestPricingAtTheGroupsModelRate(t *testing.T) {
	e := pricedAdvisorEnv(t)
	e.pricer.rates = map[string]core.RateLevels{advisorModel: {GroupModel: new(decimal.RequireFromString("0.9"))}}
	e.up.set("acc-1", &upstreamRule{status: 200, body: advisorUsageBody})

	require.Equal(t, 200, e.messages(advisorBody(false)).status)
	rec := e.record()
	require.Equal(t, []any{core.RateSourceGroup, "1.5"}, []any{rec.RateSource, rec.RateMultiplier.String()})
	require.Len(t, rec.Additional, 1)
	require.Equal(t, "0.9", rec.Additional[0].RateMultiplier.String())
}

// The price a usage item is frozen with is a copy down to the base of a
// custom rule: the pricer's rule is shared by every request.
func TestReferencedPriceIsFrozenApartFromThePricer(t *testing.T) {
	e := newEnv(t, func(e *env) {
		e.pricer.rules[advisorModel] = &core.PriceRule{ID: 19, Model: advisorModel, Mode: "visual", Expression: "p*7", ExprHash: "rule",
			Source: core.PriceSourceGroup, Base: &core.PriceBase{Expression: "p*9", ExprHash: "global"}}
	})
	e.up.set("acc-1", &upstreamRule{status: 200, body: advisorUsageBody})

	require.Equal(t, 200, e.messages(advisorBody(false)).status)
	rec := e.record()
	require.Len(t, rec.Additional, 1)
	rule, frozen := e.pricer.rules[advisorModel], rec.Additional[0].Price
	require.Equal(t, rule, frozen)

	frozen.Base.Expression = "0"
	require.Equal(t, "p*9", rule.Base.Expression)
}
