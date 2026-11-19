// Package jsonedit edits a JSON document in place of the bytes each edit
// names and copies everything else as it is: unknown fields, key order,
// white space, number formatting and \u escapes stay byte for byte. It is
// the one body editor of the plugins: a plugin receives the full request
// body and returns the final bytes, and an edit must neither re-encode the
// document nor copy it once per change.
//
// A Doc is opened on the body (Parse, or Edit for the usual parse, edit,
// return), the edits are recorded against the offsets of the original
// bytes, and Bytes applies all of them in one pass into one buffer of the
// final size. Reading is gjson's: Root and the values it returns locate
// what to edit, and only values read from the Doc can be edited.
//
//	out, err := jsonedit.Edit(body, func(d *jsonedit.Doc) {
//		d.Delete("metadata.internal")
//		d.Set("stream_options.include_usage", []byte("true"))
//	})
//
// Rules:
//
//   - Invalid JSON is refused by Parse (ErrInvalid); a value handed to an
//     edit must be valid JSON too. The result is therefore valid JSON.
//   - A batch that changes nothing returns the original slice, not a copy.
//   - Duplicate keys: Delete and DeleteFunc remove every member they match,
//     however often its key appears. Set needs one member to change: a key
//     on its path that appears more than once in its object is
//     ErrDuplicateKey and nothing is returned. Keys elsewhere are not the
//     edit's business and stay as they are. (The core refuses bodies with
//     duplicate keys before a plugin runs; the rule keeps an edit from
//     guessing if one gets through.)
//   - Edits of one member take effect in the order they are made: a Delete
//     or DeleteFunc removes what an earlier Set or Default wrote at its
//     path (DeleteFunc judges the value the member would have then), and a
//     Set or Default after a Delete writes the member again, at the end of
//     its object.
//   - Edits whose bytes overlap (a member deleted and a value inside it
//     replaced, a value replaced twice) are ErrConflict.
//   - Paths are dot-separated plain object keys ("stream_options.include_usage");
//     there is no array index or wildcard syntax: array elements are read
//     (EachElement) and edited as values (Replace, Object).
package jsonedit

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
)

var (
	// ErrInvalid: the document, or a value given to an edit, is not valid
	// JSON.
	ErrInvalid = errors.New("jsonedit: not valid JSON")
	// ErrNotObject: a member was to be set in a value that is not an object.
	ErrNotObject = errors.New("jsonedit: not a JSON object")
	// ErrDuplicateKey: a key Set changes appears more than once in its
	// object.
	ErrDuplicateKey = errors.New("jsonedit: key appears more than once in its object")
	// ErrConflict: two edits touch the same bytes.
	ErrConflict = errors.New("jsonedit: edits overlap")
	// ErrForeign: an edited value was not read from this document.
	ErrForeign = errors.New("jsonedit: value not read from this document")
)

// Doc is a JSON document being edited. It reads the bytes it was opened on
// in place: they must not change while the Doc is in use.
type Doc struct {
	src     []byte
	root    gjson.Result
	spans   []span          // value replacements
	objects []*object       // objects with member edits, in the order first edited
	byStart map[int]*object // the same, by the offset of their "{"
	seq     int             // member edits made so far: the call order
	err     error           // the first error; Bytes returns it
}

// span replaces src[start:end] with text; start == end inserts.
type span struct {
	start, end int
	text       []byte
}

// Parse opens body for editing. It fails with ErrInvalid unless body is one
// valid JSON value.
func Parse(body []byte) (*Doc, error) {
	if !gjson.ValidBytes(body) {
		return nil, ErrInvalid
	}
	return &Doc{src: body, root: gjson.Parse(unsafe.String(unsafe.SliceData(body), len(body)))}, nil
}

// Edit parses body, lets edit record its edits and returns the result of
// Bytes.
func Edit(body []byte, edit func(*Doc)) ([]byte, error) {
	d, err := Parse(body)
	if err != nil {
		return nil, err
	}
	edit(d)
	return d.Bytes()
}

// Root is the document's top-level value.
func (d *Doc) Root() gjson.Result { return d.root }

// Set sets path below the root object; see Object.Set.
func (d *Doc) Set(path string, raw []byte) { d.Object(d.root).Set(path, raw) }

// Default sets path below the root object where nothing is; see
// Object.Default.
func (d *Doc) Default(path string, raw []byte) { d.Object(d.root).Default(path, raw) }

// Delete removes path below the root object; see Object.Delete.
func (d *Doc) Delete(path string) { d.Object(d.root).Delete(path) }

// Replace replaces v, a value read from the document, with raw (valid
// JSON). Replacing a value with the same bytes is no edit.
func (d *Doc) Replace(v gjson.Result, raw []byte) {
	start, ok := d.offset(v.Raw)
	switch {
	case !ok:
		d.fail(ErrForeign)
	case !gjson.ValidBytes(raw):
		d.fail(fmt.Errorf("%w: replacement %.40q", ErrInvalid, raw))
	case v.Raw != string(raw):
		d.spans = append(d.spans, span{start, start + len(v.Raw), raw})
	}
}

// Object is an object of the document whose members are edited.
type Object struct {
	d *Doc
	v gjson.Result
}

// Object addresses v, a value read from the document, for member edits.
// When v is not an object, deleting from it does nothing and setting in it
// is ErrNotObject.
func (d *Doc) Object(v gjson.Result) Object { return Object{d, v} }

// Set sets the member at path (dot-separated keys, relative to the object)
// to raw (valid JSON). An existing value is replaced in place; a missing
// member is added at the end of its object, with the objects missing on
// the way, and members set in one object are added in the order set. A
// value on the way that is not an object is replaced by one. A key on the
// path that appears twice in its object is ErrDuplicateKey. The last Set of
// a path wins.
func (o Object) Set(path string, raw []byte) { o.d.set(o.v, path, raw, false) }

