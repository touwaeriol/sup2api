package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Billing modes (model_prices.mode).
const (
	ModePerRequest = "per_request"
	ModePerToken   = "per_token"
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

// PerRequestConfig is mode=per_request: one price per request and the
// request surcharges of a per_token price (CONTRACTS §131).
type PerRequestConfig struct {
	Price *Num         `json:"price"`
	Rules []RuleConfig `json:"rules,omitempty"`
}

// TokenConfig is mode=per_token: prompt-length tiers and request surcharges
// (CONTRACTS §131).
type TokenConfig struct {
	Tiers []TierConfig `json:"tiers"`
	Rules []RuleConfig `json:"rules"`
}

// Tier units.
const (
	UnitToken   = "token"
	UnitRequest = "request"
)

// TierConfig is one tier: it applies up to MaxLen prompt tokens (len), the
// tier without MaxLen to the rest. A unit=token tier has token prices, a
// unit=request tier only Price (USD per request).
type TierConfig struct {
	Name   string `json:"name"`
	MaxLen *Num   `json:"max_len"`
	Unit   string `json:"unit,omitempty"` // token (default) | request
	TokenPrices
	Price *Num `json:"price,omitempty"`
}

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
	case ModePerRequest:
		var c PerRequestConfig
		if err := json.Unmarshal(config, &c); err != nil {
			return "", cfgErr("config", "%v", err)
		}
		if c.Price == nil {
			return "", cfgErr("config.price", "required")
		}
		if err := checkNum("config.price", float64(*c.Price)); err != nil {
			return "", err
		}
		return withRules(fmt.Sprintf(`tier("base", flat(%s))`, fmtNum(float64(*c.Price))), c.Rules)
	case ModePerToken:
		// Strict: a misspelt or old key (prices, flat) would be a free price.
		var c TokenConfig
		dec := json.NewDecoder(bytes.NewReader(config))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return "", cfgErr("config", "%v", err)
		}
		return generateTokens(c)
	case ModeExpression:
		var c map[string]json.RawMessage
		if err := json.Unmarshal(config, &c); err != nil || len(c) > 0 {
			return "", cfgErr("config", "must be {}: an expression price is its expression")
		}
		return "", nil
	}
	return "", cfgErr("mode", "unknown mode %q", mode)
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

// generateTokens builds the tiers by len and applies the rules (withRules).
func generateTokens(c TokenConfig) (string, error) {
	if len(c.Tiers) == 0 {
		return "", cfgErr("config.tiers", "at least one tier is required")
	}
	type tb struct {
		name   string
		maxLen *float64
		body   string
	}
	tiers := make([]tb, 0, len(c.Tiers))
	names := map[string]bool{}
	rest := 0
	for i, t := range c.Tiers {
		field := fmt.Sprintf("config.tiers[%d]", i)
		if !tierNameRe.MatchString(t.Name) {
			return "", cfgErr(field+".name", "must match %s", tierNameRe.String())
		}
		if names[t.Name] {
			return "", cfgErr(field+".name", "duplicate tier %q", t.Name)
		}
		names[t.Name] = true
		body, err := tierBody(field, t)
		if err != nil {
			return "", err
		}
		x := tb{name: t.Name, body: body}
		if t.MaxLen != nil {
			m := float64(*t.MaxLen)
			if err := checkNum(field+".max_len", m); err != nil {
				return "", err
			}
			x.maxLen = &m
		} else {
			rest++
		}
		tiers = append(tiers, x)
	}
	sort.SliceStable(tiers, func(i, j int) bool {
		a, b := tiers[i].maxLen, tiers[j].maxLen
		if a == nil || b == nil {
			return a != nil && b == nil
		}
		return *a < *b
	})
	if rest != 1 {
		return "", cfgErr("config.tiers", "exactly one tier must have no max_len (the rest)")
	}
	for i := 1; i < len(tiers); i++ {
		if tiers[i].maxLen != nil && *tiers[i].maxLen == *tiers[i-1].maxLen {
			return "", cfgErr("config.tiers", "duplicate max_len %s", fmtNum(*tiers[i].maxLen))
		}
	}
	call := func(t tb) string { return fmt.Sprintf("tier(%s, %s)", strconv.Quote(t.name), t.body) }
	// Built right to left: len <= a ? A : (len <= b ? B : C)
	base := call(tiers[len(tiers)-1])
	for i := len(tiers) - 2; i >= 0; i-- {
		if i < len(tiers)-2 {
			base = "(" + base + ")"
		}
		base = fmt.Sprintf("len <= %s ? %s : %s", fmtNum(*tiers[i].maxLen), call(tiers[i]), base)
	}
	return withRules(base, c.Rules)
}

// withRules is base without rules and `(base) * (rule) * ...` with them.
func withRules(base string, rules []RuleConfig) (string, error) {
	if len(rules) == 0 {
		return base, nil
	}
	factors := []string{base}
	for i, r := range rules {
		s, err := ruleExpr(fmt.Sprintf("config.rules[%d]", i), r)
		if err != nil {
			return "", err
		}
		factors = append(factors, s)
	}
	return "(" + strings.Join(factors, ") * (") + ")", nil
}

