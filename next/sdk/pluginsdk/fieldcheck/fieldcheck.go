// Package fieldcheck is the server-side check of a plugin form: the rules a
// native settings or account form shows in the console (required, length,
// pattern, bounds, enum) written once more in Go, where they are final
// (PluginService.ValidateSettings, PlatformService.ValidateCredentials).
// Every key the rules do not name is refused.
//
// Declare the rules once, as a package variable (patterns compile when the
// rule is built), and check the decoded object:
//
//	var settingsRules = fieldcheck.Fields{
//		"host":     fieldcheck.String().MaxLen(253),
//		"port":     fieldcheck.Integer().Range(1, 65535),
//		"security": fieldcheck.String().OneOf("starttls", "tls", "none"),
//		"password": fieldcheck.String().MaxLen(1000),
//	}
//
//	func (p *Plugin) ValidateSettings(ctx context.Context, cfg pluginsdk.Config) (json.RawMessage, error) {
//		var values map[string]any
//		if err := cfg.Decode(&values); err != nil {
//			return nil, err
//		}
//		return nil, fieldcheck.Check(settingsRules, values).Err()
//	}
//
// Credentials and account settings arrive as two objects; Check takes both
// and reads each field from the first that holds it.
//
// Two differences from the JSON Schema rules worth knowing: Integer without
// Range does not check the size, so a whole number past what the plugin's Go
// type holds passes here and fails only when Configure decodes it; and
// Required refuses an empty string (and null), stricter than JSON Schema's
// required, which only asks for the key.
package fieldcheck

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// Fields are the rules of an object by key.
type Fields map[string]Field

// Field is the rule of one value. Build it with String, Integer, Number,
// Bool, Array, Object or Any and narrow it with the methods; a bound not
// set is not checked.
type Field struct {
	kind     kind
	required bool
	minLen   *int
	maxLen   *int
	min      *float64
	max      *float64
	pattern  *regexp.Regexp
	enum     []string
	fields   Fields
}

type kind int

const (
	kindAny kind = iota
	kindString
	kindInteger
	kindNumber
	kindBool
	kindArray
	kindObject
)

var kindNames = map[kind]string{kindAny: "any value", kindString: "a string", kindInteger: "an integer", kindNumber: "a number",
	kindBool: "true or false", kindArray: "an array", kindObject: "an object"}

// String is a JSON string; lengths count characters (code points), as JSON
// Schema and the console's rules do.
func String() Field { return Field{kind: kindString} }

// Integer is a JSON number without a fractional part.
func Integer() Field { return Field{kind: kindInteger} }

// Number is any JSON number.
func Number() Field { return Field{kind: kindNumber} }

// Bool is true or false.
func Bool() Field { return Field{kind: kindBool} }

// Array is a JSON array; lengths count its items.
func Array() Field { return Field{kind: kindArray} }

// Object is a nested JSON object checked by fields, its unknown keys refused
// like those of the top level.
func Object(fields Fields) Field { return Field{kind: kindObject, fields: fields} }

// Any is a known key taking any value (a hidden value the plugin keeps, a
// free-form object).
func Any() Field { return Field{kind: kindAny} }

// Required: the key must be present, not null, and a string not empty.
func (f Field) Required() Field { f.required = true; return f }

// MinLen is the least length of a string or array.
func (f Field) MinLen(n int) Field { f.minLen = &n; return f }

// MaxLen is the greatest length of a string or array.
func (f Field) MaxLen(n int) Field { f.maxLen = &n; return f }

// Len bounds the length of a string or array: MinLen(min).MaxLen(max).
func (f Field) Len(min, max int) Field { return f.MinLen(min).MaxLen(max) }

// Min is the least value of a number (inclusive).
func (f Field) Min(n float64) Field { f.min = &n; return f }

// Max is the greatest value of a number (inclusive).
func (f Field) Max(n float64) Field { f.max = &n; return f }

// Range bounds a number: Min(min).Max(max).
func (f Field) Range(min, max float64) Field { return f.Min(min).Max(max) }

// Pattern is a regular expression (Go RE2 syntax) a string must match
// somewhere, as JSON Schema's pattern: anchor it with ^ and $ to match the
// whole string. It panics on an invalid expression, or on a rule that is
// not String, when the rule is built.
func (f Field) Pattern(expr string) Field {
	f.mustBeString("Pattern")
	f.pattern = regexp.MustCompile(expr)
	return f
}

// OneOf are the values a string may take. It panics on a rule that is not
// String, when the rule is built.
func (f Field) OneOf(values ...string) Field {
	f.mustBeString("OneOf")
	f.enum = values
	return f
}

// mustBeString panics when a string-only rule is put on another kind: it
// would never be checked.
func (f Field) mustBeString(rule string) {
	if f.kind != kindString {
		panic(fmt.Sprintf("fieldcheck: %s applies to String only, not %s", rule, kindNames[f.kind]))
	}
}

// Check checks objs against fields: each field is read from the first object
// holding its key; a key of any object that fields does not name is refused
// with code "unknown". Errors carry the JSON pointer of the value ("a/b"),
// one per field (its first failing rule): the fields in key order, then the
// unknown keys in key order, level by level.
func Check(fields Fields, objs ...map[string]any) pluginsdk.FieldErrors {
	return check(nil, "", fields, objs)
}

// Unknown refuses the keys of objs that known does not list (code
// "unknown"), sorted.
func Unknown(known []string, objs ...map[string]any) pluginsdk.FieldErrors {
	return unknown(nil, "", func(k string) bool { return slices.Contains(known, k) }, objs)
}

