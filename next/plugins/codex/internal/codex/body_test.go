package codex

import (
	"runtime"
	"strings"
	"testing"
)

// codexBody edits the received bytes: what it does not change keeps its
// bytes, order and spacing, whichever members go.
func TestCodexBodyBytes(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		ws             bool
	}{
		{"nothing to do", `{"store":false, "instructions":"x","input":[1, "a"],"model":"m"}`, `{"store":false, "instructions":"x","input":[1, "a"],"model":"m"}`, false},
		{"empty object", `{}`, `{"store":false,"instructions":""}`, false},
		{"leading space", " \n{ }", " \n{\"store\":false,\"instructions\":\"\" }", false},
		{"first members go", `{"user":"u", "metadata":{},"model":"m","input":[]}`, `{"model":"m","input":[],"store":false,"instructions":""}`, false},
		{"middle and last go", `{"model":"m",  "top_p":1,"input":[] ,"truncation":"auto"}`, `{"model":"m","input":[],"store":false,"instructions":""}`, false},
		{"all go", `{ "user":"u" , "top_p":1 }`, `{ "store":false,"instructions":"" }`, false},
		{"store replaced in place", "{\"store\": true,\n\"instructions\":null}", "{\"store\": false,\n\"instructions\":null}", false},
		{"nested names stay", `{"input":[{"role":"user","content":"hi","metadata":{"user":1}}],"instructions":"","store":false}`, `{"input":[{"role":"user","content":"hi","metadata":{"user":1}}],"instructions":"","store":false}`, false},
		{"unicode and escapes stay", `{"input":"h\u00e9 \"q\"","instructions":"é","store":false}`, `{"input":[{"content":[{"text":"hé \"q\"","type":"input_text"}],"role":"user","type":"message"}],"instructions":"é","store":false}`, false},
		{"http drops stream_options", `{"stream":true,"stream_options":{"include_usage":true},"store":false,"instructions":""}`, `{"stream":true,"store":false,"instructions":""}`, false},
		{"ws keeps stream_options", `{"type":"response.create","stream_options":{"include_usage":true},"max_output_tokens":5}`, `{"type":"response.create","stream_options":{"include_usage":true},"store":false,"instructions":""}`, true},
	} {
		out, err := codexBody([]byte(tc.in), tc.ws)
		if err != nil || string(out) != tc.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", tc.name, out, err, tc.want)
		}
	}
	// Without an edit the body is handed back as it is, not copied.
	in := []byte(`{"store":false,"instructions":"","input":[]}`)
	if out, _ := codexBody(in, false); &out[0] != &in[0] {
		t.Error("an unchanged body was copied")
	}
	// Every unsupported field goes over HTTP; a WebSocket message keeps
	// stream_options only.
	for _, ws := range []bool{false, true} {
		for _, f := range unsupportedFields {
			out, _ := codexBody([]byte(`{"`+f+`":1,"store":false,"instructions":""}`), ws)
			kept := ws && f == wsKept
			if (string(out) == `{"store":false,"instructions":""}`) == kept {
				t.Errorf("ws=%v %s: %s", ws, f, out)
			}
		}
	}
}

// System input items become developer items, every one of them; nothing
// else in the input changes.
func TestSystemItemsBecomeDeveloper(t *testing.T) {
	in := `{"instructions":"","store":false,"input":[{"role":"system","content":"be brief"},{"type":"function_call","call_id":"c","name":"f","arguments":"{\"role\":\"system\"}"},` +
		`{"role":"user","content":"system"},{"type":"message","role" : "system","content":[{"type":"input_text","text":"x"}]},{"role":"developer","content":"d"},"odd",{"role":{"nested":"system"}},{"role":["system"]}]}`
	want := `{"instructions":"","store":false,"input":[{"role":"developer","content":"be brief"},{"type":"function_call","call_id":"c","name":"f","arguments":"{\"role\":\"system\"}"},` +
		`{"role":"user","content":"system"},{"type":"message","role" : "developer","content":[{"type":"input_text","text":"x"}]},{"role":"developer","content":"d"},"odd",{"role":{"nested":"system"}},{"role":["system"]}]}`
	for _, ws := range []bool{false, true} {
		if out, err := codexBody([]byte(in), ws); err != nil || string(out) != want {
			t.Fatalf("ws=%v:\n got %s (%v)\nwant %s", ws, out, err, want)
		}
	}
	// No cap: the edits cost no copy each.
	many := `{"instructions":"","store":false,"input":[` + strings.TrimSuffix(strings.Repeat(`{"role":"system"},`, 1000), ",") + `]}`
	if out, _ := codexBody([]byte(many), false); string(out) != strings.ReplaceAll(many, `"system"`, `"developer"`) {
		t.Fatalf("1000 system items: %.200s", out)
	}
}

// A large body costs one parse copy and one output buffer, well inside the
// conformance bound of four times the body.
func TestCodexBodyAllocations(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"system","content":"s"},{"role":"user","content":"` + strings.Repeat("x", 16<<20) + `"}],"temperature":1}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := codexBody(body, false)
	runtime.ReadMemStats(&after)
	if err != nil || len(out) < len(body) {
		t.Fatalf("out %d bytes: %v", len(out), err)
	}
	if extra := after.TotalAlloc - before.TotalAlloc; extra > 4*uint64(len(body)) {
		t.Fatalf("allocated %d bytes for a %d byte body", extra, len(body))
	}
}
