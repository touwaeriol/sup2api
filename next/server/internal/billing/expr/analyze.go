package expr

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// analyzer walks the parsed (unchecked) AST to find the inputs an expression
// reads and to build the per-tier term breakdown.
type analyzer struct {
	p        *Program
	params   map[string]bool
	headers  map[string]bool
	facts    map[string]bool
	bounds   map[float64]bool
	tierSeen map[string]bool
}

func newAnalyzer(p *Program) *analyzer {
	return &analyzer{
		p:        p,
		params:   map[string]bool{},
		headers:  map[string]bool{},
		facts:    map[string]bool{},
		bounds:   map[float64]bool{},
		tierSeen: map[string]bool{},
	}
}

var timeFuncs = map[string]bool{"hour": true, "minute": true, "weekday": true, "day": true, "month": true}

var isVar = func() map[string]bool {
	m := map[string]bool{"len": true}
	for _, v := range TokenVars {
		m[v] = true
	}
	return m
}()

func parse(src string) (ast.Node, *CompileError) {
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, compileErr(CodeSyntax, err.Error())
	}
	return tree.Node, nil
}

// visitor collects inputs; the first problem found is kept in err.
type visitor struct {
	a   *analyzer
	err *CompileError
}

func (v *visitor) fail(code, detail string) {
	if v.err == nil {
		v.err = compileErr(code, detail)
	}
}

func (v *visitor) Visit(node *ast.Node) {
	a := v.a
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		if isVar[n.Value] {
			a.p.used[n.Value] = true
		}
		if n.Value == traceFunc || n.Value == "$env" {
			v.fail(CodeSyntax, n.Value+" is reserved")
		}
	case *ast.ConditionalNode:
		mult, ok := traced(n)
		if !ok {
			return
		}
		if mult < 0 {
			v.fail(CodeNegativeMultiplier, n.String())
			return
		}
		a.p.rules = append(a.p.rules, &rule{cond: n.Cond.String(), mult: mult})
	case *ast.BinaryNode:
		switch n.Operator {
		case "<", "<=", ">", ">=", "==", "!=":
			if b, ok := lenBound(n.Left, n.Right); ok {
				a.bounds[b] = true
			} else if b, ok := lenBound(n.Right, n.Left); ok {
				a.bounds[b] = true
			}
		}
	case *ast.CallNode:
		id, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return
		}
		name := id.Value
		lit := func() (string, bool) {
			if len(n.Arguments) == 0 {
				return "", false
			}
			s, ok := n.Arguments[0].(*ast.StringNode)
			if !ok {
				return "", false
			}
			return s.Value, true
		}
		switch {
		case name == "param":
			s, ok := lit()
			if !ok || s == "" {
				v.fail(CodeLiteralArg, "param() needs a string literal path")
				return
			}
			a.params[s] = true
		case name == "header":
			s, ok := lit()
			if !ok || s == "" {
				v.fail(CodeLiteralArg, "header() needs a string literal name")
				return
			}
			a.headers[strings.ToLower(s)] = true
		case name == "u":
			s, ok := lit()
			if !ok || s == "" {
				v.fail(CodeLiteralArg, "u() needs a string literal key")
				return
			}
			a.facts[s] = true
		case name == "tier":
			if _, ok := lit(); !ok {
				v.fail(CodeLiteralArg, "tier() needs a string literal name")
			}
		case timeFuncs[name]:
			s, ok := lit()
			if !ok {
				v.fail(CodeLiteralArg, name+"() needs a string literal time zone")
				return
			}
			if _, err := loadLocation(s); err != nil {
				v.fail(CodeTimezone, fmt.Sprintf("%s(%q): %v", name, s, err))
			}
		}
	}
}

func lenBound(id, num ast.Node) (float64, bool) {
	i, ok := id.(*ast.IdentifierNode)
	if !ok || i.Value != "len" {
		return 0, false
	}
	return numLiteral(num)
}

