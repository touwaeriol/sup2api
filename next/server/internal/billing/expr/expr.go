// Package expr implements the v1 price expression language (ARCHITECTURE
// 7.3). It is a pure package: no database, no network, no clock except the
// request time passed in by the caller.
//
// An expression, optionally prefixed "v1:", computes the price in
// "micro-dollars" (token coefficients are USD per million tokens, so the
// value divided by 1e6 is USD). A request surcharge is written in the
// language itself, `(base) * (cond ? multiplier : 1)`: every conditional of
// the shape `cond ? <numeric literal> : 1` whose condition reads the request
// or the usage (param, header, u, hour, minute, weekday, day, month) is a
// traced rule, reported as matched or not by every evaluation.
//
// The engine is built on github.com/expr-lang/expr. The design follows the
// ideas of new-api's billing expressions, but the implementation is original.
package expr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Version1 is the only expression language version so far.
const Version1 = 1

// Scale converts an expression value to USD (value / Scale). flat(x) returns
// x * Scale so that it cancels out.
const Scale = 1_000_000

// TokenVars are the token variables of v1, in display order.
var TokenVars = []string{"p", "c", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao"}

var versionPrefix = regexp.MustCompile(`^v(\d+)\s*:`)

// splitVersion strips an optional "vN:" prefix.
func splitVersion(src string) (version int, body string, err error) {
	s := strings.TrimSpace(src)
	m := versionPrefix.FindStringSubmatch(s)
	if m == nil {
		return Version1, s, nil
	}
	v, _ := strconv.Atoi(m[1])
	if v != Version1 {
		return 0, "", fmt.Errorf("unsupported expression version v%d", v)
	}
	return v, strings.TrimSpace(s[len(m[0]):]), nil
}

// Hash returns the canonical hash of an expression: sha256 hex of
// "v<version>:<body>" with surrounding whitespace removed, so "v1:x" and
// "x" hash the same. Unsupported versions hash their raw text.
func Hash(src string) string {
	v, body, err := splitVersion(src)
	canon := strings.TrimSpace(src)
	if err == nil {
		canon = fmt.Sprintf("v%d:%s", v, body)
	}
	sum := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- program

// Program is a compiled, analysed expression. It is immutable and safe for
// concurrent use.
type Program struct {
	version int
	source  string // canonical "v1:<body>"
	hash    string

	prog  *vm.Program
	rules []*rule    // traced rules, in evaluation-trace order
	tiers []*tierDef // tier() calls, source order

	used      map[string]bool // token variables and "len"
	params    []string
	headers   []string
	facts     []string
	factAt    map[string]int // character offset of each fact's first u() in the body
	lenBounds []float64
	issues    []Issue // non-fatal findings (warnings) from analysis
}

// rule is a traced request rule: `cond ? mult : 1`.
type rule struct {
	cond string // canonical condition text
	mult float64
}

type tierDef struct {
	name  string
	terms []*term
}

type term struct {
	label   string   // variable name, "flat", "u:<key>" or "other"
	varName string   // token variable when the term prices exactly one variable
	rate    *float64 // coefficient when the term is `var * number`
	expr    string
	prog    *vm.Program
}

// Hash is the canonical expression hash (see Hash).
func (p *Program) Hash() string { return p.hash }

// Version is the language version.
func (p *Program) Version() int { return p.version }

// Expression is the canonical source "v1:<body>".
func (p *Program) Expression() string { return p.source }

// Uses reports whether the expression reads a token variable (or "len").
func (p *Program) Uses(name string) bool { return p.used[name] }

// UsedVars lists the token variables (and "len") the expression reads.
func (p *Program) UsedVars() []string {
	var out []string
	for _, v := range append(append([]string{}, TokenVars...), "len") {
		if p.used[v] {
			out = append(out, v)
		}
	}
	return out
}

// Params lists the request body paths read through param().
func (p *Program) Params() []string { return append([]string(nil), p.params...) }

// Headers lists the lower-case header names read through header().
func (p *Program) Headers() []string { return append([]string(nil), p.headers...) }

// Facts lists the plugin metering keys read through u().
func (p *Program) Facts() []string { return append([]string(nil), p.facts...) }

// TierNames lists the literal tier names in source order (deduplicated).
func (p *Program) TierNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range p.tiers {
		if !seen[t.name] {
			seen[t.name] = true
			out = append(out, t.name)
		}
	}
	return out
}

// RuleConds lists the traced request rule conditions.
func (p *Program) RuleConds() []string {
	out := make([]string, len(p.rules))
	for i, r := range p.rules {
		out[i] = r.cond
	}
	return out
}

// LenBoundaries lists numeric literals compared against len.
func (p *Program) LenBoundaries() []float64 { return append([]float64(nil), p.lenBounds...) }

// ---------------------------------------------------------------- compile

var builtins = []string{"max", "min", "abs", "ceil", "floor"}

func options(extra ...expr.Option) []expr.Option {
	ops := []expr.Option{expr.Env(env{}), expr.DisableAllBuiltins(), expr.MaxNodes(5000)}
	for _, b := range builtins {
		ops = append(ops, expr.EnableBuiltin(b))
	}
	return append(ops, extra...)
}

// CompileError describes why an expression failed to compile.
type CompileError struct {
	Issue Issue
	at    int // character offset of the problem in the body; -1 when unknown
}

func (e *CompileError) Error() string {
	if e.Issue.Detail != "" {
		return e.Issue.Message["en"] + ": " + e.Issue.Detail
	}
	return e.Issue.Message["en"]
}

func compileErr(code, detail string) *CompileError {
	return compileErrAt(code, detail, -1)
}

func compileErrAt(code, detail string, at int) *CompileError {
	return &CompileError{Issue: newIssue(code, detail), at: at}
}

// Compile parses, checks and analyses an expression. Errors are
// *CompileError, located in src when the problem has a position.
func Compile(src string) (*Program, error) {
	p, cerr := compile(src)
	if cerr != nil {
		cerr.Issue = cerr.Issue.placed(src, cerr.at)
		return nil, cerr
	}
	return p, nil
}

func compile(src string) (*Program, *CompileError) {
	version, body, err := splitVersion(src)
	if err != nil {
		return nil, compileErr(CodeVersion, err.Error())
	}
	if body == "" {
		return nil, compileErr(CodeEmpty, "")
	}
	p := &Program{
		version: version,
		source:  fmt.Sprintf("v%d:%s", version, body),
		used:    map[string]bool{},
	}
	p.hash = Hash(p.source)
	tracer := &ruleTracer{}
	if p.prog, err = expr.Compile(body, options(expr.AsFloat64(), expr.Patch(tracer))...); err != nil {
		return nil, syntaxErr(body, err)
	}
	if err := newAnalyzer(p).analyze(body); err != nil {
		return nil, err
	}
	if tracer.n != len(p.rules) {
		return nil, compileErr(CodeSyntax, fmt.Sprintf("traced %d rules, analysed %d", tracer.n, len(p.rules)))
	}
	return p, nil
}

// ---------------------------------------------------------------- cache

const maxCached = 4096

var (
	cacheMu sync.RWMutex
	cache   = map[string]*Program{}
)

// CompileCached compiles through a process-wide cache keyed by Hash.
func CompileCached(src string) (*Program, error) {
	h := Hash(src)
	cacheMu.RLock()
	p := cache[h]
	cacheMu.RUnlock()
	if p != nil {
		return p, nil
	}
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	if len(cache) >= maxCached {
		cache = map[string]*Program{}
	}
	cache[h] = p
	cacheMu.Unlock()
	return p, nil
}
