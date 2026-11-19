package jsonedit

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// object holds the member edits of one object: of the document (v, start)
// or a new one built by Set (start -1). They are resolved by render in one
// walk over the members, so removed runs and added members get exactly one
// comma between neighbours whatever the combination.
type object struct {
	v     gjson.Result
	start int // offset of the "{"
	drops []drop
	sets  []*member
}

// drop is a Delete or DeleteFunc of the object, with its place in the call
// order.
type drop struct {
	fn  func(key string, value gjson.Result) bool
	seq int
}

// member is a member Set or Default writes: raw, or a new object (sub)
// when the path goes on below a member that is missing. seq is the place
// in the call order of the Set or Default that wrote it last.
type member struct {
	key  string
	raw  []byte
	sub  *object
	keep bool // Default: only where the member is missing
	seq  int
}

// object returns the edits of v, an object of the document. For a value
// that is not an object it returns nil, failing when must is set.
func (d *Doc) object(v gjson.Result, must bool) *object {
	start, ok := d.offset(v.Raw)
	switch {
	case !ok && v.Exists():
		d.fail(ErrForeign)
		return nil
	case !v.IsObject():
		if must {
			d.fail(ErrNotObject)
		}
		return nil
	}
	if o := d.byStart[start]; o != nil {
		return o
	}
	if d.byStart == nil {
		d.byStart = map[int]*object{}
	}
	o := &object{v: v, start: start}
	d.byStart[start] = o
	d.objects = append(d.objects, o)
	return o
}

// put records keys (one or more) to be set to raw in o; seq is the edit's
// place in the call order.
func (o *object) put(keys []string, raw []byte, keep bool, seq int) {
	i := slices.IndexFunc(o.sets, func(m *member) bool { return m.key == keys[0] })
	if i < 0 {
		o.sets = append(o.sets, &member{key: keys[0], keep: keep, seq: seq})
		i = len(o.sets) - 1
	} else if keep {
		if len(keys) == 1 || o.sets[i].sub == nil {
			return // already set: Default adds nothing
		}
	}
	m := o.sets[i]
	if !keep {
		m.keep, m.seq = false, seq
	}
	if len(keys) == 1 {
		m.raw, m.sub = raw, nil
		return
	}
	if m.sub == nil {
		m.raw, m.sub = nil, &object{start: -1}
	}
	m.sub.put(keys[1:], raw, keep, seq)
}

// set returns the member a Set or Default of key wrote, or nil.
func (o *object) set(key string) *member {
	i := slices.IndexFunc(o.sets, func(m *member) bool { return m.key == key })
	if i < 0 {
		return nil
	}
	return o.sets[i]
}

// drop records a Delete or DeleteFunc made at seq. It takes effect after
// the edits made before it: a member an earlier Set or Default wrote is
// judged by the value it would leave and, when fn removes it, is not
// written, and the member of the document under that key goes too.
func (o *object) drop(fn func(string, gjson.Result) bool, seq int) {
	o.sets = slices.DeleteFunc(o.sets, func(m *member) bool {
		value, ok := o.current(m)
		if !fn(m.key, value) {
			return false
		}
		if ok { // the document's own member, which fn judges at render
			return true
		}
		key := m.key
		o.drops = append(o.drops, drop{func(k string, _ gjson.Result) bool { return k == key }, seq})
		return true
	})
	o.drops = append(o.drops, drop{fn, seq})
}

// current is the value m leaves in o: the document's own value for a
// Default of a member the document has and no earlier delete removed
// (ok), else what m writes.
func (o *object) current(m *member) (value gjson.Result, ok bool) {
	if m.keep {
		if v, n := unique(o.v, m.key); n > 0 && !o.dropped(m.key, v, m.seq) {
			return v, true
		}
	}
	return gjson.ParseBytes(m.value()), false
}

// dropped reports a member of the document that a delete made before seq
// removes.
func (o *object) dropped(key string, value gjson.Result, seq int) bool {
	return slices.ContainsFunc(o.drops, func(d drop) bool { return d.seq < seq && d.fn(key, value) })
}