func numLiteral(n ast.Node) (float64, bool) {
	switch x := n.(type) {
	case *ast.IntegerNode:
		return float64(x.Value), true
	case *ast.FloatNode:
		return x.Value, true
	case *ast.UnaryNode:
		if x.Operator == "-" {
			if f, ok := numLiteral(x.Node); ok {
				return -f, true
			}
		}
	}
	return 0, false
}

func (a *analyzer) walk(node ast.Node) *CompileError {
	v := &visitor{a: a}
	ast.Walk(&node, v)
	return v.err
}

// analyze collects what the expression reads, its traced rules and the
// terms of its tiers.
func (a *analyzer) analyze(src string) *CompileError {
	node, cerr := parse(src)
	if cerr != nil {
		return cerr
	}
	if err := a.walk(node); err != nil {
		return err
	}
	// Collect tier() calls and their additive terms.
	var tiers []*ast.CallNode
	collectTiers(node, &tiers)
	for _, call := range tiers {
		name := call.Arguments[0].(*ast.StringNode).Value
		if a.tierSeen[name] {
			a.p.issues = append(a.p.issues, newIssue(CodeDuplicateTier, name))
			continue
		}
		a.tierSeen[name] = true
		td := &tierDef{name: name}
		if len(call.Arguments) == 2 {
			var parts []ast.Node
			flattenSum(call.Arguments[1], &parts)
			for _, part := range parts {
				t, err := buildTerm(part)
				if err != nil {
					return err
				}
				td.terms = append(td.terms, t)
			}
		}
		a.p.tiers = append(a.p.tiers, td)
	}
	a.finish()
	return nil
}

type tierCollector struct{ out *[]*ast.CallNode }

func (c tierCollector) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	if id, ok := call.Callee.(*ast.IdentifierNode); ok && id.Value == "tier" && len(call.Arguments) > 0 {
		if _, ok := call.Arguments[0].(*ast.StringNode); ok {
			*c.out = append(*c.out, call)
		}
	}
}

// collectTiers returns tier() calls in source order. ast.Walk is
// post-order, which keeps left-to-right order for sibling calls.
func collectTiers(node ast.Node, out *[]*ast.CallNode) {
	ast.Walk(&node, tierCollector{out: out})
}

func flattenSum(n ast.Node, out *[]ast.Node) {
	if b, ok := n.(*ast.BinaryNode); ok && b.Operator == "+" {
		flattenSum(b.Left, out)
		flattenSum(b.Right, out)
		return
	}
	*out = append(*out, n)
}

type refCollector struct {
	vars  map[string]bool
	facts map[string]bool
	flat  bool
	other bool // calls other than flat/u
}

func (r *refCollector) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		if isVar[n.Value] {
			r.vars[n.Value] = true
		}
	case *ast.CallNode:
		r.visitCall(n)
	}
}

// visitCall classifies one call: flat(), u("<fact>") or anything else.
func (r *refCollector) visitCall(n *ast.CallNode) {
	id, ok := n.Callee.(*ast.IdentifierNode)
	if !ok {
		return
	}
	switch id.Value {
	case "flat":
		r.flat = true
	case "u":
		r.addFact(n)
	default:
		r.other = true
	}
}

// addFact records the literal fact of a u() call; other argument shapes are
// not fact references.
func (r *refCollector) addFact(n *ast.CallNode) {
	if len(n.Arguments) == 0 {
		return
	}
	if s, ok := n.Arguments[0].(*ast.StringNode); ok {
		r.facts[s.Value] = true
	}
}

