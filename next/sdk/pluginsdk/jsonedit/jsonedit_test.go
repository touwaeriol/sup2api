package jsonedit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

func raw(s string) []byte { return []byte(s) }

func keys(names ...string) func(string, gjson.Result) bool {
	return func(key string, _ gjson.Result) bool { return slices.Contains(names, key) }
}

// edit runs fn on in and fails the test on an error or invalid output.
func edit(t *testing.T, in string, fn func(*jsonedit.Doc)) string {
	t.Helper()
	out, err := jsonedit.Edit([]byte(in), fn)
	if err != nil {
		t.Fatalf("%s: %v", in, err)
	}
	if !json.Valid(out) {
		t.Fatalf("%s: invalid result %s", in, out)
	}
	return string(out)
}

// TestMembers pins the member rewrite of one object: removed members take
// the right comma, set members replace in place or are added at the end,
// and the bytes around stay as they were (from the bedrock plugin's
// jsonedit.go tests).
func TestMembers(t *testing.T) {
	type set struct{ key, raw string }
	for _, tc := range []struct {
		name, in string
		drop     func(string, gjson.Result) bool
		set      []set
		want     string
	}{
		{"nothing", `{"a":1}`, nil, nil, `{"a":1}`},
		{"first", `{"a":1,"b":2,"c":3}`, keys("a"), nil, `{"b":2,"c":3}`},
		{"middle", `{"a":1,"b":2,"c":3}`, keys("b"), nil, `{"a":1,"c":3}`},
		{"last", `{"a":1,"b":2,"c":3}`, keys("c"), nil, `{"a":1,"b":2}`},
		{"leading run", `{"a":1,"b":2,"c":3}`, keys("a", "b"), nil, `{"c":3}`},
		{"trailing run", `{"a":1,"b":2,"c":3}`, keys("b", "c"), nil, `{"a":1}`},
		{"around a kept one", `{"a":1,"b":2,"c":3}`, keys("a", "c"), nil, `{"b":2}`},
		{"all", `{"a":1,"b":2}`, keys("a", "b"), nil, `{}`},
		{"empty object", `{}`, keys("a"), nil, `{}`},
		{"set in place", `{"a":1,"b":2}`, nil, []set{{"a", `"x"`}}, `{"a":"x","b":2}`},
		{"set appends", `{"a":1}`, nil, []set{{"z", `true`}, {"y", `[]`}}, `{"a":1,"z":true,"y":[]}`},
		{"set into empty", `{}`, nil, []set{{"z", `1`}, {"y", `2`}}, `{"z":1,"y":2}`},
		{"set after all removed", `{"a":1,"b":2}`, keys("a", "b"), []set{{"z", `1`}}, `{"z":1}`},
		{"set after the last removed", `{"a":1,"b":2}`, keys("b"), []set{{"z", `1`}}, `{"a":1,"z":1}`},
		{"set of a deleted key moves it to the end", `{"a":1,"b":2}`, keys("a"), []set{{"a", `3`}}, `{"b":2,"a":3}`},
		{"duplicates of a dropped key go", `{"a":1,"b":2,"a":3}`, keys("a"), nil, `{"b":2}`},
		{"escaped key", `{"model":"x","b":2}`, keys("model"), nil, `{"b":2}`},
		{"whitespace kept", "{\n  \"a\": 1,\n  \"b\" : [ 1 ],\n  \"c\": 3\n}", keys("b"), nil, "{\n  \"a\": 1,\n  \"c\": 3\n}"},
		{"whitespace, leading", "{ \"a\": 1 ,\n \"b\": 2 }", keys("a"), []set{{"z", `0`}}, "{ \"b\": 2 ,\"z\":0}"},
		{"whitespace, trailing run removed", "{ \"a\": 1, \"b\": 2\n}", keys("b"), []set{{"z", `0`}}, "{ \"a\": 1,\"z\":0\n}"},
		{"whitespace, all removed", `{ "user":"u" , "top_p":1 }`, keys("user", "top_p"), []set{{"s", `false`}}, `{ "s":false }`},
		{"leading space before the document", " \n{ }", nil, []set{{"s", `false`}}, " \n{\"s\":false }"},
		{"key needing escapes", `{}`, nil, []set{{"a\"b\n", `1`}}, `{"a\"b\n":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := edit(t, tc.in, func(d *jsonedit.Doc) {
				o := d.Object(d.Root())
				if tc.drop != nil {
					o.DeleteFunc(tc.drop)
				}
				for _, s := range tc.set {
					o.Set(s.key, raw(s.raw))
				}
			})
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestPaths: Set, Default and Delete by path, as sjson does them but in
// one pass.
func TestPaths(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		fn       func(*jsonedit.Doc)
		want     string
	}{
		{"set adds the missing objects", `{"model":"m"}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) },
			`{"model":"m","stream_options":{"include_usage":true}}`},
		{"set inside an existing object, in place", `{"model":"m","stream_options":{"include_usage":false,"z":1},"stream":true}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) },
			`{"model":"m","stream_options":{"include_usage":true,"z":1},"stream":true}`},
		{"set appends inside an existing object", `{"model":"m","stream_options":{"z":1},"stream":true}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) },
			`{"model":"m","stream_options":{"z":1,"include_usage":true},"stream":true}`},
		{"two paths share a new object", `{}`,
			func(d *jsonedit.Doc) { d.Set("a.b", raw(`1`)); d.Set("a.c.d", raw(`2`)); d.Set("e", raw(`3`)) },
			`{"a":{"b":1,"c":{"d":2}},"e":3}`},
		{"the last set wins", `{"a":1}`,
			func(d *jsonedit.Doc) {
				d.Set("a", raw(`2`))
				d.Set("a", raw(`3`))
				d.Set("b.c", raw(`1`))
				d.Set("b", raw(`4`))
			},
			`{"a":3,"b":4}`},
		{"a value on the way is replaced by an object", `{"a":null,"b":2}`,
			func(d *jsonedit.Doc) { d.Set("a.x", raw(`1`)) },
			`{"a":{"x":1},"b":2}`},
		{"setting the same bytes is no edit", `{"a": false}`,
			func(d *jsonedit.Doc) { d.Set("a", raw(`false`)) },
			`{"a": false}`},
		{"default adds what is missing", `{"store":false}`,
			func(d *jsonedit.Doc) { d.Default("instructions", raw(`""`)); d.Default("store", raw(`true`)) },
			`{"store":false,"instructions":""}`},
		{"default keeps null, and a value on the way", `{"instructions":null,"a":1}`,
			func(d *jsonedit.Doc) { d.Default("instructions", raw(`""`)); d.Default("a.b", raw(`1`)) },
			`{"instructions":null,"a":1}`},
		{"default below a missing object", `{}`,
			func(d *jsonedit.Doc) { d.Default("a.b", raw(`1`)); d.Default("a.b", raw(`2`)) },
			`{"a":{"b":1}}`},
		{"default on a duplicated key changes nothing", `{"a":1,"a":2}`,
			func(d *jsonedit.Doc) { d.Default("a", raw(`3`)) },
			`{"a":1,"a":2}`},
		{"delete every occurrence", `{"service_tier":"a","x":1,"service_tier":"b"}`,
			func(d *jsonedit.Doc) { d.Delete("service_tier") },
			`{"x":1}`},
		{"delete nested, in every parent of that name", `{"m":{"a":1,"b":2},"x":0,"m":{"b":3}}`,
			func(d *jsonedit.Doc) { d.Delete("m.b") },
			`{"m":{"a":1},"x":0,"m":{}}`},
		{"delete a missing path", `{"a":{"c":1}}`,
			func(d *jsonedit.Doc) { d.Delete("b"); d.Delete("a.b"); d.Delete("a.c.d") },
			`{"a":{"c":1}}`},
		{"delete from a value that is not an object", `[1,{"a":1}]`,
			func(d *jsonedit.Doc) { d.Delete("a") },
			`[1,{"a":1}]`},
		{"duplicates elsewhere stay", `{"a":1,"a":2}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) },
			`{"a":1,"a":2,"stream_options":{"include_usage":true}}`},
		{"delete then set in one object", `{"a":1,"b":2,"c":3}`,
			func(d *jsonedit.Doc) { d.Delete("b"); d.Set("c", raw(`4`)); d.Set("d", raw(`5`)) },
			`{"a":1,"c":4,"d":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := edit(t, tc.in, tc.fn); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestCallOrder: edits of one member take effect in the order they are
// made, whichever of Set, Default, Delete and DeleteFunc they are.
func TestCallOrder(t *testing.T) {
	isTrue := func(_ string, v gjson.Result) bool { return v.Type == gjson.True }
	for _, tc := range []struct {
		name, in string
		fn       func(*jsonedit.Doc)
		want     string
	}{
		{"set then delete", `{"a":1,"stream":false}`,
			func(d *jsonedit.Doc) { d.Set("stream", raw(`true`)); d.Delete("stream") },
			`{"a":1}`},
		{"set of a missing key then delete", `{"a":1}`,
			func(d *jsonedit.Doc) { d.Set("stream", raw(`true`)); d.Delete("stream") },
			`{"a":1}`},
		{"delete then set", `{"a":1,"stream":false}`,
			func(d *jsonedit.Doc) { d.Delete("stream"); d.Set("stream", raw(`true`)) },
			`{"a":1,"stream":true}`},
		{"set, delete, set", `{"stream":false,"a":1}`,
			func(d *jsonedit.Doc) { d.Set("stream", raw(`1`)); d.Delete("stream"); d.Set("stream", raw(`2`)) },
			`{"a":1,"stream":2}`},
		{"nested set then delete", `{"a":{"x":1}}`,
			func(d *jsonedit.Doc) { d.Set("a.y", raw(`2`)); d.Delete("a.y") },
			`{"a":{"x":1}}`},
		{"nested delete then set", `{"a":{"x":1,"y":0}}`,
			func(d *jsonedit.Doc) { d.Delete("a.y"); d.Set("a.y", raw(`2`)) },
			`{"a":{"x":1,"y":2}}`},
		{"set below a missing object then delete", `{"b":1}`,
			func(d *jsonedit.Doc) { d.Set("a.y", raw(`2`)); d.Set("a.z", raw(`3`)); d.Delete("a.y") },
			`{"b":1,"a":{"z":3}}`},
		{"set of an object value then delete inside it", `{"b":1}`,
			func(d *jsonedit.Doc) { d.Set("a", raw(`{"y":2,"z":3}`)); d.Delete("a.y") },
			`{"b":1,"a":{"z":3}}`},
		{"default then delete", `{"a":1}`,
			func(d *jsonedit.Doc) { d.Default("b", raw(`2`)); d.Delete("b") },
			`{"a":1}`},
		{"default of a present key then delete", `{"a":1,"b":0}`,
			func(d *jsonedit.Doc) { d.Default("b", raw(`2`)); d.Delete("b") },
			`{"a":1}`},
		{"delete then default", `{"a":1,"b":0}`,
			func(d *jsonedit.Doc) { d.Delete("b"); d.Default("b", raw(`2`)) },
			`{"a":1,"b":2}`},
		{"set then DeleteFunc judging the value set", `{"a":1,"stream":false}`,
			func(d *jsonedit.Doc) { d.Set("stream", raw(`true`)); d.Object(d.Root()).DeleteFunc(isTrue) },
			`{"a":1}`},
		{"set then DeleteFunc that keeps the value set", `{"stream":true,"a":1}`,
			func(d *jsonedit.Doc) { d.Set("stream", raw(`false`)); d.Object(d.Root()).DeleteFunc(isTrue) },
			`{"stream":false,"a":1}`},
		{"DeleteFunc then set", `{"stream":true,"a":1}`,
			func(d *jsonedit.Doc) { d.Object(d.Root()).DeleteFunc(isTrue); d.Set("stream", raw(`true`)) },
			`{"a":1,"stream":true}`},
		{"default then DeleteFunc", `{"a":1}`,
			func(d *jsonedit.Doc) { d.Default("b", raw(`true`)); d.Object(d.Root()).DeleteFunc(isTrue) },
			`{"a":1}`},
		{"nested set then DeleteFunc of its object", `{"o":{"x":1}}`,
			func(d *jsonedit.Doc) {
				d.Set("o.y", raw(`true`))
				d.Object(d.Root().Get("o")).DeleteFunc(isTrue)
			},
			`{"o":{"x":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := edit(t, tc.in, tc.fn); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestValues: values read from the document are edited where they are,
// inside arrays and nested objects.
func TestValues(t *testing.T) {
	in := `{"input":[{"role":"system","content":"s"},"odd",{"role" : "system"},{"role":{"nested":"system"}},{"cache_control":{"type":"e","scope":"g"}}],"n":1.50e+3}`
	want := `{"input":[{"role":"developer","content":"s"},"odd",{"role" : "developer"},{"role":{"nested":"system"}},{"cache_control":{"type":"e"}}],"n":1.50e+3}`
	got := edit(t, in, func(d *jsonedit.Doc) {
		jsonedit.EachElement(d.Root().Get("input"), func(item gjson.Result) {
			if role := item.Get("role"); role.Type == gjson.String && role.Str == "system" {
				d.Replace(role, raw(`"developer"`))
			}
			d.Object(item.Get("cache_control")).Delete("scope")
		})
		jsonedit.EachElement(d.Root().Get("n"), func(gjson.Result) { t.Error("EachElement called for a number") })
	})
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// A string becomes an array, in place.
	if got := edit(t, `{"input":"hé", "x":1}`, func(d *jsonedit.Doc) {
		d.Replace(d.Root().Get("input"), raw(`[{"text":"hé"}]`))
	}); got != `{"input":[{"text":"hé"}], "x":1}` {
		t.Fatalf("string to array: %s", got)
	}
	// 1000 edits in one array, one pass.
	many := `{"input":[` + strings.TrimSuffix(strings.Repeat(`{"role":"system"},`, 1000), ",") + `]}`
	if got := edit(t, many, func(d *jsonedit.Doc) {
		jsonedit.EachElement(d.Root().Get("input"), func(item gjson.Result) { d.Replace(item.Get("role"), raw(`"developer"`)) })
	}); got != strings.ReplaceAll(many, `"system"`, `"developer"`) {
		t.Fatalf("1000 items: %.200s", got)
	}
}

// TestErrors: what the package refuses, with the error saying which rule.
func TestErrors(t *testing.T) {
	for _, in := range []string{``, `{"a":`, `{} {}`, `{"a":0012}`, `nul`} {
		if _, err := jsonedit.Parse([]byte(in)); !errors.Is(err, jsonedit.ErrInvalid) {
			t.Errorf("Parse(%q) = %v", in, err)
		}
	}
	other, _ := jsonedit.Parse([]byte(`{"a":1}`))
	for _, tc := range []struct {
		name, in string
		fn       func(*jsonedit.Doc)
		want     error
	}{
		{"duplicate on the set path", `{"stream_options":{"include_usage":false},"stream_options":{}}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) }, jsonedit.ErrDuplicateKey},
		{"duplicate of the set key", `{"stream_options":{"include_usage":false,"include_usage":false}}`,
			func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) }, jsonedit.ErrDuplicateKey},
		{"duplicate of a set member", `{"a":1,"b":2,"a":3}`,
			func(d *jsonedit.Doc) { d.Set("a", raw(`9`)) }, jsonedit.ErrDuplicateKey},
		{"set in an array", `[]`, func(d *jsonedit.Doc) { d.Set("a", raw(`1`)) }, jsonedit.ErrNotObject},
		{"set in a missing object", `{}`, func(d *jsonedit.Doc) { d.Object(d.Root().Get("x")).Set("a", raw(`1`)) }, jsonedit.ErrNotObject},
		{"invalid value", `{}`, func(d *jsonedit.Doc) { d.Set("a", raw(`{`)) }, jsonedit.ErrInvalid},
		{"invalid replacement", `{"a":1}`, func(d *jsonedit.Doc) { d.Replace(d.Root().Get("a"), raw(``)) }, jsonedit.ErrInvalid},
		{"value of another document", `{"a":1}`, func(d *jsonedit.Doc) { d.Replace(other.Root().Get("a"), raw(`2`)) }, jsonedit.ErrForeign},
		{"value gjson built", `{"a":[1,2]}`, func(d *jsonedit.Doc) { d.Replace(d.Root().Get("a|@reverse"), raw(`2`)) }, jsonedit.ErrForeign},
		{"edit inside a deleted member", `{"a":{"b":1}}`,
			func(d *jsonedit.Doc) { d.Delete("a"); d.Replace(d.Root().Get("a.b"), raw(`2`)) }, jsonedit.ErrConflict},
		{"a value replaced twice", `{"a":1}`,
			func(d *jsonedit.Doc) { d.Replace(d.Root().Get("a"), raw(`2`)); d.Set("a", raw(`3`)) }, jsonedit.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := jsonedit.Edit([]byte(tc.in), tc.fn); !errors.Is(err, tc.want) || out != nil {
				t.Fatalf("%s, %v; want %v", out, err, tc.want)
			}
		})
	}
	if _, err := jsonedit.Edit([]byte(`{}`), func(d *jsonedit.Doc) { d.Set("a..b", raw(`1`)) }); err == nil {
		t.Error("a path with an empty key was accepted")
	}
}

