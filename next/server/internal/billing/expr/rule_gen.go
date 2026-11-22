package expr

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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
	if c.Source == "time" {
		if c.Key != "" || c.Op != "" || hasValue {
			return "", cfgErr(field, "a time condition has tz, from_hour and to_hour only")
		}
		return timeCond(field, c)
	}
	if c.TZ != "" || c.FromHour != nil || c.ToHour != nil {
		return "", cfgErr(field, "only a time condition has tz, from_hour and to_hour")
	}
	arg, err := condArg(field, c)
	if err != nil {
		return "", err
	}
	switch c.Op {
	case "contains":
		return containsCond(field, arg, c.Value)
	case "exists":
		if hasValue {
			return "", cfgErr(field+".value", "exists takes no value")
		}
		return arg + " != " + absent[c.Source], nil
	case "eq", "ne":
		return equalCond(field, arg, c)
	case "gt", "gte", "lt", "lte":
		return orderCond(field, arg, c)
	}
	return "", cfgErr(field+".op", "unknown op %q", c.Op)
}

// containsCond is `has(arg, "value")`.
func containsCond(field, arg string, value json.RawMessage) (string, *ConfigError) {
	s, err := stringValue(field, value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("has(%s, %s)", arg, strconv.Quote(s)), nil
}

// equalCond is `arg == value` or `arg != value`; a header compares as text.
func equalCond(field, arg string, c CondConfig) (string, *ConfigError) {
	var lit string
	var err *ConfigError
	if c.Source == "header" {
		var s string
		s, err = stringValue(field, c.Value)
		lit = strconv.Quote(s)
	} else {
		lit, err = literal(field, c.Value)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s", arg, compareOps[c.Op], lit), nil
}

// orderCond compares a param or usage value with a number; an absent param
// reads 0.
func orderCond(field, arg string, c CondConfig) (string, *ConfigError) {
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