// DecodeSettings is the settings check of ValidateSettings and Configure:
// the object cfg holds is checked against fields (Check), then decoded into
// out, whose values stay where a key is absent (the plugin's defaults).
// Settings that are not a JSON object are refused at "" with code
// "invalid_json". The error is a pluginsdk.FieldErrors one.
func DecodeSettings(cfg pluginsdk.Config, fields Fields, out any) error {
	invalid := pluginsdk.FieldErrors{}.Add("", "invalid_json", "settings must be a JSON object / 设置必须是 JSON 对象").Err()
	var values map[string]any
	if err := cfg.Decode(&values); err != nil {
		return invalid
	}
	if err := Check(fields, values).Err(); err != nil {
		return err
	}
	if err := cfg.Decode(out); err != nil {
		return invalid
	}
	return nil
}

func check(errs pluginsdk.FieldErrors, at string, fields Fields, objs []map[string]any) pluginsdk.FieldErrors {
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		value, present := lookup(key, objs)
		errs = fields[key].check(errs, at+escape(key), value, present)
	}
	return unknown(errs, at, func(k string) bool { _, ok := fields[k]; return ok }, objs)
}

func unknown(errs pluginsdk.FieldErrors, at string, known func(string) bool, objs []map[string]any) pluginsdk.FieldErrors {
	var keys []string
	for _, o := range objs {
		for k := range o {
			if !known(k) && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		p := at + escape(k)
		errs = errs.Add(p, "unknown", fmt.Sprintf("%s is not a known field / %s 不是可识别的字段", p, p))
	}
	return errs
}

// check adds the first problem of the value at path p.
func (f Field) check(errs pluginsdk.FieldErrors, p string, value any, present bool) pluginsdk.FieldErrors {
	if !present || (f.required && (value == nil || value == "")) {
		if f.required {
			return errs.Add(p, "required", fmt.Sprintf("%s is required / %s 不能为空", p, p))
		}
		return errs
	}
	switch f.kind {
	case kindAny:
		return errs
	case kindString:
		s, ok := value.(string)
		if !ok {
			return f.typeError(errs, p)
		}
		return f.checkString(errs, p, s)
	case kindInteger, kindNumber:
		n, ok := number(value)
		if !ok || (f.kind == kindInteger && n != math.Trunc(n)) {
			return f.typeError(errs, p)
		}
		return f.checkNumber(errs, p, n)
	case kindBool:
		if _, ok := value.(bool); !ok {
			return f.typeError(errs, p)
		}
		return errs
	case kindArray:
		a, ok := value.([]any)
		if !ok {
			return f.typeError(errs, p)
		}
		return f.checkLen(errs, p, len(a), "minItems", "maxItems", "items", "项")
	}
	o, ok := value.(map[string]any)
	if !ok {
		return f.typeError(errs, p)
	}
	return check(errs, p+"/", f.fields, []map[string]any{o})
}

func (f Field) typeError(errs pluginsdk.FieldErrors, p string) pluginsdk.FieldErrors {
	return errs.Add(p, "type", fmt.Sprintf("%s must be %s / %s 的类型不正确", p, kindNames[f.kind], p))
}

func (f Field) checkString(errs pluginsdk.FieldErrors, p, s string) pluginsdk.FieldErrors {
	n := len(errs)
	if errs = f.checkLen(errs, p, utf8.RuneCountInString(s), "minLength", "maxLength", "characters", "个字符"); len(errs) > n {
		return errs
	}
	switch {
	case f.pattern != nil && !f.pattern.MatchString(s):
		return errs.Add(p, "pattern", fmt.Sprintf("%s has an invalid format / %s 格式不正确", p, p))
	case f.enum != nil && !slices.Contains(f.enum, s):
		return errs.Add(p, "enum", fmt.Sprintf("%s must be one of %s / %s 只能是 %s 之一", p, strings.Join(f.enum, ", "), p, strings.Join(f.enum, "、")))
	}
	return errs
}

func (f Field) checkLen(errs pluginsdk.FieldErrors, p string, n int, minCode, maxCode, unit, unitZh string) pluginsdk.FieldErrors {
	switch {
	case f.minLen != nil && n < *f.minLen:
		return errs.Add(p, minCode, fmt.Sprintf("%s needs at least %d %s / %s 至少 %d %s", p, *f.minLen, unit, p, *f.minLen, unitZh))
	case f.maxLen != nil && n > *f.maxLen:
		return errs.Add(p, maxCode, fmt.Sprintf("%s takes at most %d %s / %s 最多 %d %s", p, *f.maxLen, unit, p, *f.maxLen, unitZh))
	}
	return errs
}

func (f Field) checkNumber(errs pluginsdk.FieldErrors, p string, n float64) pluginsdk.FieldErrors {
	switch {
	case f.min != nil && n < *f.min:
		return errs.Add(p, "minimum", fmt.Sprintf("%s must be at least %s / %s 不能小于 %s", p, format(*f.min), p, format(*f.min)))
	case f.max != nil && n > *f.max:
		return errs.Add(p, "maximum", fmt.Sprintf("%s must be at most %s / %s 不能大于 %s", p, format(*f.max), p, format(*f.max)))
	}
	return errs
}

// number reads a JSON number as encoding/json decodes it (float64, or
// json.Number with UseNumber, as accountform does).
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func format(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

func lookup(key string, objs []map[string]any) (any, bool) {
	for _, o := range objs {
		if v, ok := o[key]; ok {
			return v, true
		}
	}
	return nil, false
}

var escaper = strings.NewReplacer("~", "~0", "/", "~1")

// escape writes a key as one JSON pointer token (RFC 6901).
func escape(key string) string { return escaper.Replace(key) }
