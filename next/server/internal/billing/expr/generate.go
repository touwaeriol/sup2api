package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Billing modes (model_prices.mode, CONTRACTS §131.1): a visual price is
// generated from its config, an expression price is written by hand.
const (
	ModeVisual     = "visual"
	ModeExpression = "expression"
)

// Num is a JSON number that also accepts numeric strings ("3.75").
type Num float64

func (n *Num) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("invalid number %q", s)
		}
		*n = Num(f)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = Num(f)
	return nil
}

// VisualConfig is mode=visual: conditional tiers and request surcharges
// (CONTRACTS §131.2).
type VisualConfig struct {
	Tiers []TierConfig `json:"tiers"`
	Rules []RuleConfig `json:"rules"`
}

// Tier units.
const (
	UnitToken   = "token"
	UnitRequest = "request"
)

// TierConfig is one tier. Tiers are tried in order and the first whose When
// holds applies; the last tier has no When and takes the rest. A unit=token
// tier has token prices, a unit=request tier only Price (USD per request).
type TierConfig struct {
	Name string    `json:"name"`
	When *TierCond `json:"when,omitempty"`
	Unit string    `json:"unit,omitempty"` // token (default) | request
	TokenPrices
	Price *Num `json:"price,omitempty"`
}

// Limits of a visual config.
const (
	MaxTiers      = 20
	MaxCondDepth  = 5  // a lone comparison is 1 deep
	MaxCondLeaves = 20 // comparisons in the condition of one tier
)

// PriceKeys are the price fields of a tier, USD per million tokens or, for
// price, per request; a multiplier scales every one of them.
var PriceKeys = []string{"p", "c", "cr", "cc", "cc1h", "img", "img_o", "price"}

// TokenPrices are USD per million tokens. p and c are always emitted (0 when
// absent); any other price is emitted only when present, so cache tokens
// without a price are not billed and image tokens without one are billed as
// p and c.
type TokenPrices struct {
	P    *Num `json:"p,omitempty"`
	C    *Num `json:"c,omitempty"`
	CR   *Num `json:"cr,omitempty"`
	CC   *Num `json:"cc,omitempty"`
	CC1h *Num `json:"cc1h,omitempty"`
	Img  *Num `json:"img,omitempty"`
	ImgO *Num `json:"img_o,omitempty"`
}

// RuleConfig is one request surcharge: the price is multiplied by Multiplier
// when every condition holds. Name is for people only.
type RuleConfig struct {
	Name       string       `json:"name,omitempty"`
	Multiplier *Num         `json:"multiplier"`
	Conds      []CondConfig `json:"conds"`
}

// CondConfig is one condition of a rule. Source param, header or usage
// compares the value under Key with Op; source time is the hour window
// [FromHour, ToHour) in TZ and has no Op.
type CondConfig struct {
	Source   string          `json:"source"` // param | header | usage | time
	Key      string          `json:"key,omitempty"`
	Op       string          `json:"op,omitempty"` // eq | ne | contains | exists | gt | gte | lt | lte
	Value    json.RawMessage `json:"value,omitempty"`
	TZ       string          `json:"tz,omitempty"`
	FromHour *int            `json:"from_hour,omitempty"`
	ToHour   *int            `json:"to_hour,omitempty"`
}

// ConfigError is a generator failure bound to a config field.
type ConfigError struct {
	Field string
	Issue Issue
}

// Error is the detail, which starts with the field.
func (e *ConfigError) Error() string { return e.Issue.Detail }

func cfgErr(field, format string, args ...any) *ConfigError {
	return &ConfigError{Field: field, Issue: issuef(CodeConfig, "%s: "+format, append([]any{field}, args...)...)}
}

// Generate builds the expression of a price from its config. An expression
// price is written by hand: its config must be {} and Generate returns "".
// Errors are *ConfigError.
func Generate(mode string, config json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage("{}")
	}
	switch mode {
	case ModeVisual:
		var c VisualConfig
		if err := decodeStrict(config, &c); err != nil {
			return "", err
		}
		return generateVisual(c)
	case ModeExpression:
		var c map[string]json.RawMessage
		if err := json.Unmarshal(config, &c); err != nil || len(c) > 0 {
			return "", cfgErr("config", "must be {}: an expression price is its expression")
		}
		return "", nil
	}
	return "", cfgErr("mode", "unknown mode %q", mode)
}

// decodeStrict decodes a config refusing unknown keys: a misspelt or old key
// (rule, prices, flat) would silently drop a charge.
func decodeStrict(config json.RawMessage, v any) *ConfigError {
	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return cfgErr("config", "%v", err)
	}
	return nil
}