func buildTerm(n ast.Node) (*term, *CompileError) {
	src := n.String()
	prog, err := expr.Compile(src, options(expr.AsFloat64())...)
	if err != nil {
		return nil, compileErr(CodeSyntax, err.Error())
	}
	t := &term{expr: src, prog: prog, label: "other"}
	rc := &refCollector{vars: map[string]bool{}, facts: map[string]bool{}}
	nn := n
	ast.Walk(&nn, rc)
	delete(rc.vars, "len")
	switch {
	case len(rc.vars) == 1 && len(rc.facts) == 0 && !rc.flat:
		for v := range rc.vars {
			t.varName, t.label = v, v
		}
		if b, ok := n.(*ast.BinaryNode); ok && b.Operator == "*" {
			if f, ok := numLiteral(b.Right); ok && isIdent(b.Left, t.varName) {
				t.rate = &f
			} else if f, ok := numLiteral(b.Left); ok && isIdent(b.Right, t.varName) {
				t.rate = &f
			}
		} else if isIdent(n, t.varName) {
			one := 1.0
			t.rate = &one
		}
	case len(rc.vars) == 0 && len(rc.facts) == 0 && rc.flat:
		t.label = "flat"
	case len(rc.vars) == 0 && len(rc.facts) == 1 && !rc.flat:
		for k := range rc.facts {
			t.label = "u:" + k
		}
	}
	return t, nil
}

func isIdent(n ast.Node, name string) bool {
	id, ok := n.(*ast.IdentifierNode)
	return ok && id.Value == name
}

// traceFunc is the function the compiler wraps the condition of a traced
// rule in; expressions may not call it, by name or through $env.
const traceFunc = "__rule"

// probes are the functions whose result depends on the request or its usage.
var probes = map[string]bool{"param": true, "header": true, "u": true, "hour": true, "minute": true, "weekday": true, "day": true, "month": true}

// traced reports whether a conditional is a traced rule, `cond ? m : 1`
// with a numeric literal m and a condition that reads a probe, and returns m.
func traced(n *ast.ConditionalNode) (float64, bool) {
	if f, ok := numLiteral(n.Exp2); !ok || f != 1 {
		return 0, false
	}
	m, ok := numLiteral(n.Exp1)
	if !ok {
		return 0, false
	}
	found := &probeFinder{}
	cond := n.Cond
	ast.Walk(&cond, found)
	return m, found.ok
}

type probeFinder struct{ ok bool }

func (f *probeFinder) Visit(node *ast.Node) {
	if call, ok := (*node).(*ast.CallNode); ok {
		if id, ok := call.Callee.(*ast.IdentifierNode); ok && probes[id.Value] {
			f.ok = true
		}
	}
}

// ruleTracer is the compile patch that wraps the condition of every traced
// rule in traceFunc(index, cond). ast.Walk visits in the same post-order as
// the analysis, so the indexes are those of Program.rules; wrapping keeps
// the probes of a condition, so an enclosing rule is still found.
type ruleTracer struct{ n int }

func (t *ruleTracer) Visit(node *ast.Node) {
	n, ok := (*node).(*ast.ConditionalNode)
	if !ok {
		return
	}
	if _, ok := traced(n); !ok {
		return
	}
	n.Cond = &ast.CallNode{Callee: &ast.IdentifierNode{Value: traceFunc}, Arguments: []ast.Node{&ast.IntegerNode{Value: t.n}, n.Cond}}
	t.n++
}

func (a *analyzer) finish() {
	a.p.params = sortedKeys(a.params)
	a.p.headers = sortedKeys(a.headers)
	a.p.facts = sortedKeys(a.facts)
	for b := range a.bounds {
		a.p.lenBounds = append(a.p.lenBounds, b)
	}
	sort.Float64s(a.p.lenBounds)
	if len(a.p.tiers) == 0 {
		a.p.issues = append(a.p.issues, newIssue(CodeNoTier, ""))
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- time zones

var locCache = struct {
	m map[string]*time.Location
}{m: map[string]*time.Location{}}

func loadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("an explicit IANA time zone is required")
	}
	locMu.RLock()
	l := locCache.m[name]
	locMu.RUnlock()
	if l != nil {
		return l, nil
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	locMu.Lock()
	locCache.m[name] = l
	locMu.Unlock()
	return l, nil
}
