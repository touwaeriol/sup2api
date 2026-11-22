package core

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Group pricing (CONTRACTS §97): what a call costs is a list price times a
// rate, and each side is resolved on its own.
//
//	list price: the group's price of the model -> the global price
//	rate:       the segment's rate in the group
//	            -> the group's rate x the segment's discount -> the group's rate
//
// A segment is a user group: every user belongs to exactly one.

// Segment kinds.
const (
	SegmentNamed    = "named"
	SegmentPersonal = "personal" // the segment of one user
)

// Where a rate comes from.
const (
	RateSourceSegment  = "segment"  // the segment's own rate in the group
	RateSourceDiscount = "discount" // the group's rate times the segment's discount
	RateSourceGroup    = "group"    // the group's rate
)

// Where a list price comes from.
const (
	PriceSourceGroup  = "group"  // the group's rule on the model
	PriceSourceGlobal = "global" // the global model price
)

// Price rule kinds.
const (
	PriceRuleMultiplier = "multiplier" // the global price times a constant
	PriceRuleCustom     = "custom"     // a price of its own
)

// rateScale is the number of decimals a rate keeps, as
// usage_logs.rate_multiplier stores it (numeric(10,4)): a settlement retry
// reads the rate back from that column and must find the number the request
// was priced with.
const rateScale = 4

// maxRate is the largest rate that column holds: 999999.9999.
var maxRate = decimal.New(9999999999, -rateScale)

// ParseRate reads a rate, a discount or a multiplier as a client sends it: a
// decimal from 0 to 999999.9999 with 4 decimals at most.
func ParseRate(s string) (decimal.Decimal, bool) {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	return d, err == nil && !d.IsNegative() && !d.GreaterThan(maxRate) && d.Exponent() >= -rateScale
}

// SegmentRef names a segment where other rows refer to it. Name of a personal
// segment is its user's email.
type SegmentRef struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	UserID *int64 `json:"user_id,omitempty"`
}

// GroupRef names a group where other rows refer to it.
type GroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// SegmentRate is the rate a segment pays in a group and where it comes from:
// its override there, else the group's default times its discount, else the
// group's default. It is the one place the rule lives.
func SegmentRate(groupDefault, discount decimal.Decimal, override *decimal.Decimal) (decimal.Decimal, string) {
	if override != nil {
		return *override, RateSourceSegment
	}
	if discount.Equal(decimal.NewFromInt(1)) {
		return groupDefault, RateSourceGroup
	}
	return decimal.Min(groupDefault.Mul(discount).Round(rateScale), maxRate), RateSourceDiscount
}

// PriceQuery asks for the list price of a model. GroupID selects the price
// rule that applies; 0 asks for the global price.
type PriceQuery struct {
	Model   string
	GroupID int64
}

// PricingTrace records why a usage record was priced as it was. It is frozen
// with the record (usage_logs.billing_detail.inputs.pricing), so a settlement
// retry prices it the same way whatever the rules are by then. A record at
// the global price and the group's own rate has none.
type PricingTrace struct {
	SegmentID  int64  `json:"segment_id,omitempty"`
	RateSource string `json:"rate_source,omitempty"`
	// PriceSource is group, or segment on a record priced by a user group's
	// rule before core migration 0123 removed them (PriceRule.Source).
	PriceSource string `json:"price_source,omitempty"`
	// Scale is the multiplier of a multiplier rule.
	Scale *decimal.Decimal `json:"scale,omitempty"`
	// Base is the global price a custom rule replaced.
	Base *PriceBase `json:"base,omitempty"`
}

// PriceBase is the global price expression behind a custom rule: what the
// usage would have cost without the rule.
type PriceBase struct {
	Expression string `json:"expression"`
	ExprHash   string `json:"expr_hash"`
}