// deletePath removes keys (one or more) from what the Sets and Defaults
// made before seq write in o; the document's members are not its business.
func (o *object) deletePath(keys []string, seq int) {
	if len(keys) == 1 {
		o.drop(func(k string, _ gjson.Result) bool { return k == keys[0] }, seq)
		return
	}
	if m := o.set(keys[0]); m != nil {
		m.deletePath(keys[1:], seq)
	}
}

// deletePath removes keys (one or more) from the value m writes.
func (m *member) deletePath(keys []string, seq int) {
	if m.sub != nil {
		m.sub.deletePath(keys, seq)
		return
	}
	if gjson.ParseBytes(m.raw).IsObject() {
		// m.raw is valid JSON (Set checked it): deleting cannot fail.
		m.raw, _ = Edit(m.raw, func(d *Doc) { d.Delete(strings.Join(keys, ".")) })
	}
}

// render appends the spans of o's edits: removed members go with one
// separating comma (the one before them, or after a leading run the one
// after), set members replace their value in place, and the members still
// missing are added before the "}" when the last member stays (as sjson
// adds them), else where the last removed member ended (the space that
// followed it stays before the "}"), or after the "{" of an empty object.
func (d *Doc) render(o *object, spans []span) []span {
	found := make([]bool, len(o.sets))
	end := o.start + 1   // where added members go
	keptEnd := -1        // end of the last kept member, -1 before the first
	cut, cutEnd := -1, 0 // the run of removed members since then; cut -1: none
	o.v.ForEach(func(k, v gjson.Result) bool {
		keyStart, _ := d.offset(k.Raw)
		valueStart, _ := d.offset(v.Raw)
		end = valueStart + len(v.Raw)
		// A delete made after a Set of the member judged the value the Set
		// wrote, not this one.
		seq := math.MaxInt
		if m := o.set(k.Str); m != nil && !m.keep {
			seq = m.seq
		}
		if o.dropped(k.Str, v, seq) {
			if cut < 0 {
				cut = keyStart
				if keptEnd >= 0 {
					cut = keptEnd // with the comma before it
				}
			}
			cutEnd = end
			return true
		}
		if cut >= 0 {
			to := cutEnd
			if keptEnd < 0 {
				to = keyStart // a leading run: with the comma after it
			}
			spans, cut = append(spans, span{start: cut, end: to}), -1
		}
		keptEnd = end
		i := slices.IndexFunc(o.sets, func(m *member) bool { return m.key == k.Str })
		if i < 0 {
			return true
		}
		m := o.sets[i]
		if found[i] && !m.keep {
			d.fail(fmt.Errorf("%w: %q", ErrDuplicateKey, k.Str))
			return false
		}
		found[i] = true
		if m.keep {
			return true
		}
		if text := m.value(); v.Raw != string(text) {
			spans = append(spans, span{valueStart, end, text})
		}
		return true
	})
	if cut >= 0 {
		spans = append(spans, span{start: cut, end: cutEnd})
	}
	var add []byte
	for i, m := range o.sets {
		if found[i] {
			continue
		}
		if keptEnd >= 0 || len(add) > 0 {
			add = append(add, ',')
		}
		add = m.appendTo(add)
	}
	if add != nil {
		if keptEnd == end { // after a kept last member: before the "}", as sjson does
			end = o.start + strings.LastIndexByte(o.v.Raw, '}')
		}
		spans = append(spans, span{start: end, end: end, text: add})
	}
	return spans
}

// appendTo appends m as "key":value.
func (m *member) appendTo(dst []byte) []byte {
	return append(append(gjson.AppendJSONString(dst, m.key), ':'), m.value()...)
}

// value is the value of m: raw, or the new object.
func (m *member) value() []byte {
	if m.sub == nil {
		return m.raw
	}
	dst := []byte{'{'}
	for i, s := range m.sub.sets {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = s.appendTo(dst)
	}
	return append(dst, '}')
}