// Default is Set where nothing is: it adds the member at path when it is
// missing and changes no existing value (null included), on the way or at
// the end.
func (o Object) Default(path string, raw []byte) { o.d.set(o.v, path, raw, true) }

// Delete removes the member at path, every occurrence of it: on the way,
// every member with the key that is an object is searched. A missing path
// is no edit. A member removed takes one separating comma with it. What an
// earlier Set or Default wrote at path is removed too.
func (o Object) Delete(path string) {
	keys, err := splitPath(path)
	if err != nil {
		o.d.fail(err)
		return
	}
	o.d.delete(o.v, keys, o.d.next())
}

// DeleteFunc removes every member of the object for which drop reports
// true. A member an earlier Set or Default wrote is judged by the value it
// would leave.
func (o Object) DeleteFunc(drop func(key string, value gjson.Result) bool) {
	if obj := o.d.object(o.v, false); obj != nil {
		obj.drop(drop, o.d.next())
	}
}

// EachElement calls fn with each element of array, and does nothing for
// any other value (gjson's ForEach would call fn with the value itself).
func EachElement(array gjson.Result, fn func(gjson.Result)) {
	if !array.IsArray() {
		return
	}
	array.ForEach(func(_, v gjson.Result) bool {
		fn(v)
		return true
	})
}

// Bytes applies the edits and returns the document, or the first error an
// edit met. Without edits it returns the original slice; otherwise one new
// slice of the final size.
func (d *Doc) Bytes() ([]byte, error) {
	spans := slices.Clone(d.spans)
	for _, o := range d.objects {
		spans = d.render(o, spans)
	}
	if d.err != nil {
		return nil, d.err
	}
	if len(spans) == 0 {
		return d.src, nil
	}
	slices.SortStableFunc(spans, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end))
	})
	size := len(d.src)
	for _, s := range spans {
		size += len(s.text) - (s.end - s.start)
	}
	out, at := make([]byte, 0, size), 0
	for _, s := range spans {
		if s.start < at {
			return nil, ErrConflict
		}
		out = append(append(out, d.src[at:s.start]...), s.text...)
		at = s.end
	}
	return append(out, d.src[at:]...), nil
}

// next is the place of a member edit in the call order.
func (d *Doc) next() int {
	d.seq++
	return d.seq
}

func (d *Doc) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// offset is where raw, a string gjson read from the document, starts in it;
// ok is false for a string that is not part of the document (a value of
// another document, or one gjson built, such as a modifier's result).
func (d *Doc) offset(raw string) (int, bool) {
	if raw == "" || len(d.src) == 0 {
		return 0, false
	}
	off := int(uintptr(unsafe.Pointer(unsafe.StringData(raw))) - uintptr(unsafe.Pointer(unsafe.SliceData(d.src))))
	return off, off >= 0 && off <= len(d.src)-len(raw)
}

// splitPath splits a dot path into its keys.
func splitPath(path string) ([]string, error) {
	keys := strings.Split(path, ".")
	if slices.Contains(keys, "") {
		return nil, fmt.Errorf("jsonedit: path %q has an empty key", path)
	}
	return keys, nil
}

// unique returns the value of the member key of obj and how often key
// appears (counting stops at 2).
func unique(obj gjson.Result, key string) (gjson.Result, int) {
	var value gjson.Result
	n := 0
	obj.ForEach(func(k, v gjson.Result) bool {
		if k.Str == key {
			if n++; n == 1 {
				value = v
			}
		}
		return n < 2
	})
	return value, n
}

func (d *Doc) set(v gjson.Result, path string, raw []byte, keep bool) {
	keys, err := splitPath(path)
	switch {
	case err != nil:
		d.fail(err)
		return
	case !gjson.ValidBytes(raw):
		d.fail(fmt.Errorf("%w: value of %s %.40q", ErrInvalid, path, raw))
		return
	}
	seq := d.next()
	obj := d.object(v, true)
	for obj != nil && len(keys) > 1 {
		child, n := unique(v, keys[0])
		switch {
		case n > 1:
			d.fail(fmt.Errorf("%w: %q on the path %s", ErrDuplicateKey, keys[0], path))
			return
		case n == 1 && !child.IsObject() && keep:
			return // Default replaces nothing
		case !child.IsObject():
			obj.put(keys, raw, keep, seq) // missing, or replaced by an object
			return
		}
		v, keys, obj = child, keys[1:], d.object(child, true)
	}
	if obj != nil {
		obj.put(keys, raw, keep, seq)
	}
}

func (d *Doc) delete(v gjson.Result, keys []string, seq int) {
	if len(keys) == 1 {
		if obj := d.object(v, false); obj != nil {
			obj.drop(func(key string, _ gjson.Result) bool { return key == keys[0] }, seq)
		}
		return
	}
	if !v.IsObject() {
		return
	}
	if obj := d.byStart[d.start(v)]; obj != nil {
		if m := obj.set(keys[0]); m != nil {
			m.deletePath(keys[1:], seq)
			if !m.keep {
				return // the document's member is replaced
			}
		}
	}
	v.ForEach(func(k, child gjson.Result) bool {
		if k.Str == keys[0] && child.IsObject() {
			d.delete(child, keys[1:], seq)
		}
		return true
	})
}

// start is the offset of v in the document, -1 for a value not read from
// it.
func (d *Doc) start(v gjson.Result) int {
	if start, ok := d.offset(v.Raw); ok {
		return start
	}
	return -1
}
