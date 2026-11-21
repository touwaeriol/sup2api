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

// The list price is asked for the key's group and its owner's segment, for
// the request's model and for each model it refers to; the usage record has
// the segment and the rate the owner pays, whatever the group's own rate is.
func TestPricingFollowsThePrincipalsSegment(t *testing.T) {
	e := pricedAdvisorEnv(t)
	p := e.auth.keys[testKey]
	p.SegmentID, p.RateSource, p.Group.RateMultiplier = 12, core.RateSourceDiscount, decimal.RequireFromString("1.2")
	e.up.set("acc-1", &upstreamRule{status: 200, body: advisorUsageBody})

	require.Equal(t, 200, e.messages(advisorBody(false)).status)
	rec := e.record()
	require.Equal(t, []core.PriceQuery{
		{Model: testModel, GroupID: testGroup, SegmentID: 12},
		{Model: advisorModel, GroupID: testGroup, SegmentID: 12},
	}, e.pricer.queries)
	require.Equal(t, []any{int64(12), core.RateSourceDiscount, "1.2"}, []any{rec.SegmentID, rec.RateSource, rec.RateMultiplier.String()})
	require.Len(t, rec.Additional, 1)
	require.Equal(t, "1.2", rec.Additional[0].RateMultiplier.String())
}

// The price a usage item is frozen with is a copy down to the scale of a
// multiplier rule and the base of a custom one: the pricer's rule is shared
// by every request.
func TestReferencedPriceIsFrozenApartFromThePricer(t *testing.T) {
	scale := decimal.RequireFromString("0.8")
	e := newEnv(t, func(e *env) {
		e.pricer.rules[advisorModel] = &core.PriceRule{ID: 19, Model: advisorModel, Mode: "visual", Expression: "p*7", ExprHash: "rule",
			Source: core.PriceSourceSegment, Scale: &scale, Base: &core.PriceBase{Expression: "p*9", ExprHash: "global"}}
	})
	e.up.set("acc-1", &upstreamRule{status: 200, body: advisorUsageBody})

	require.Equal(t, 200, e.messages(advisorBody(false)).status)
	rec := e.record()
	require.Len(t, rec.Additional, 1)
	rule, frozen := e.pricer.rules[advisorModel], rec.Additional[0].Price
	require.Equal(t, rule, frozen)

	*frozen.Scale = decimal.Zero
	frozen.Base.Expression = "0"
	require.Equal(t, "0.8", rule.Scale.String())
	require.Equal(t, "p*9", rule.Base.Expression)
}
