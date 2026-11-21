package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
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

// TierCond is one node of a tier condition: exactly one of a comparison
// {var, op, value, tz?}, {"and": [...]}, {"or": [...]} or {"not": node}.
// Decoding never fails on a node: it records the keys written and the first
// key whose value does not decode, and the generator reports them with the
// node's field path.
type TierCond struct {
	Var   string      `json:"var,omitempty"` // len | p | c | hour | minute | weekday | month | day
	Op    string      `json:"op,omitempty"`  // < | <= | > | >= | == | !=
	Value *Num        `json:"value,omitempty"`
	TZ    string      `json:"tz,omitempty"` // time quantities only; "" is UTC
	And   []*TierCond `json:"and,omitempty"`
	Or    []*TierCond `json:"or,omitempty"`
	Not   *TierCond   `json:"not,omitempty"`

	keys []string // sorted
	bad  string   // first key whose value did not decode
}

// UnmarshalJSON records the node; it fails on nothing (see TierCond).
func (c *TierCond) UnmarshalJSON(b []byte) error {
	*c = TierCond{}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil // not an object: no keys, refused as an empty node
	}
	c.keys = slices.Sorted(maps.Keys(m))
	for _, k := range c.keys {
		var err error
		switch k {
		case "var":
			err = json.Unmarshal(m[k], &c.Var)
		case "op":
			err = json.Unmarshal(m[k], &c.Op)
		case "tz":
			err = json.Unmarshal(m[k], &c.TZ)
		case "value":
			err = json.Unmarshal(m[k], &c.Value)
		case "and":
			err = json.Unmarshal(m[k], &c.And)
		case "or":
			err = json.Unmarshal(m[k], &c.Or)
		case "not":
			err = json.Unmarshal(m[k], &c.Not)
		}
		if err != nil && c.bad == "" {
			c.bad = k
		}
	}
	return nil
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

// condWalk renders the condition of one tier and counts its comparisons.
type condWalk struct{ leaves int }

// node renders c at depth (1 for the root). group reports an and/or of two
// or more conditions, which an enclosing and/or puts in parentheses; a group
// of one condition is that condition.
func (w *condWalk) node(field string, c *TierCond, depth int) (src string, group bool, err *ConfigError) {
	if depth > MaxCondDepth {
		return "", false, cfgErr(field, "conditions nest at most %d deep", MaxCondDepth)
	}
	shape, err := c.shape(field)
	if err != nil {
		return "", false, err
	}
	switch shape {
	case "and":
		return w.group(field+".and", c.And, " && ", depth)
	case "or":
		return w.group(field+".or", c.Or, " || ", depth)
	case "not":
		inner, _, err := w.node(field+".not", c.Not, depth+1)
		if err != nil {
			return "", false, err
		}
		return "!(" + inner + ")", false, nil
	}
	if w.leaves++; w.leaves > MaxCondLeaves {
		return "", false, cfgErr(field, "a tier condition has at most %d comparisons", MaxCondLeaves)
	}
	src, err = comparison(field, c)
	return src, false, err
}

func (w *condWalk) group(field string, nodes []*TierCond, sep string, depth int) (string, bool, *ConfigError) {
	switch len(nodes) {
	case 0:
		return "", false, cfgErr(field, "at least one condition is required")
	case 1:
		return w.node(field+"[0]", nodes[0], depth+1)
	}
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		s, group, err := w.node(fmt.Sprintf("%s[%d]", field, i), n, depth+1)
		if err != nil {
			return "", false, err
		}
		if group {
			s = "(" + s + ")"
		}
		parts[i] = s
	}
	return strings.Join(parts, sep), true, nil
}

// condShapes maps each key of a condition node to the shape it belongs to.
var condShapes = map[string]string{"var": "leaf", "op": "leaf", "value": "leaf", "tz": "leaf", "and": "and", "or": "or", "not": "not"}

// condKeyErrs is what a key whose value did not decode must be.
var condKeyErrs = map[string]string{"var": "must be a string", "op": "must be a string", "tz": "must be a string",
	"value": "must be a finite number", "and": "must be a list of conditions", "or": "must be a list of conditions"}

