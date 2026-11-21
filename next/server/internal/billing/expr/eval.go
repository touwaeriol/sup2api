package expr

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // time zone functions must work on minimal images

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/shopspring/decimal"
)

var locMu sync.RWMutex

// env is the evaluation environment. Field tags are the names visible to
// expressions; func fields are bound per evaluation.
type env struct {
	P    float64 `expr:"p"`
	C    float64 `expr:"c"`
	CR   float64 `expr:"cr"`
	CC   float64 `expr:"cc"`
	CC1h float64 `expr:"cc1h"`
	Len  float64 `expr:"len"`
	Img  float64 `expr:"img"`
	ImgO float64 `expr:"img_o"`
	AI   float64 `expr:"ai"`
	AO   float64 `expr:"ao"`

	U       func(string) any                   `expr:"u"`
	Tier    func(string, any) (float64, error) `expr:"tier"`
	Flat    func(any) (float64, error)         `expr:"flat"`
	Param   func(string) any                   `expr:"param"`
	Header  func(string) string                `expr:"header"`
	Has     func(any, any) bool                `expr:"has"`
	Hour    func(string) (int, error)          `expr:"hour"`
	Minute  func(string) (int, error)          `expr:"minute"`
	Weekday func(string) (int, error)          `expr:"weekday"`
	Day     func(string) (int, error)          `expr:"day"`
	Month   func(string) (int, error)          `expr:"month"`
	Rule    func(int, any) (bool, error)       `expr:"__rule"` // traceFunc
}

// Vars are the normalized variable values of one evaluation.
type Vars struct {
	P    float64 `json:"p"`
	C    float64 `json:"c"`
	CR   float64 `json:"cr"`
	CC   float64 `json:"cc"`
	CC1h float64 `json:"cc1h"`
	Len  float64 `json:"len"`
	Img  float64 `json:"img,omitempty"`
	ImgO float64 `json:"img_o,omitempty"`
	AI   float64 `json:"ai,omitempty"`
	AO   float64 `json:"ao,omitempty"`
}

// Get returns a variable by expression name.
func (v Vars) Get(name string) float64 {
	switch name {
	case "p":
		return v.P
	case "c":
		return v.C
	case "cr":
		return v.CR
	case "cc":
		return v.CC
	case "cc1h":
		return v.CC1h
	case "len":
		return v.Len
	case "img":
		return v.Img
	case "img_o":
		return v.ImgO
	case "ai":
		return v.AI
	case "ao":
		return v.AO
	}
	return 0
}

// Set assigns a variable by expression name.
func (v *Vars) Set(name string, f float64) {
	switch name {
	case "p":
		v.P = f
	case "c":
		v.C = f
	case "cr":
		v.CR = f
	case "cc":
		v.CC = f
	case "cc1h":
		v.CC1h = f
	case "len":
		v.Len = f
	case "img":
		v.Img = f
	case "img_o":
		v.ImgO = f
	case "ai":
		v.AI = f
	case "ao":
		v.AO = f
	}
}

// Input is everything one evaluation may read.
type Input struct {
	Vars    Vars
	Metrics map[string]any    // u("key") values
	Params  map[string]string // body path -> raw JSON value captured at request time
	Headers map[string]string // lower-case name -> value
	At      time.Time         // request start; zero means now
}

// RuleResult reports one traced request rule. A rule the evaluation did not
// reach (a branch not taken, a short-circuited operand) is not matched.
type RuleResult struct {
	Cond       string  `json:"cond"`
	Multiplier float64 `json:"multiplier"`
	Matched    bool    `json:"matched"`
}

// Item is one additive term of a tier that was hit.
type Item struct {
	Tier     string          `json:"tier"`
	Label    string          `json:"label"`              // p, c, cr, ..., flat, u:<key>, other
	Quantity *float64        `json:"quantity,omitempty"` // variable value when the term prices one variable
	Rate     *float64        `json:"rate,omitempty"`     // USD per million when the term is var*number
	Expr     string          `json:"expr"`
	Cost     decimal.Decimal `json:"cost"` // USD
}