// TestNoEditNoCopy: a batch that changes nothing hands the body back.
func TestNoEditNoCopy(t *testing.T) {
	in := []byte(`{"store":false,"instructions":"","a":{"b":1}}`)
	out, err := jsonedit.Edit(in, func(d *jsonedit.Doc) {
		d.Set("store", raw(`false`))
		d.Default("instructions", raw(`""`))
		d.Delete("missing")
		d.Delete("a.c")
		d.Replace(d.Root().Get("a.b"), raw(`1`))
	})
	if err != nil || &out[0] != &in[0] {
		t.Fatalf("%s, %v: copied", out, err)
	}
}

// realistic is a body the library has no reason to touch outside its
// edits: unusual key order, white space, fields nobody knows, \u escapes
// next to raw UTF-8, HTML characters, numbers beyond float64 and int64, and
// a few MiB of history (from the standard package's body tests).
func realistic(tail string) []byte {
	var b strings.Builder
	b.WriteString("{\n  \"zeta_unknown\": {\"nested\": [1, 2.50, -0.0, 1e400]},\n")
	b.WriteString("  \"model\" :\t\"gpt-4o-mini\",\n")
	b.WriteString("  \"big\": 123456789012345678901234567890,\n")
	b.WriteString("  \"text\": \"caf\\u00e9 café \\ud83d\\ude00 <b>&amp;</b> \\\"q\\\" \\/\",\n")
	b.WriteString("  \"service_tier\" : \"priority\" ,\n")
	b.WriteString("  \"messages\": [")
	for i := range 20000 {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`{"role":"user","content":[{"type":"text","text":"turn ` + strings.Repeat("x", 100) + `"}],"cache_control":{"type":"ephemeral"}}`)
	}
	b.WriteString("],\n  \"safety_identifier\": \"u-1\",\n  \"user\": \"u1\"" + tail + "\n}")
	return []byte(b.String())
}

