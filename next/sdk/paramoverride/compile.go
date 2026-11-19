// Package paramoverride is the request parameter override engine (CONTRACTS
// §84): it rewrites the upstream request body and headers of one account. The
// format is the one of a new-api channel's param_override: a flat object whose
// top-level keys are set literally, or {"operations": [...]} where every other
// top-level key is such a literal set applied first. An override is compiled
// once, when it is saved or the account is loaded, and Apply (apply.go) runs it
// for every upstream attempt.
//
// The account's request header override (CONTRACTS §108, header_override.go)
// compiles here too, in the format of a new-api channel's header_override:
// what it resolves for a request is the initial header state of the
// parameter override's run (Context.InitialHeaders).
//
// It lives in the SDK, not in the core, because a manifest carries the same
// format as the value of a param_override control: manifest/check checks it
// wherever a manifest is checked - the installing host, the packaging CLI, a
// plugin's own tests - with the parser the core runs, so a manifest that
// passes there never fails to compile in the core.
package paramoverride

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const (
	// MaxBytes caps the serialized override of one account.
	MaxBytes = 64 << 10
	// MaxOperations caps its operations.
	MaxOperations = 100
	// maxDescription caps the label the console stores on an operation.
	maxDescription = 200
)

// The logic of a list of conditions; an operation's default is OR,
// prune_objects' AND (as new-api).
const (
	logicAND = "AND"
	logicOR  = "OR"
)

// The message of a header value that cannot be sent.
const (
	controlCharsEn = "must not contain control characters"
	controlCharsZh = "不能包含控制字符"
)

// Override is a compiled override. nil means none.
type Override struct {
	raw    json.RawMessage
	legacy []paramSet
	ops    []paramOp
	// broken is set on a stored override that no longer compiles: every
	// Apply fails rather than sending the request unchanged.
	broken string
}

// paramSet is a literal top-level key (escaped into a path) and its value.
type paramSet struct{ path, raw string }

type paramOp struct {
	label string // "operations[2] (trim_prefix)", names the operation in errors
	conds []paramCond
	all   bool // logic AND; OR by default
	run   func(*overrideRun) error
}

type paramCond struct {
	path, mode          string
	value               gjson.Result
	re                  *regexp.Regexp // mode regex, compiled when saved
	invert, passMissing bool
}

// Error is what a return_error operation answers the client.
type Error struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	SkipRetry bool   `json:"skip_retry"`
}

func (e *Error) Error() string { return e.Message }

var paramConditionModes = map[string]bool{"full": true, "prefix": true, "suffix": true, "contains": true,
	"regex": true, "gt": true, "gte": true, "lt": true, "lte": true}

// Raw is the override as saved (compact JSON); null when there is none.
func (p *Override) Raw() json.RawMessage {
	if p == nil {
		return json.RawMessage("null")
	}
	return p.raw
}

// Load compiles a stored override. One that no longer compiles
// (it was validated when saved) fails every request of the account.
func Load(raw []byte) *Override {
	p, errs := Compile("param_override", raw, false)
	if len(errs) > 0 {
		return &Override{raw: raw, broken: "stored override is invalid: " + errs[0].Message}
	}
	return p
}

// FieldError is one problem of an override: field is the field name the
// caller passed to Compile, the message names the location in the override.
// It has the shape of the core's and manifest/check's field errors, which
// convert it field by field.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Compile validates and compiles an override. null, {} and an override
// without operations or keys compile to nil (none). Errors are field errors
// on field, their messages naming the location in the override, e.g.
// "operations[2].path: required"; zh picks the Chinese wording (the caller
// knows the request locale, this package does not).
func Compile(field string, raw []byte, zh bool) (*Override, []FieldError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	c := &overrideCompiler{zh: zh, field: field}
	root, ok := c.object(raw, "the override", "参数覆盖")
	if !ok {
		return nil, c.errs
	}
	p := &Override{}
	root.ForEach(func(k, v gjson.Result) bool {
		if k.Str == "operations" {
			p.ops = c.operations(v)
		} else {
			p.legacy = append(p.legacy, paramSet{path: escapePathKey(k.Str), raw: v.Raw})
		}
		return true
	})
	if len(c.errs) > 0 {
		return nil, c.errs
	}
	if len(p.legacy) == 0 && len(p.ops) == 0 {
		return nil, nil
	}
	p.raw = compact(raw)
	return p, nil
}