// shape is "leaf", "and", "or" or "not": the one shape the keys of c belong to.
func (c *TierCond) shape(field string) (string, *ConfigError) {
	const oneShape = "must be exactly one of {var, op, value, tz}, {and}, {or} or {not}"
	shape := ""
	for _, k := range c.keysOrNone() {
		s, ok := condShapes[k]
		if !ok {
			return "", cfgErr(field, "unknown key %q", k)
		}
		if shape != "" && s != shape {
			return "", cfgErr(field, oneShape)
		}
		shape = s
	}
	if shape == "" {
		return "", cfgErr(field, oneShape)
	}
	if c.bad != "" {
		return "", cfgErr(field+"."+c.bad, "%s", condKeyErrs[c.bad])
	}
	return shape, nil
}

func (c *TierCond) keysOrNone() []string {
	if c == nil {
		return nil
	}
	return c.keys
}

var (
	countVars = map[string]bool{"len": true, "p": true, "c": true}
	// timeVars are the whole-number ranges of the time quantities.
	timeVars = map[string][2]int{"hour": {0, 23}, "minute": {0, 59}, "weekday": {0, 6}, "month": {1, 12}, "day": {1, 31}}
	condOps  = map[string]bool{"<": true, "<=": true, ">": true, ">=": true, "==": true, "!=": true}
)

// comparison renders a leaf: `len <= 200000`, `hour("Asia/Shanghai") >= 22`.
func comparison(field string, c *TierCond) (string, *ConfigError) {
	r, timed := timeVars[c.Var]
	if !timed && !countVars[c.Var] {
		return "", cfgErr(field+".var", "must be len, p, c, hour, minute, weekday, month or day")
	}
	if !condOps[c.Op] {
		return "", cfgErr(field+".op", "must be <, <=, >, >=, == or !=")
	}
	if c.Value == nil {
		return "", cfgErr(field+".value", "required")
	}
	v := float64(*c.Value)
	if math.IsNaN(v) || math.IsInf(v, 0) { // "NaN" and "Inf" parse as numeric strings
		return "", cfgErr(field+".value", "%s", condKeyErrs["value"])
	}
	if !timed {
		if slices.Contains(c.keys, "tz") {
			return "", cfgErr(field+".tz", "only a time quantity has a time zone")
		}
		return fmt.Sprintf("%s %s %s", c.Var, c.Op, fmtNum(v)), nil
	}
	if v != math.Trunc(v) || v < float64(r[0]) || v > float64(r[1]) {
		return "", cfgErr(field+".value", "%s is a whole number from %d to %d", c.Var, r[0], r[1])
	}
	zone, err := zoneArg(field+".tz", c.TZ)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s(%s) %s %s", c.Var, zone, c.Op, fmtNum(v)), nil
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
	zone, err := zoneArg(field+".tz", strings.TrimSpace(c.TZ))
	if err != nil {
		return "", err
	}
	if c.FromHour == nil || c.ToHour == nil {
		return "", cfgErr(field, "from_hour and to_hour are required")
	}
	from, to := *c.FromHour, *c.ToHour
	if from < 0 || from > 23 || to < 0 || to > 24 || from == to {
		return "", cfgErr(field, "hours must satisfy 0 <= from_hour <= 23, 0 <= to_hour <= 24, from_hour != to_hour")
	}
	h := "hour(" + zone + ")"
	if from < to {
		return fmt.Sprintf("%s >= %d && %s < %d", h, from, h, to), nil
	}
	return fmt.Sprintf("(%s >= %d || %s < %d)", h, from, h, to), nil // wraps midnight
}

// zoneArg is the quoted time zone argument of a time function: tz, UTC
// when blank; it must load.
func zoneArg(field, tz string) (string, *ConfigError) {
	if tz == "" {
		tz = "UTC"
	}
	if _, err := loadLocation(tz); err != nil {
		return "", cfgErr(field, "unknown time zone %q", tz)
	}
	return strconv.Quote(tz), nil
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
