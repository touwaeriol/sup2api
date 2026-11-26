package payment

import (
	"github.com/shopspring/decimal"
)

// Num is a decimal that the API writes as a JSON number: the console's
// top-up types are numbers, while the money stays decimal end to end.
// Reading takes a number or a numeric string (decimal's UnmarshalJSON),
// which never yields NaN or infinity.
type Num struct{ decimal.Decimal }

// MarshalJSON writes the decimal unquoted.
func (n Num) MarshalJSON() ([]byte, error) { return []byte(n.String()), nil }

func num(d decimal.Decimal) Num { return Num{d} }

func numInt(i int64) Num { return Num{decimal.NewFromInt(i)} }

// decimalOf parses a decimal constant.
func decimalOf(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// exact reports whether d has at most places decimals.
func exact(d decimal.Decimal, places int32) bool { return d.Equal(d.Round(places)) }