// object parses raw as a JSON object of at most MaxBytes; what names it in
// the error of one too large.
func (c *overrideCompiler) object(raw []byte, what, whatZh string) (gjson.Result, bool) {
	if len(raw) > MaxBytes {
		c.fail("too_large", "", what+" is larger than 64 KiB", whatZh+"超过 64 KiB")
		return gjson.Result{}, false
	}
	root := gjson.ParseBytes(raw)
	if !json.Valid(raw) || !root.IsObject() {
		c.fail("invalid", "", "must be a JSON object", "必须是 JSON 对象")
		return gjson.Result{}, false
	}
	return root, true
}

// compact is valid JSON without insignificant space.
func compact(raw []byte) json.RawMessage {
	var buf bytes.Buffer
	_ = json.Compact(&buf, raw)
	return buf.Bytes()
}

type overrideCompiler struct {
	zh    bool
	field string
	errs  []FieldError
}

func (c *overrideCompiler) fail(code, loc, en, zh string) {
	msg := en
	if c.zh {
		msg = zh
	}
	if loc != "" {
		msg = loc + ": " + msg
	}
	c.errs = append(c.errs, FieldError{Field: c.field, Code: code, Message: msg})
}

func (c *overrideCompiler) required(loc string) {
	c.fail("required", loc, "required", "必填")
}

// str reads an optional string member.
func (c *overrideCompiler) str(o gjson.Result, loc, key string) string {
	v := o.Get(key)
	if v.Exists() && v.Type != gjson.String && v.Type != gjson.Null {
		c.fail("invalid", loc+"."+key, "must be a string", "必须是字符串")
	}
	return v.Str
}

// boolean reads an optional boolean member.
func (c *overrideCompiler) boolean(o gjson.Result, loc, key string) bool {
	return c.flag(loc+"."+key, o.Get(key))
}

func (c *overrideCompiler) flag(loc string, v gjson.Result) bool {
	if v.Exists() && v.Type != gjson.True && v.Type != gjson.False && v.Type != gjson.Null {
		c.fail("invalid", loc, "must be true or false", "必须是 true 或 false")
	}
	return v.Bool()
}

func (c *overrideCompiler) operations(v gjson.Result) []paramOp {
	if !v.IsArray() {
		c.fail("invalid", "operations", "must be an array", "必须是数组")
		return nil
	}
	items := v.Array()
	if len(items) > MaxOperations {
		c.fail("too_many", "operations", "at most 100 operations", "最多 100 个操作")
		return nil
	}
	ops := make([]paramOp, 0, len(items))
	for i, item := range items {
		ops = append(ops, c.operation("operations["+strconv.Itoa(i)+"]", item))
	}
	return ops
}