// TestBytesIdentity: on a large realistic body every byte no edit names is
// returned as it came.
func TestBytesIdentity(t *testing.T) {
	in := realistic(",\n  \"stream_options\": {\"x\": 1, \"include_usage\": false}")
	out, err := jsonedit.Edit(in, func(d *jsonedit.Doc) {
		d.Delete("service_tier")
		d.Delete("safety_identifier")
		d.Set("stream_options.include_usage", raw(`true`))
	})
	want := strings.Replace(string(in), ",\n  \"service_tier\" : \"priority\"", "", 1)
	want = strings.Replace(want, ",\n  \"safety_identifier\": \"u-1\"", "", 1)
	want = strings.Replace(want, `"include_usage": false`, `"include_usage": true`, 1)
	if err != nil || string(out) != want {
		t.Fatalf("%v: %d bytes, want %d", err, len(out), len(want))
	}
	in = realistic("")
	out, err = jsonedit.Edit(in, func(d *jsonedit.Doc) { d.Set("stream_options.include_usage", raw(`true`)) })
	end := bytes.LastIndexByte(in, '}')
	if want := string(in[:end]) + `,"stream_options":{"include_usage":true}` + string(in[end:]); err != nil || string(out) != want {
		t.Fatalf("%v: tail %q", err, out[max(0, len(out)-80):])
	}
}

