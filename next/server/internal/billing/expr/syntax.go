package expr

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser/lexer"
)

// syntaxErr explains in Chinese why expr-lang rejected body, at the
// position of the problem: an unbalanced parenthesis, an unknown name (with
// the nearest known one), or else expr-lang's own message.
func syntaxErr(body string, err error) *CompileError {
	if cerr := parenErr(body); cerr != nil {
		return cerr
	}
	var fe *file.Error
	if !errors.As(err, &fe) {
		return compileErr(CodeSyntax, "表达式写法不对："+err.Error())
	}
	msg := strings.TrimSpace(fe.Message)
	if name, ok := strings.CutPrefix(msg, "unknown name "); ok {
		return compileErrAt(CodeSyntax, unknownName(name), fe.From)
	}
	return compileErrAt(CodeSyntax, "表达式写法不对："+msg, fe.From)
}

// parenErr finds the first unbalanced parenthesis of body, using expr-lang's
// lexer so that strings and comments are skipped; nil when they balance or
// body does not lex.
func parenErr(body string) *CompileError {
	tokens, err := lexer.Lex(file.NewSource(body))
	if err != nil {
		return nil
	}
	var open []int
	for _, t := range tokens {
		if !t.Is(lexer.Bracket, "(", ")") {
			continue
		}
		if t.Value == "(" {
			open = append(open, t.From)
			continue
		}
		if len(open) == 0 {
			return compileErrAt(CodeSyntax, "多了一个右括号", t.From)
		}
		open = open[:len(open)-1]
	}
	if len(open) > 0 {
		return compileErrAt(CodeSyntax, "这个左括号没有配对的右括号", open[len(open)-1])
	}
	return nil
}

// knownNames are the variables and functions an expression may use (the
// names of env, then the builtins), the candidates for a suggestion.
var knownNames = func() []string {
	t := reflect.TypeFor[env]()
	var out []string
	for i := range t.NumField() {
		if name := t.Field(i).Tag.Get("expr"); name != traceFunc {
			out = append(out, name)
		}
	}
	return append(out, builtins...)
}()

// unknownName says name is not known, suggesting the nearest known name
// within two edits (the first one on a tie) that keeps some of name: "x" is
// one edit from "p" but nothing like it.
func unknownName(name string) string {
	best, near := min(3, utf8.RuneCountInString(name)), ""
	for _, k := range knownNames {
		if d := editDistance(name, k); d < best {
			best, near = d, k
		}
	}
	if near == "" {
		return "不认识的名字 " + name
	}
	return fmt.Sprintf("不认识的名字 %s，是不是 %s？", name, near)
}

// editDistance is the Levenshtein distance of a and b, in characters.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	d := make([]int, len(y)+1)
	for j := range d {
		d[j] = j
	}
	for i := 1; i <= len(x); i++ {
		prev := d[0]
		d[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			prev, d[j] = d[j], min(d[j]+1, d[j-1]+1, prev+cost)
		}
	}
	return d[len(y)]
}
