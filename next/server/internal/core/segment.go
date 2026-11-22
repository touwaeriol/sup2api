package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/shopspring/decimal"
)

// Group pricing (CONTRACTS §97): what a call costs is a list price times a
// rate, and each side is resolved on its own.
//
//	list price: the group's price of the model -> the global price
//	rate:       one applies, never a product, the most specific first:
//	            segment x group x model -> segment x group
//	            -> group x model -> the group's base rate
//
// A segment is a user group: every user belongs to exactly one.

// Segment kinds.
const (
	SegmentNamed    = "named"
	SegmentPersonal = "personal" // the segment of one user
)

// Where a rate comes from, the most specific first.
const (
	RateSourceSegmentModel = "segment_model" // the segment's rate for the model in the group
	RateSourceSegment      = "segment"       // the segment's rate in the group
	RateSourceGroupModel   = "group_model"   // the group's rate for the model
	RateSourceGroup        = "group"         // the group's base rate
)

// Where a list price comes from.
const (
	PriceSourceGroup  = "group"  // the group's rule on the model
	PriceSourceGlobal = "global" // the global model price
)

// rateScale is the number of decimals a rate keeps, as
// usage_logs.rate_multiplier stores it (numeric(10,4)): a settlement retry
// reads the rate back from that column and must find the number the request
// was priced with.
const rateScale = 4

// maxRate is the largest rate that column holds: 999999.9999.
var maxRate = decimal.New(9999999999, -rateScale)

// ParseRate reads a rate as a client sends it: a
// decimal from 0 to 999999.9999 with 4 decimals at most.
func ParseRate(s string) (decimal.Decimal, bool) {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	return d, err == nil && !d.IsNegative() && !d.GreaterThan(maxRate) && d.Exponent() >= -rateScale
}

// RateError is the field error of a value at field that is no rate
// (ParseRate).
func RateError(ctx context.Context, field string) FieldError {
	return NewFieldError(ctx, field, "invalid",
		"must be 0 or more and below 1000000, with at most 4 decimals", "必须不小于 0、小于 1000000，最多 4 位小数")
}

// RateInput is a rate request field: a rate as a JSON string, or null
// (Value nil). Set marks it present and Invalid a value that is no rate:
// one that is no string or null, or a string ParseRate refuses. Decoding
// never fails, so a list reports each wrong item by its path (Errors)
// instead of failing as a whole.
type RateInput struct {
	Set     bool
	Value   *decimal.Decimal
	Invalid bool
}

func (in *RateInput) UnmarshalJSON(b []byte) error {
	*in = RateInput{Set: true}
	var text *string
	if json.Unmarshal(b, &text) != nil {
		in.Invalid = true
		return nil
	}
	if text != nil {
		d, ok := ParseRate(*text)
		in.Value, in.Invalid = &d, !ok
	}
	return nil
}

// Errors are the field errors of the rate at field: required when it is
// missing (a rate or null must be given), RateError when it is no rate.
// Value means anything only without them.
func (in RateInput) Errors(ctx context.Context, field string) []FieldError {
	switch {
	case !in.Set:
		return []FieldError{NewFieldError(ctx, field, "required", "a rate or null is required", "必须填写倍率或 null")}
	case in.Invalid:
		return []FieldError{RateError(ctx, field)}
	}
	return nil
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

// RateLevels are the rates that may apply to a call, nil where not set.
// Rate picks one; it is the one place the rule lives.
type RateLevels struct {
	SegmentModel *decimal.Decimal
	Segment      *decimal.Decimal
	GroupModel   *decimal.Decimal
	Group        decimal.Decimal // the group's base rate
}

// Rate is the most specific rate set and where it comes from.
func (l RateLevels) Rate() (decimal.Decimal, string) {
	switch {
	case l.SegmentModel != nil:
		return *l.SegmentModel, RateSourceSegmentModel
	case l.Segment != nil:
		return *l.Segment, RateSourceSegment
	case l.GroupModel != nil:
		return *l.GroupModel, RateSourceGroupModel
	}
	return l.Group, RateSourceGroup
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
	// Scale is the multiplier of a multiplier rule, on records priced
	// before core migration 0124 turned those rules into model rates
	// (PriceRule.Scale); none is written any more. Compatibility, removed
	// with PriceRule.Scale (CONTRACTS §97.4).
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
