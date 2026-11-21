package expr

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// DefaultBigCostUSD is the default warning threshold for the cost of one
// request with one million input tokens.
var DefaultBigCostUSD = decimal.NewFromInt(10)

// ValidateOptions tune Validate.
type ValidateOptions struct {
	// Facts are the u() keys declared in the generation (usage rules of
	// platforms, endpoints and account type platforms); nil skips the check.
	Facts map[string]bool
	// BigCostUSD is the warning threshold; zero means DefaultBigCostUSD.
	BigCostUSD decimal.Decimal
	// At is the evaluation time for smoke samples; zero means now.
	At time.Time
}

// Analysis summarises what an expression reads, for the console.
type Analysis struct {
	Version  int       `json:"version"`
	Vars     []string  `json:"vars"`
	Params   []string  `json:"params"`
	Headers  []string  `json:"headers"`
	Facts    []string  `json:"facts"`
	Tiers    []string  `json:"tiers"`
	Rules    []string  `json:"rules"`
	LenTiers []float64 `json:"len_boundaries"`
}

// Report is the validation outcome.
type Report struct {
	OK         bool      `json:"ok"`
	Expression string    `json:"expression"`
	ExprHash   string    `json:"expr_hash,omitempty"`
	Errors     []Issue   `json:"errors"`
	Warnings   []Issue   `json:"warnings"`
	Samples    int       `json:"samples"`
	BigCostUSD string    `json:"cost_per_million_input,omitempty"`
	Analysis   *Analysis `json:"analysis,omitempty"`
}

// Analyze describes a compiled program.
func (p *Program) Analyze() *Analysis {
	return &Analysis{
		Version:  p.version,
		Vars:     nonNil(p.UsedVars()),
		Params:   nonNil(p.Params()),
		Headers:  nonNil(p.Headers()),
		Facts:    nonNil(p.Facts()),
		Tiers:    nonNil(p.TierNames()),
		Rules:    nonNil(p.RuleConds()),
		LenTiers: append([]float64{}, p.lenBounds...),
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Validate compiles src, checks u() keys, runs the smoke test and computes
// the big-cost warning.
func Validate(src string, opts ValidateOptions) *Report {
	r := &Report{Expression: strings.TrimSpace(src), Errors: []Issue{}, Warnings: []Issue{}}
	p, err := Compile(src)
	if err != nil {
		if ce, ok := err.(*CompileError); ok {
			r.Errors = append(r.Errors, ce.Issue)
		} else {
			r.Errors = append(r.Errors, newIssue(CodeSyntax, err.Error()))
		}
		return r
	}
	r.ExprHash = p.hash
	r.Analysis = p.Analyze()
	r.Warnings = append(r.Warnings, p.issues...)
	if opts.Facts != nil {
		for _, f := range p.facts {
			if !opts.Facts[f] {
				r.Errors = append(r.Errors, newIssue(CodeUnknownFact, f))
			}
		}
	}

	samples := SmokeSamples(p)
	r.Samples = len(samples)
	for _, s := range samples {
		in := s.Input
		in.At = opts.At
		if _, err := p.Eval(in); err != nil {
			detail := err.Error()
			if ee, ok := err.(*EvalError); ok {
				detail = ee.Issue.Detail
			}
			r.Errors = append(r.Errors, issuef(CodeSmoke, "%s: %s", s.Name, detail))
			break // one failing sample is enough to explain the problem
		}
	}
	if len(r.Errors) == 0 {
		cost, err := p.CostPerMillionInput(opts.At)
		if err == nil {
			r.BigCostUSD = cost.String()
			limit := opts.BigCostUSD
			if limit.IsZero() {
				limit = DefaultBigCostUSD
			}
			if cost.GreaterThan(limit) {
				r.Warnings = append(r.Warnings, issuef(CodeBigCost, "$%s > $%s", cost.StringFixed(4), limit.String()))
			}
		}
	}
	r.OK = len(r.Errors) == 0
	return r
}

// CostPerMillionInput evaluates one request with one million input tokens
// (len = 1e6, everything else 0, no request headers or params) at the given
// time. Request rules only count when they match such a request.
func (p *Program) CostPerMillionInput(at time.Time) (decimal.Decimal, error) {
	res, err := p.Eval(Input{Vars: Vars{P: 1e6, Len: 1e6}, At: at})
	if err != nil {
		return decimal.Zero, err
	}
	return res.Cost, nil
}

// Sample is one smoke-test input.
type Sample struct {
	Name  string
	Input Input
}

var smokeValues = []float64{1, 1000, 1e6}

// SmokeSamples builds the smoke-test inputs: all zero; each used token
// variable and u() key at 1, 1000 and 1e6 alone; everything at once at
// each value; and len at each tier boundary (b-1, b, b+1).
func SmokeSamples(p *Program) []Sample {
	var vars []string
	for _, v := range TokenVars {
		if p.used[v] {
			vars = append(vars, v)
		}
	}
	mk := func(name string, set func(*Vars, map[string]any)) Sample {
		in := Input{Metrics: map[string]any{}}
		for _, f := range p.facts {
			in.Metrics[f] = 0.0
		}
		set(&in.Vars, in.Metrics)
		if in.Vars.Len == 0 {
			v := in.Vars
			in.Vars.Len = v.P + v.CR + v.CC + v.CC1h + v.Img + v.AI
		}
		return Sample{Name: name, Input: in}
	}
	out := []Sample{mk("all=0", func(*Vars, map[string]any) {})}
	for _, val := range smokeValues {
		for _, name := range vars {
			out = append(out, mk(fmt.Sprintf("%s=%g", name, val), func(v *Vars, _ map[string]any) { v.Set(name, val) }))
		}
		for _, f := range p.facts {
			out = append(out, mk(fmt.Sprintf("u(%q)=%g", f, val), func(_ *Vars, m map[string]any) { m[f] = val }))
		}
		out = append(out, mk(fmt.Sprintf("all=%g", val), func(v *Vars, m map[string]any) {
			for _, name := range TokenVars {
				v.Set(name, val)
			}
			for _, f := range p.facts {
				m[f] = val
			}
		}))
	}
	for _, b := range p.lenBounds {
		for _, l := range []float64{b - 1, b, b + 1} {
			if l < 0 {
				continue
			}
			out = append(out, mk(fmt.Sprintf("len=%g", l), func(v *Vars, _ map[string]any) {
				v.P, v.Len, v.C = l, l, 1000
			}))
		}
	}
	return out
}