// operation compiles one operation. Members it does not know (such as the
// console's description) are kept in the stored JSON and ignored here.
func (c *overrideCompiler) operation(loc string, o gjson.Result) paramOp {
	if !o.IsObject() {
		c.fail("invalid", loc, "must be an object", "必须是对象")
		return paramOp{}
	}
	mode := strings.ToLower(strings.TrimSpace(c.str(o, loc, "mode")))
	path, from, to := strings.TrimSpace(c.str(o, loc, "path")), c.str(o, loc, "from"), c.str(o, loc, "to")
	keep := c.boolean(o, loc, "keep_origin")
	value := o.Get("value")
	if d := c.str(o, loc, "description"); utf8.RuneCountInString(d) > maxDescription {
		c.fail("invalid", loc+".description", "at most 200 characters", "最多 200 个字符")
	}
	op := paramOp{label: loc + " (" + mode + ")"}
	op.conds, op.all = c.conditions(o, loc, logicOR)
	switch mode {
	case "":
		c.required(loc + ".mode")
	case "set", "delete", "prepend", "append":
		c.need(loc+".path", path)
		if mode != "delete" && !value.Exists() {
			c.required(loc + ".value")
		}
		op.run = eachPath(path, pathEdit(mode, value, keep))
	case "trim_prefix", "trim_suffix", "ensure_prefix", "ensure_suffix", "trim_space", "to_lower", "to_upper", "replace", "regex_replace":
		c.need(loc+".path", path)
		edit := c.stringEdit(loc, mode, value, from, to)
		op.run = eachPath(path, func(r *overrideRun, p string) error { return r.editString(p, edit) })
	case "copy", "move":
		c.need(loc+".from", from)
		c.need(loc+".to", to)
		op.run = func(r *overrideRun) error { return r.copyValue(from, to, mode == "move") }
	case "return_error":
		e := c.returnError(loc+".value", value)
		op.run = func(*overrideRun) error { return e }
	case "set_header":
		name, set := c.header(loc+".path", path), c.headerValue(loc+".value", value)
		op.run = func(r *overrideRun) error { return r.setHeaderOp(name, keep, set) }
	case "delete_header":
		name := c.header(loc+".path", path)
		op.run = func(r *overrideRun) error { r.deleteHeader(name); return nil }
	case "copy_header", "move_header":
		src, dst := c.header(loc+".from", firstText(from, path)), c.header(loc+".to", firstText(to, path))
		op.run = func(r *overrideRun) error { return r.copyHeader(src, dst, keep, mode == "move_header") }
	case "pass_headers":
		names := c.headerList(loc+".value", value)
		op.run = func(r *overrideRun) error {
			for _, n := range names {
				if err := r.copyHeader(n, n, keep, false); err != nil {
					return err
				}
			}
			return nil
		}
	case "sync_fields":
		src, dst := c.syncTarget(loc+".from", from), c.syncTarget(loc+".to", to)
		op.run = func(r *overrideRun) error { return r.syncFields(src, dst) }
	case "prune_objects":
		spec := c.pruneSpec(loc+".value", value)
		op.run = func(r *overrideRun) error { return r.prune(path, spec) }
	default:
		c.fail("unsupported", loc+".mode", "unknown mode "+mode, "未知模式 "+mode)
	}
	return op
}

func (c *overrideCompiler) need(loc, s string) {
	if strings.TrimSpace(s) == "" {
		c.required(loc)
	}
}

// conditions compiles the conditions of an operation (or of prune_objects):
// an array of {path, mode, value, invert, pass_missing_key}, or an object of
// path → value (mode full). It also returns whether all must hold: the
// logic member, or defaultLogic without one, is AND.
func (c *overrideCompiler) conditions(o gjson.Result, loc, defaultLogic string) ([]paramCond, bool) {
	logic := strings.ToUpper(strings.TrimSpace(c.str(o, loc, "logic")))
	switch logic {
	case "":
		logic = defaultLogic
	case logicAND, logicOR:
	default:
		c.fail("invalid", loc+".logic", "must be AND or OR", "必须是 AND 或 OR")
	}
	raw, loc := o.Get("conditions"), loc+".conditions"
	var out []paramCond
	switch {
	case !raw.Exists() || raw.Type == gjson.Null:
	case raw.IsObject():
		if out = fullConditions(raw); len(out) == 0 {
			c.fail("invalid", loc, "must not be empty", "不能为空")
		}
	case raw.IsArray():
		for i, item := range raw.Array() {
			out = append(out, c.condition(loc+"["+strconv.Itoa(i)+"]", item))
		}
	default:
		c.fail("invalid", loc, "must be an array or an object", "必须是数组或对象")
	}
	return out, logic == logicAND
}

func (c *overrideCompiler) condition(loc string, o gjson.Result) paramCond {
	if !o.IsObject() {
		c.fail("invalid", loc, "must be an object", "必须是对象")
		return paramCond{}
	}
	cond := paramCond{path: strings.TrimSpace(c.str(o, loc, "path")), mode: strings.ToLower(strings.TrimSpace(c.str(o, loc, "mode"))),
		value: o.Get("value"), invert: c.boolean(o, loc, "invert"), passMissing: c.boolean(o, loc, "pass_missing_key")}
	c.need(loc+".path", cond.path)
	switch {
	case cond.mode == "":
		c.required(loc + ".mode")
	case !paramConditionModes[cond.mode]:
		c.fail("unsupported", loc+".mode", "unknown condition mode "+cond.mode, "未知条件模式 "+cond.mode)
	case cond.mode == "regex":
		cond.re = c.regex(loc+".value", cond.value)
	}
	if !cond.value.Exists() {
		cond.value = gjson.Parse("null")
	}
	return cond
}