// Breakdown explains a result.
type Breakdown struct {
	Vars            Vars            `json:"vars"`
	Metrics         map[string]any  `json:"metrics,omitempty"`
	Tiers           []string        `json:"tiers,omitempty"` // every tier() hit, in order
	Items           []Item          `json:"items"`
	Subtotal        decimal.Decimal `json:"subtotal"`         // sum of items (USD)
	Base            decimal.Decimal `json:"base"`             // the expression with no rule matched (USD)
	RulesMultiplier decimal.Decimal `json:"rules_multiplier"` // value / base (8 places), 1 when the base is 0
}

// Result is the outcome of one evaluation. Cost excludes the group rate
// multiplier, which the caller applies.
type Result struct {
	Cost      decimal.Decimal `json:"cost"`  // USD
	Value     float64         `json:"value"` // raw expression value (micro-dollars)
	Tier      string          `json:"tier"`  // last tier() hit, "" when none
	Rules     []RuleResult    `json:"rules"`
	Breakdown Breakdown       `json:"breakdown"`
}

type evalState struct {
	tiers   []string
	matched []bool // by rule index
	noRules bool   // every traced rule takes its ": 1" branch
}

func newEnv(in *Input, st *evalState) *env {
	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	inZone := func(tz string) (time.Time, error) {
		l, err := loadLocation(tz)
		if err != nil {
			return time.Time{}, err
		}
		return at.In(l), nil
	}
	v := in.Vars
	return &env{
		P: v.P, C: v.C, CR: v.CR, CC: v.CC, CC1h: v.CC1h, Len: v.Len,
		Img: v.Img, ImgO: v.ImgO, AI: v.AI, AO: v.AO,
		U: func(key string) any { return metric(in.Metrics, key) },
		Tier: func(name string, x any) (float64, error) {
			f, err := toFloat(x)
			if st != nil {
				st.tiers = append(st.tiers, name)
			}
			return f, err
		},
		Flat: func(x any) (float64, error) {
			f, err := toFloat(x)
			return f * Scale, err
		},
		Param:  func(path string) any { return param(in.Params, path) },
		Header: func(name string) string { return header(in.Headers, name) },
		Has:    func(s, sub any) bool { return strings.Contains(toString(s), toString(sub)) },
		Hour: func(tz string) (int, error) {
			t, err := inZone(tz)
			return t.Hour(), err
		},
		Minute: func(tz string) (int, error) {
			t, err := inZone(tz)
			return t.Minute(), err
		},
		Weekday: func(tz string) (int, error) {
			t, err := inZone(tz)
			return int(t.Weekday()), err
		},
		Day: func(tz string) (int, error) {
			t, err := inZone(tz)
			return t.Day(), err
		},
		Month: func(tz string) (int, error) {
			t, err := inZone(tz)
			return int(t.Month()), err
		},
		Rule: func(i int, cond any) (bool, error) {
			b, ok := cond.(bool)
			switch {
			case cond == nil: // an absent param, as the old engine read it
			case !ok:
				return false, fmt.Errorf("rule condition is %T, not a boolean", cond)
			case st.noRules:
				return false, nil
			}
			if b && i < len(st.matched) {
				st.matched[i] = true
			}
			return b, nil
		},
	}
}

func metric(m map[string]any, key string) any {
	v, ok := m[key]
	if !ok || v == nil {
		return 0.0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case bool, string:
		return x
	}
	return fmt.Sprint(v)
}

func toFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case float32:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	}
	return 0, fmt.Errorf("expected a number, got %T", v)
}

func param(m map[string]string, path string) any {
	raw, ok := m[path]
	if !ok || raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw // not JSON: treat as a plain string
	}
	return v
}