// TestAllocations: a batch of edits all over an 8 MiB body costs one
// output buffer and little else, at most twice the body; a chain of sjson
// calls costs one copy per edit. Measured without the race detector, which
// changes what is allocated.
func TestAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes what is allocated")
	}
	var b strings.Builder
	b.WriteString(`{"model":"m","stream":true,"metadata":{"user_id":"u"},"input":[`)
	for b.Len() < 8<<20 {
		b.WriteString(`{"role":"system","content":"` + strings.Repeat("lorem \\\"ipsum\\\" ", 200) + `","cache_control":{"type":"ephemeral","scope":"global"}},`)
	}
	b.WriteString(`{"role":"user","content":"end"}]}`)
	body := []byte(b.String())
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := jsonedit.Edit(body, func(d *jsonedit.Doc) {
		d.Delete("metadata")
		d.Delete("stream")
		d.Set("store", raw(`false`))
		jsonedit.EachElement(d.Root().Get("input"), func(item gjson.Result) {
			if role := item.Get("role"); role.Str == "system" {
				d.Replace(role, raw(`"developer"`))
			}
			d.Object(item.Get("cache_control")).Delete("scope")
		})
	})
	runtime.ReadMemStats(&after)
	if err != nil || !json.Valid(out) {
		t.Fatalf("%v", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("%d bytes allocated for a body of %d (%.2fx)", allocated, len(body), float64(allocated)/float64(len(body)))
	if allocated > uint64(2*len(body)) {
		t.Fatalf("allocated %d bytes for a body of %d", allocated, len(body))
	}
}