// regex compiles the pattern of a regex condition.
func (c *overrideCompiler) regex(loc string, v gjson.Result) *regexp.Regexp {
	if v.Type != gjson.String {
		c.fail("invalid", loc, "must be a regular expression string", "必须是正则表达式字符串")
		return nil
	}
	return c.compileRegexp(loc, v.Str)
}

func (c *overrideCompiler) compileRegexp(loc, pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		c.fail("invalid", loc, "invalid regular expression: "+err.Error(), "正则表达式无效："+err.Error())
		return nil
	}
	return re
}

// stringEdit compiles the string transformation of a string mode.
func (c *overrideCompiler) stringEdit(loc, mode string, value gjson.Result, from, to string) func(string) string {
	text := value.String()
	needValue := func(nonEmpty bool) {
		if !value.Exists() || value.Type == gjson.Null || (nonEmpty && text == "") {
			c.required(loc + ".value")
		}
	}
	switch mode {
	case "trim_prefix":
		needValue(false)
		return func(s string) string { return strings.TrimPrefix(s, text) }
	case "trim_suffix":
		needValue(false)
		return func(s string) string { return strings.TrimSuffix(s, text) }
	case "ensure_prefix":
		needValue(true)
		return func(s string) string {
			if strings.HasPrefix(s, text) {
				return s
			}
			return text + s
		}
	case "ensure_suffix":
		needValue(true)
		return func(s string) string {
			if strings.HasSuffix(s, text) {
				return s
			}
			return s + text
		}
	case "trim_space":
		return strings.TrimSpace
	case "to_lower":
		return strings.ToLower
	case "to_upper":
		return strings.ToUpper
	case "replace":
		c.need(loc+".from", from)
		return func(s string) string { return strings.ReplaceAll(s, from, to) }
	}
	// regex_replace, the last string mode
	c.need(loc+".from", from)
	re := c.compileRegexp(loc+".from", from)
	if re == nil {
		return nil
	}
	return func(s string) string { return re.ReplaceAllString(s, to) }
}

// returnError compiles the value of a return_error operation: a message, or
// {message|msg, status_code|status, code, type, skip_retry}.
func (c *overrideCompiler) returnError(loc string, v gjson.Result) *Error {
	e := &Error{Status: http.StatusBadRequest, Code: "invalid_request", Type: "invalid_request_error", SkipRetry: true}
	switch {
	case v.Type == gjson.String:
		e.Message = strings.TrimSpace(v.Str)
	case v.IsObject():
		e.Message = strings.TrimSpace(firstText(c.str(v, loc, "message"), c.str(v, loc, "msg")))
		if code := strings.TrimSpace(v.Get("code").String()); code != "" {
			e.Code = code
		}
		if t := strings.TrimSpace(c.str(v, loc, "type")); t != "" {
			e.Type = t
		}
		if s := v.Get("skip_retry"); s.Exists() {
			e.SkipRetry = c.flag(loc+".skip_retry", s)
		}
		status := v.Get("status_code")
		if !status.Exists() {
			status = v.Get("status")
		}
		if status.Exists() {
			if status.Type != gjson.Number || status.Num != math.Trunc(status.Num) || status.Num < 100 || status.Num > 599 {
				c.fail("invalid", loc+".status_code", "must be an HTTP status code (100-599)", "必须是 HTTP 状态码（100-599）")
			}
			e.Status = int(status.Num)
		}
	default:
		c.fail("invalid", loc, "must be a message or an object", "必须是错误消息或对象")
		return e
	}
	if e.Message == "" {
		c.required(loc + ".message")
	}
	return e
}

// header validates a header name: lower-cased, a valid field name, and none
// that header passthrough blocks (CONTRACTS §81) - an override must not set,
// read or remove credentials, hop-by-hop, client address or gateway control
// headers.
func (c *overrideCompiler) header(loc, name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		c.required(loc)
	case !ValidHeaderName(n):
		c.fail("invalid", loc, "invalid header name "+n, "请求头名称无效 "+n)
	case HeaderBlocked(n):
		c.fail("blocked", loc, "header "+n+" cannot be overridden (credential, hop-by-hop, client address or gateway control header)",
			"请求头 "+n+" 不能覆盖（凭证、逐跳、客户端地址或网关控制头）")
	}
	return n
}