func header(m map[string]string, name string) string {
	if v, ok := m[strings.ToLower(name)]; ok {
		return v
	}
	for k, v := range m { // tolerate callers that did not lower-case
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// EvalError is a runtime evaluation failure (bad result or runtime error).
type EvalError struct {
	Issue Issue
}

func (e *EvalError) Error() string {
	if e.Issue.Detail != "" {
		return e.Issue.Message["en"] + ": " + e.Issue.Detail
	}
	return e.Issue.Message["en"]
}

func evalErr(code, detail string) *EvalError { return &EvalError{Issue: newIssue(code, detail)} }

func runFloat(prog *vm.Program, e *env) (float64, error) {
	out, err := expr.Run(prog, e)
	if err != nil {
		return 0, err
	}
	f, ok := out.(float64)
	if !ok {
		return 0, fmt.Errorf("expected a number, got %T", out)
	}
	return f, nil
}

func finiteNonNeg(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 }

var decScale = decimal.NewFromInt(Scale)

// usd converts an expression value to USD.
func usd(f float64) decimal.Decimal { return decimal.NewFromFloat(f).Div(decScale) }

// Eval runs the expression. The result is always finite and non-negative;
// anything else is an *EvalError.
func (p *Program) Eval(in Input) (*Result, error) {
	st := &evalState{matched: make([]bool, len(p.rules))}
	value, err := p.run(&in, st)
	if err != nil {
		return nil, err
	}
	res := &Result{Value: value, Cost: usd(value), Rules: make([]RuleResult, len(p.rules))}
	for i, r := range p.rules {
		res.Rules[i] = RuleResult{Cond: r.cond, Multiplier: r.mult, Matched: st.matched[i]}
	}
	// The base is the value with no rule matched; a run that fails then
	// (a rule kept it from an error) leaves it at the value.
	baseValue := value
	if slices.Contains(st.matched, true) {
		if v, err := p.run(&in, &evalState{noRules: true}); err == nil {
			baseValue = v
		}
	}
	mult := decimal.NewFromInt(1)
	if baseValue > 0 {
		mult = decimal.NewFromFloat(value / baseValue).Round(8)
	}
	if len(st.tiers) > 0 {
		res.Tier = st.tiers[len(st.tiers)-1]
	}
	res.Breakdown = Breakdown{
		Vars:            in.Vars,
		Metrics:         in.Metrics,
		Tiers:           st.tiers,
		Items:           p.items(&in, st.tiers),
		Base:            usd(baseValue),
		RulesMultiplier: mult,
	}
	sub := decimal.Zero
	for _, it := range res.Breakdown.Items {
		sub = sub.Add(it.Cost)
	}
	res.Breakdown.Subtotal = sub
	return res, nil
}

// run evaluates the program once; the value must be finite and non-negative.
func (p *Program) run(in *Input, st *evalState) (float64, error) {
	value, err := runFloat(p.prog, newEnv(in, st))
	if err != nil {
		return 0, evalErr(CodeRuntime, err.Error())
	}
	if !finiteNonNeg(value) {
		return 0, evalErr(CodeBadResult, fmt.Sprint(value))
	}
	return value, nil
}

// items evaluates the additive terms of every tier that was hit.
func (p *Program) items(in *Input, hit []string) []Item {
	out := []Item{}
	seen := map[string]bool{}
	e := newEnv(in, nil)
	for _, name := range hit {
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, td := range p.tiers {
			if td.name != name {
				continue
			}
			for _, t := range td.terms {
				f, err := runFloat(t.prog, e)
				if err != nil {
					continue
				}
				it := Item{Tier: name, Label: t.label, Rate: t.rate, Expr: t.expr, Cost: usd(f)}
				if t.varName != "" {
					q := in.Vars.Get(t.varName)
					it.Quantity = &q
				}
				out = append(out, it)
			}
			break
		}
	}
	return out
}