func checkNum(field string, f float64) *ConfigError {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return cfgErr(field, "must be a finite, non-negative number")
	}
	return nil
}

// fmtNum prints the shortest decimal form without exponent.
func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func tierBody(field string, t TierConfig) (string, *ConfigError) {
	switch t.Unit {
	case "", UnitToken:
		if t.Price != nil {
			return "", cfgErr(field+".price", "only a unit request tier has a price")
		}
		return tokenBody(field, t.TokenPrices)
	case UnitRequest:
		if t.TokenPrices != (TokenPrices{}) {
			return "", cfgErr(field, "a unit request tier has a price and no token prices")
		}
		if t.Price == nil {
			return "", cfgErr(field+".price", "required")
		}
		if err := checkNum(field+".price", float64(*t.Price)); err != nil {
			return "", err
		}
		return fmt.Sprintf("flat(%s)", fmtNum(float64(*t.Price))), nil
	}
	return "", cfgErr(field+".unit", "must be token or request")
}

func tokenBody(field string, c TokenPrices) (string, *ConfigError) {
	var terms []string
	zero := Num(0)
	for _, x := range []struct {
		name     string
		v        *Num
		required bool
	}{{"p", c.P, true}, {"c", c.C, true}, {"cr", c.CR, false}, {"cc", c.CC, false}, {"cc1h", c.CC1h, false},
		{"img", c.Img, false}, {"img_o", c.ImgO, false}} {
		v := x.v
		if v == nil {
			if !x.required {
				continue
			}
			v = &zero
		}
		if err := checkNum(field+"."+x.name, float64(*v)); err != nil {
			return "", err
		}
		terms = append(terms, fmt.Sprintf("%s*%s", x.name, fmtNum(float64(*v))))
	}
	return strings.Join(terms, " + "), nil
}

var (
	tierNameRe   = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~\-]{1,100}$`)
)

// generateVisual builds the tier chain and applies the rules (withRules):
// `c1 ? T1 : (c2 ? T2 : T3)`, the last two tiers without parentheses.
func generateVisual(c VisualConfig) (string, error) {
	n := len(c.Tiers)
	if n == 0 {
		return "", cfgErr("config.tiers", "at least one tier is required")
	}
	if n > MaxTiers {
		return "", cfgErr("config.tiers", "at most %d tiers", MaxTiers)
	}
	calls, conds := make([]string, n), make([]string, n)
	names := map[string]bool{}
	for i, t := range c.Tiers {
		call, cond, err := tierExpr(fmt.Sprintf("config.tiers[%d]", i), t, i == n-1, names)
		if err != nil {
			return "", err
		}
		calls[i], conds[i] = call, cond
	}
	base := calls[n-1]
	for i := n - 2; i >= 0; i-- {
		if i < n-2 {
			base = "(" + base + ")"
		}
		base = conds[i] + " ? " + calls[i] + " : " + base
	}
	return withRules(base, c.Rules)
}

// tierExpr checks one tier and returns its tier() call and its condition,
// "" for the last tier.
func tierExpr(field string, t TierConfig, last bool, names map[string]bool) (call, cond string, err *ConfigError) {
	if !tierNameRe.MatchString(t.Name) {
		return "", "", cfgErr(field+".name", "must match %s", tierNameRe.String())
	}
	if names[t.Name] {
		return "", "", cfgErr(field+".name", "duplicate tier %q", t.Name)
	}
	names[t.Name] = true
	body, err := tierBody(field, t)
	if err != nil {
		return "", "", err
	}
	call = fmt.Sprintf("tier(%s, %s)", strconv.Quote(t.Name), body)
	switch {
	case last && t.When != nil:
		return "", "", cfgErr(field+".when", "the last tier takes the rest and has no condition")
	case last:
		return call, "", nil
	case t.When == nil:
		return "", "", cfgErr(field+".when", "required on every tier but the last")
	}
	cond, _, err = (&condWalk{}).node(field+".when", t.When, 1)
	return call, cond, err
}

// Times returns the expression src multiplied by the constant m,
// "(<src>) * m"; the ")" goes on a line of its own after a line comment.
func Times(src, m string) (string, error) {
	_, body, err := splitVersion(src)
	if err != nil {
		return "", err
	}
	if strings.Contains(body, "//") {
		body += "\n"
	}
	out := "(" + body + ") * " + m
	if _, err := Compile(out); err != nil {
		return "", err
	}
	return out, nil
}