// headerList compiles the header names of pass_headers.
func (c *overrideCompiler) headerList(loc string, v gjson.Result) []string {
	raw, ok := c.headerNames(loc, v)
	if !ok {
		return nil
	}
	var names []string
	for i, n := range raw {
		if strings.TrimSpace(n) != "" {
			names = append(names, c.header(loc+"["+strconv.Itoa(i)+"]", n))
		}
	}
	if len(names) == 0 {
		c.required(loc)
	}
	return uniqueStrings(names)
}

// headerNames lists the names of a pass_headers value: an array, a
// comma-separated string, either as JSON in a string, or an object whose
// headers, names and header members hold such values (new-api's forms).
func (c *overrideCompiler) headerNames(loc string, v gjson.Result) ([]string, bool) {
	switch {
	case v.Type == gjson.String:
		s := strings.TrimSpace(v.Str)
		if (strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")) && gjson.Valid(s) {
			return c.headerNames(loc, gjson.Parse(s))
		}
		return strings.Split(s, ","), true
	case v.IsArray():
		var raw []string
		for _, item := range v.Array() {
			raw = append(raw, item.String())
		}
		return raw, true
	case v.IsObject():
		var raw []string
		for _, key := range []string{"headers", "names", "header"} {
			if m := v.Get(key); m.Exists() {
				names, _ := c.headerNames(loc+"."+key, m)
				raw = append(raw, names...)
			}
		}
		return raw, true
	}
	c.fail("invalid", loc, "must be an array, a comma-separated string or an object of header names", "必须是请求头名称数组、逗号分隔的字符串或对象")
	return nil, false
}

// syncTarget compiles a side of sync_fields: "json:<path>", "body:<path>",
// "header:<name>", or a bare JSON path.
func (c *overrideCompiler) syncTarget(loc, spec string) syncTarget {
	raw := strings.TrimSpace(spec)
	kind, key, prefixed := strings.Cut(raw, ":")
	if !prefixed {
		c.need(loc, raw)
		return syncTarget{key: raw}
	}
	key = strings.TrimSpace(key)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "json", "body":
		c.need(loc, key)
		return syncTarget{key: key}
	case "header":
		return syncTarget{key: c.header(loc, key), header: true}
	}
	c.fail("invalid", loc, "must start with json:, body: or header:", "必须以 json:、body: 或 header: 开头")
	return syncTarget{}
}

// pruneSpec compiles the value of prune_objects: a type (the objects whose
// type is it), or {logic, recursive, conditions, where, type}; the
// conditions of all three must hold unless logic is OR.
func (c *overrideCompiler) pruneSpec(loc string, v gjson.Result) pruneSpec {
	spec := pruneSpec{all: true, recursive: true}
	typeCond := func(t gjson.Result) paramCond { return paramCond{path: "type", mode: "full", value: t} }
	switch text := strings.TrimSpace(v.Str); {
	case v.Type == gjson.String && text != "":
		spec.conds = []paramCond{typeCond(gjson.Result{Type: gjson.String, Str: text})}
	case v.IsObject():
		spec.conds, spec.all = c.conditions(v, loc, logicAND)
		if r := v.Get("recursive"); r.Exists() && r.Type != gjson.Null {
			spec.recursive = c.flag(loc+".recursive", r)
		}
		spec.conds = append(spec.conds, c.where(loc+".where", v.Get("where"))...)
		if t := v.Get("type"); t.Exists() {
			spec.conds = append(spec.conds, typeCond(t))
		}
		if len(spec.conds) == 0 {
			c.fail("required", loc+".conditions", "required (conditions, where or type)", "必填（conditions、where 或 type）")
		}
	case v.Exists() && v.Type != gjson.Null && v.Type != gjson.String:
		c.fail("invalid", loc, "must be a type string or an object", "必须是类型字符串或对象")
	default:
		c.required(loc)
	}
	return spec
}

// where compiles the where member of prune_objects: path → value (mode
// full); like new-api it trims the paths and skips blank ones.
func (c *overrideCompiler) where(loc string, v gjson.Result) []paramCond {
	if !v.Exists() || v.Type == gjson.Null {
		return nil
	}
	if !v.IsObject() {
		c.fail("invalid", loc, "must be an object", "必须是对象")
		return nil
	}
	var out []paramCond
	for _, cond := range fullConditions(v) {
		if cond.path = strings.TrimSpace(cond.path); cond.path != "" {
			out = append(out, cond)
		}
	}
	return out
}