// ruleExpr is `cond && ... ? multiplier : 1`, a traced rule.
func ruleExpr(field string, r RuleConfig) (string, *ConfigError) {
	if r.Multiplier == nil {
		return "", cfgErr(field+".multiplier", "required")
	}
	m := float64(*r.Multiplier)
	if err := checkNum(field+".multiplier", m); err != nil {
		return "", err
	}
	if len(r.Conds) == 0 {
		return "", cfgErr(field+".conds", "at least one condition is required")
	}
	conds := make([]string, len(r.Conds))
	for i, c := range r.Conds {
		s, err := condExpr(fmt.Sprintf("%s.conds[%d]", field, i), c)
		if err != nil {
			return "", err
		}
		conds[i] = s
	}
	return fmt.Sprintf("%s ? %s : 1", strings.Join(conds, " && "), fmtNum(m)), nil
}

// compareOps are the comparison operators of a condition.
var compareOps = map[string]string{"eq": "==", "ne": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}

// absent is what a source reads when the value is not there: an absent
// usage key reads 0 (eval.go metric).
var absent = map[string]string{"param": "nil", "header": `""`, "usage": "0"}

func condExpr(field string, c CondConfig) (string, *ConfigError) {
	hasValue := len(c.Value) > 0 && string(c.Value) != "null"
	timed := c.TZ != "" || c.FromHour != nil || c.ToHour != nil
	if c.Source == "time" {
		if c.Key != "" || c.Op != "" || hasValue {
			return "", cfgErr(field, "a time condition has tz, from_hour and to_hour only")
		}
		return timeCond(field, c)
	}
	if timed {
		return "", cfgErr(field, "only a time condition has tz, from_hour and to_hour")
	}
	arg, err := condArg(field, c)
	if err != nil {
		return "", err
	}
	switch c.Op {
	case "contains":
		s, err := stringValue(field, c.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("has(%s, %s)", arg, strconv.Quote(s)), nil
	case "exists":
		if hasValue {
			return "", cfgErr(field+".value", "exists takes no value")
		}
		return arg + " != " + absent[c.Source], nil
	case "eq", "ne":
		var lit string
		if c.Source == "header" {
			s, err := stringValue(field, c.Value)
			if err != nil {
				return "", err
			}
			lit = strconv.Quote(s)
		} else if lit, err = literal(field, c.Value); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s", arg, compareOps[c.Op], lit), nil
	case "gt", "gte", "lt", "lte":
		if c.Source == "header" {
			return "", cfgErr(field+".op", "a header is text: eq, ne, contains or exists")
		}
		var n Num
		if json.Unmarshal(c.Value, &n) != nil {
			return "", cfgErr(field+".value", "must be a number")
		}
		if c.Source == "param" {
			arg = "(" + arg + " ?? 0)"
		}
		return fmt.Sprintf("%s %s %s", arg, compareOps[c.Op], fmtNum(float64(n))), nil
	}
	return "", cfgErr(field+".op", "unknown op %q", c.Op)
}

// condArg is the probe a param, header or usage condition reads.
func condArg(field string, c CondConfig) (string, *ConfigError) {
	key := strings.TrimSpace(c.Key)
	switch c.Source {
	case "header":
		key = strings.ToLower(key)
		if !headerNameRe.MatchString(key) {
			return "", cfgErr(field+".key", "invalid header name")
		}
		return fmt.Sprintf("header(%s)", strconv.Quote(key)), nil
	case "param", "usage":
		if key == "" || len(key) > 200 {
			return "", cfgErr(field+".key", "required, at most 200 characters")
		}
		fn := "param"
		if c.Source == "usage" {
			fn = "u"
		}
		return fmt.Sprintf("%s(%s)", fn, strconv.Quote(key)), nil
	}
	return "", cfgErr(field+".source", "must be param, header, usage or time")
}

func timeCond(field string, c CondConfig) (string, *ConfigError) {
	tz := strings.TrimSpace(c.TZ)
	if tz == "" {
		tz = "UTC"
	}
	if _, err := loadLocation(tz); err != nil {
		return "", cfgErr(field+".tz", "unknown time zone %q", tz)
	}
	if c.FromHour == nil || c.ToHour == nil {
		return "", cfgErr(field, "from_hour and to_hour are required")
	}
	from, to := *c.FromHour, *c.ToHour
	if from < 0 || from > 23 || to < 0 || to > 24 || from == to {
		return "", cfgErr(field, "hours must satisfy 0 <= from_hour <= 23, 0 <= to_hour <= 24, from_hour != to_hour")
	}
	h := fmt.Sprintf("hour(%s)", strconv.Quote(tz))
	if from < to {
		return fmt.Sprintf("%s >= %d && %s < %d", h, from, h, to), nil
	}
	return fmt.Sprintf("(%s >= %d || %s < %d)", h, from, h, to), nil // wraps midnight
}

func stringValue(field string, raw json.RawMessage) (string, *ConfigError) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s == "" {
		return "", cfgErr(field+".value", "must be a non-empty string")
	}
	return s, nil
}

func literal(field string, raw json.RawMessage) (string, *ConfigError) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", cfgErr(field+".value", "required")
	}
	switch x := v.(type) {
	case string:
		return strconv.Quote(x), nil
	case float64:
		return fmtNum(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return "", cfgErr(field+".value", "must be a string, number or boolean")
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
