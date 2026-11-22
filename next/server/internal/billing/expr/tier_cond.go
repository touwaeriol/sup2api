package expr

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
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
	v, err := condValue(field+".value", c.Value)
	if err != nil {
		return "", err
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

// condValue is the finite value of a comparison; -0 is written 0, as the
// console's generator writes it.
func condValue(field string, value *Num) (float64, *ConfigError) {
	if value == nil {
		return 0, cfgErr(field, "required")
	}
	v := float64(*value)
	if v == 0 {
		v = 0
	}
	if math.IsNaN(v) || math.IsInf(v, 0) { // "NaN" and "Inf" parse as numeric strings
		return 0, cfgErr(field, "%s", condKeyErrs["value"])
	}
	return v, nil
}