// fullConditions are the conditions of an object of path → value.
func fullConditions(o gjson.Result) []paramCond {
	var out []paramCond
	o.ForEach(func(k, v gjson.Result) bool {
		out = append(out, paramCond{path: k.Str, mode: "full", value: v})
		return true
	})
	return out
}

// headerValue compiles the value of set_header into a function of the
// header's current value: a fixed string, or a token mapping (see
// headerTokens). An empty result removes the header.
func (c *overrideCompiler) headerValue(loc string, v gjson.Result) func(current string) string {
	switch {
	case !v.Exists() || v.Type == gjson.Null:
		c.required(loc)
	case v.IsObject():
		return c.headerTokens(loc, v).apply
	case v.IsArray():
		c.fail("invalid", loc, "must be a string or a token mapping object", "必须是字符串或令牌映射对象")
	}
	text := c.headerText(loc, strings.TrimSpace(v.String()))
	return func(string) string { return text }
}

// headerText checks that a header value of the override can be sent.
func (c *overrideCompiler) headerText(loc, s string) string {
	if !ValidHeaderValue(s) {
		c.fail("invalid", loc, controlCharsEn, controlCharsZh)
	}
	return s
}

// headerTokens compiles a token mapping of set_header, applied to the
// comma-separated tokens of the header's current value (how anthropic-beta
// is edited): "<token>" → replacement (string, array or null to drop),
// "*" → replacement of every undeclared token, "$append" → tokens added at
// the end, "$keep_only_declared": true drops undeclared tokens.
func (c *overrideCompiler) headerTokens(loc string, v gjson.Result) *headerTokenMap {
	m := &headerTokenMap{tokens: map[string][]string{}}
	v.ForEach(func(k, item gjson.Result) bool {
		switch k.Str {
		case "$keep_only_declared":
			m.keepOnly = c.flag(loc+"."+k.Str, item)
		case "$append":
			m.append = c.tokens(loc+"."+k.Str, item)
		case "*":
			m.wildcard, m.hasWildcard = c.tokens(loc+".*", item), true
		default:
			m.tokens[k.Str] = c.tokens(loc+"."+k.Str, item)
		}
		return true
	})
	if len(m.tokens) == 0 && !m.hasWildcard && m.append == nil && !m.keepOnly {
		c.fail("invalid", loc, "token mapping must not be empty", "令牌映射不能为空")
	}
	return m
}

func (c *overrideCompiler) tokens(loc string, v gjson.Result) []string {
	items := []gjson.Result{v}
	if v.IsArray() {
		items = v.Array()
	}
	out := []string{}
	for _, item := range items {
		if item.IsObject() || item.IsArray() {
			c.fail("invalid", loc, "must be a string, an array of strings or null", "必须是字符串、字符串数组或 null")
			continue
		}
		out = append(out, splitTokens(c.headerText(loc, item.String()))...)
	}
	return uniqueStrings(out)
}

type headerTokenMap struct {
	tokens      map[string][]string
	wildcard    []string
	hasWildcard bool
	append      []string
	keepOnly    bool
}

func (m *headerTokenMap) apply(current string) string {
	var out []string
	for _, t := range splitTokens(current) {
		repl, ok := m.tokens[t]
		if !ok && m.hasWildcard && !m.keepOnly {
			repl, ok = m.wildcard, true
		}
		switch {
		case ok:
			out = append(out, repl...)
		case !m.keepOnly:
			out = append(out, t)
		}
	}
	return strings.Join(uniqueStrings(append(out, m.append...)), ",")
}

func splitTokens(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// uniqueStrings drops repeated strings, keeping the first of each.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstText(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// pathSpecial are the characters gjson and sjson read as path syntax:
// separators, wildcards, the escape, queries and arrays (#), modifiers (@)
// and pipes (|).
const pathSpecial = `.*?\#@|`

// escapePathKey makes key one literal gjson/sjson path segment.
func escapePathKey(key string) string {
	if !strings.ContainsAny(key, pathSpecial) {
		return key
	}
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		if strings.IndexByte(pathSpecial, key[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(key[i])
	}
	return b.String()
}
