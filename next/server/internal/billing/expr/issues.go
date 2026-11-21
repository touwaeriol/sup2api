package expr

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Issue is a validation finding shown to administrators. Message holds
// {"en","zh"}; Detail carries technical context (compiler output, sample).
// Line and Column (from 1, counted in characters) locate the problem in the
// expression as submitted; zero when the issue has no position.
type Issue struct {
	Code    string            `json:"code"`
	Message map[string]string `json:"message"`
	Detail  string            `json:"detail,omitempty"`
	Line    int               `json:"line,omitempty"`
	Column  int               `json:"column,omitempty"`
}

// Issue codes.
const (
	CodeVersion            = "unsupported_version"
	CodeEmpty              = "empty_expression"
	CodeSyntax             = "syntax_error"
	CodeLiteralArg         = "literal_argument_required"
	CodeTimezone           = "invalid_timezone"
	CodeNegativeMultiplier = "negative_multiplier"
	CodeRuntime            = "runtime_error"
	CodeBadResult          = "invalid_result"
	CodeUnknownFact        = "unknown_usage_fact"
	CodeSmoke              = "smoke_test_failed"
	CodeBigCost            = "big_cost"
	CodeDuplicateTier      = "duplicate_tier"
	CodeNoTier             = "no_tier"
	CodeConfig             = "invalid_config"
)

var messages = map[string][2]string{
	CodeVersion:            {"Unsupported expression version", "不支持的表达式版本"},
	CodeEmpty:              {"Expression is empty", "表达式为空"},
	CodeSyntax:             {"Expression does not compile", "表达式无法编译"},
	CodeLiteralArg:         {"Function argument must be a string literal", "函数参数必须是字符串常量"},
	CodeTimezone:           {"Unknown time zone", "未知的时区"},
	CodeNegativeMultiplier: {"Rule multiplier must not be negative", "加价倍数不能为负数"},
	CodeRuntime:            {"Expression failed at runtime", "表达式执行出错"},
	CodeBadResult:          {"Expression result must be a finite, non-negative number", "表达式结果必须是有限的非负数"},
	CodeUnknownFact:        {"u() reads a usage key no enabled plugin declares", "u() 读取的计量值没有被任何已启用插件声明"},
	CodeSmoke:              {"Smoke test failed", "冒烟测试失败"},
	CodeBigCost:            {"Cost per million input tokens exceeds the warning threshold; please confirm", "按 100 万输入 token 计算的单次费用超过阈值，请确认"},
	CodeDuplicateTier:      {"Tier name is used more than once; the breakdown shows the first one", "档位名称重复，明细只显示第一个"},
	CodeNoTier:             {"Expression has no tier(); the matched tier will be empty", "表达式没有使用 tier()，命中档位将为空"},
	CodeConfig:             {"Invalid price configuration", "价格配置无效"},
}

func newIssue(code, detail string) Issue {
	m := messages[code]
	if m[0] == "" {
		m = [2]string{code, code}
	}
	return Issue{Code: code, Message: map[string]string{"en": m[0], "zh": m[1]}, Detail: detail}
}

func issuef(code, format string, args ...any) Issue {
	return newIssue(code, fmt.Sprintf(format, args...))
}

// placed returns i located at character offset at of the body of src (src
// without its version prefix and surrounding space). A negative offset
// leaves i unplaced.
func (i Issue) placed(src string, at int) Issue {
	if at < 0 {
		return i
	}
	_, body, _ := splitVersion(src)
	start := len(strings.TrimRightFunc(src, unicode.IsSpace)) - len(body)
	end := start
	for ; at > 0 && end < start+len(body); at-- {
		_, n := utf8.DecodeRuneInString(src[end:])
		end += n
	}
	before := src[:end]
	lineStart := strings.LastIndexByte(before, '\n') + 1
	i.Line = strings.Count(before, "\n") + 1
	i.Column = utf8.RuneCountInString(before[lineStart:]) + 1
	return i
}
