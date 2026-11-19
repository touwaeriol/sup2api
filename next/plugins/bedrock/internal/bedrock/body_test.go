package bedrock

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestInvokeBodyTopLevel: the path carries model and stream, so both leave
// the body; anthropic_version is always set, the top-level fields InvokeModel
// refuses leave, output_config stays, and the fallbacks the core guards are
// never touched.
func TestInvokeBodyTopLevel(t *testing.T) {
	for _, tc := range []struct {
		name, header, body, want string
	}{
		{"a plain request", "",
			`{"model":"claude-haiku-4-5","max_tokens":8,"messages":[]}`,
			`{"max_tokens":8,"messages":[],"anthropic_version":"bedrock-2023-05-31"}`},
		{"what Claude Code sends Anthropic", "claude-code-20250219,context-1m-2025-08-07",
			`{"model":"claude-haiku-4-5","stream":true,"max_tokens":8,"messages":[],"metadata":{"user_id":"u"},"provider":{"only":["x"]},"service_tier":"auto","inference_geo":"us","speed":"fast",` +
				`"context_management":{"edits":[]},"output_config":{"effort":"high"},"output_format":{"type":"json_schema"},"fallbacks":[{"model":"claude-opus-4-8"}],"anthropic_beta":["oauth-2025-04-20"]}`,
			`{"max_tokens":8,"messages":[],"output_config":{"effort":"high"},"output_format":{"type":"json_schema"},"fallbacks":[{"model":"claude-opus-4-8"}],"anthropic_beta":["context-1m-2025-08-07"],"anthropic_version":"bedrock-2023-05-31"}`},
		{"Anthropic's service choices", "",
			`{"model":"claude-opus-5-5","inference_geo":"us","max_tokens":8,"speed":"fast","messages":[],"service_tier":"standard_only"}`,
			`{"max_tokens":8,"messages":[],"anthropic_version":"bedrock-2023-05-31"}`},
		{"context management with its feature", "context-management-2025-06-27",
			`{"model":"claude-haiku-4-5","max_tokens":8,"messages":[],"context_management":{"edits":[]}}`,
			`{"max_tokens":8,"messages":[],"context_management":{"edits":[]},"anthropic_version":"bedrock-2023-05-31","anthropic_beta":["context-management-2025-06-27"]}`},
		{"a body feature nobody supports", "",
			`{"model":"claude-haiku-4-5","max_tokens":8,"anthropic_beta":["claude-code-20250219"],"messages":[]}`,
			`{"max_tokens":8,"messages":[],"anthropic_version":"bedrock-2023-05-31"}`},
		{"the client's own anthropic_version is replaced", "",
			`{"anthropic_version":"2023-06-01","model":"m","max_tokens":8,"messages":[]}`,
			`{"anthropic_version":"bedrock-2023-05-31","max_tokens":8,"messages":[]}`},
		{"duplicate keys leave with their first", "",
			`{"model":"a","max_tokens":8,"model":"b","messages":[],"stream":false,"stream":true}`,
			`{"max_tokens":8,"messages":[],"anthropic_version":"bedrock-2023-05-31"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := invokeBody([]byte(tc.body), tc.header, testHaiku)
			if err != nil || string(got) != tc.want {
				t.Fatalf("body = %s (%v)\nwant   %s", got, err, tc.want)
			}
		})
	}
	for _, body := range []string{``, `[]`, `{"model":`, `null`} {
		if _, err := invokeBody([]byte(body), "", testHaiku); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%q: %v", body, err)
		}
	}
}

// TestInvokeBodyBetas: the beta features of the header and of the body's
// own anthropic_beta are settled together; the field leaves when none is
// left, and context_management leaves without its feature.
func TestInvokeBodyBetas(t *testing.T) {
	for _, tc := range []struct {
		name string
		// header is the anthropic-beta header, body the body's
		// anthropic_beta as raw JSON ("" none), want the resulting one
		// ("" none).
		header, body, want string
		// keepsContext: context_management stays.
		keepsContext bool
	}{
		{name: "none"},
		{name: "empty header", header: "  "},
		{name: "supported feature", header: "context-1m-2025-08-07", want: `["context-1m-2025-08-07"]`},
		{name: "several, spaced", header: " context-1m-2025-08-07 ,computer-use-2025-01-24,  compact-2026-01-12",
			want: `["context-1m-2025-08-07","computer-use-2025-01-24","compact-2026-01-12"]`},
		{name: "unsupported ones dropped; context management keeps its field", header: "claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,oauth-2025-04-20",
			want: `["context-management-2025-06-27"]`, keepsContext: true},
		{name: "only unsupported ones", header: "claude-code-20250219,oauth-2025-04-20,prompt-caching-2024-07-31"},
		{name: "duplicates once", header: "context-1m-2025-08-07,context-1m-2025-08-07,fine-grained-tool-streaming-2025-05-14",
			want: `["context-1m-2025-08-07","fine-grained-tool-streaming-2025-05-14"]`},
		{name: "advanced tool use is tool search on Bedrock, with tool examples", header: "advanced-tool-use-2025-11-20",
			want: `["tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{name: "tool examples not repeated", header: "tool-examples-2025-10-29,advanced-tool-use-2025-11-20,tool-search-tool-2025-10-19",
			want: `["tool-examples-2025-10-29","tool-search-tool-2025-10-19"]`},
		{name: "features of the AWS model cards", header: "compact-2026-09-04,fallback-credit-2026-07-01,thinking-display-updates-2026-08-18,per-turn-control-2026-07-01",
			want: `["compact-2026-09-04","fallback-credit-2026-07-01","thinking-display-updates-2026-08-18","per-turn-control-2026-07-01"]`},
		{name: "body, supported", body: `["context-1m-2025-08-07"]`, want: `["context-1m-2025-08-07"]`},
		{name: "body, unsupported ones dropped", body: `["claude-code-20250219","compact-2026-01-12","advanced-tool-use-2025-11-20"]`,
			want: `["compact-2026-01-12","tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{name: "body, only unsupported ones: the field leaves", body: `["claude-code-20250219"]`},
		{name: "body, empty array: the field leaves", body: `[]`},
		{name: "body, one string in the header's format", body: `"context-1m-2025-08-07, compact-2026-01-12"`,
			want: `["context-1m-2025-08-07","compact-2026-01-12"]`},
		{name: "body, not a list of names: the field leaves", body: `{"context-1m-2025-08-07":true}`},
		{name: "body, names among other values", body: `[1,"context-management-2025-06-27",null]`, want: `["context-management-2025-06-27"]`, keepsContext: true},
		{name: "header and body merged", header: "context-1m-2025-08-07,claude-code-20250219", body: `["compact-2026-01-12","context-1m-2025-08-07"]`,
			want: `["context-1m-2025-08-07","compact-2026-01-12"]`},
		{name: "nothing usable in either", header: "oauth-2025-04-20", body: `["claude-code-20250219"]`},
		{name: "header unusable, body usable", header: "oauth-2025-04-20", body: `["context-management-2025-06-27"]`, want: `["context-management-2025-06-27"]`, keepsContext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"m","max_tokens":8,"context_management":{"edits":[]},"messages":[]}`
			if tc.body != "" {
				body = strings.Replace(body, `"messages"`, `"anthropic_beta":`+tc.body+`,"messages"`, 1)
			}
			got, err := invokeBody([]byte(body), tc.header, testHaiku)
			if err != nil {
				t.Fatal(err)
			}
			if betas := gjson.GetBytes(got, fieldBetas); betas.Raw != tc.want {
				t.Fatalf("anthropic_beta = %s, want %s (body %s)", betas.Raw, tc.want, got)
			}
			if gjson.GetBytes(got, fieldContextManagement).Exists() != tc.keepsContext {
				t.Fatalf("context_management kept = %v (body %s)", !tc.keepsContext, got)
			}
		})
	}
}

// TestToolBetas: the features the tools need are added whether or not the
// client named them.
func TestToolBetas(t *testing.T) {
	const sonnet, haiku = "global.anthropic.claude-sonnet-4-5-20250929-v1:0", testHaiku
	for _, tc := range []struct {
		name, model, tools, want string
	}{
		{"no tools", sonnet, ``, ``},
		{"plain tools", sonnet, `[{"name":"a","input_schema":{}}]`, ``},
		{"computer use", sonnet, `[{"type":"computer_20251124","name":"computer"}]`, `["computer-use-2025-11-24"]`},
		{"computer use of January", sonnet, `[{"type":"computer_20250124","name":"computer"}]`, `["computer-use-2025-01-24"]`},
		{"tool search", sonnet, `[{"type":"tool_search_tool_regex_20251119","name":"s"}]`, `["tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{"tool search, bm25, Fable", "anthropic.claude-fable-5-2", `[{"type":"tool_search_tool_bm25_20251119","name":"s"}]`, `["tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{"tool search on Haiku has no feature", haiku, `[{"type":"tool_search_tool_regex_20251119","name":"s"}]`, ``},
		{"tool search before 4.5 has no feature", "us.anthropic.claude-opus-4-1-20250805-v1:0", `[{"type":"tool_search_tool_regex_20251119","name":"s"}]`, ``},
		{"tool search, model unknown (ARN)", testARN, `[{"type":"tool_search_tool_regex_20251119","name":"s"}]`, ``},
		{"programmatic tool calling, any model", haiku, `[{"name":"a","allowed_callers":["code_execution_20250825"]}]`, `["tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{"input examples, any model", testARN, `[{"name":"a","input_examples":[{"x":1}]}]`, `["tool-search-tool-2025-10-19","tool-examples-2025-10-29"]`},
		{"empty input examples", sonnet, `[{"name":"a","input_examples":[]}]`, ``},
		{"input examples not a list", sonnet, `[{"name":"a","input_examples":"x"}]`, ``},
		{"tools not a list", sonnet, `{"type":"computer_20251124"}`, ``},
		{"with the header's, each once", sonnet, `[{"type":"computer_20251124"},{"type":"computer_20251124"}]`, `["context-1m-2025-08-07","computer-use-2025-11-24"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"max_tokens":8,"messages":[]}`
			if tc.tools != "" {
				body = `{"max_tokens":8,"messages":[],"tools":` + tc.tools + `}`
			}
			header := ""
			if strings.HasPrefix(tc.name, "with the header") {
				header = "context-1m-2025-08-07,computer-use-2025-11-24"
			}
			got, err := invokeBody([]byte(body), header, tc.model)
			if err != nil {
				t.Fatal(err)
			}
			if betas := gjson.GetBytes(got, fieldBetas).Raw; betas != tc.want {
				t.Fatalf("anthropic_beta = %s, want %s", betas, tc.want)
			}
		})
	}
}

// TestSanitize: what Claude Code adds deep inside a body and InvokeModel
// refuses is cleaned, the rest of each object stays as it was.
func TestSanitize(t *testing.T) {
	const opus45, opus47, fable, sonnet4 = "global.anthropic.claude-opus-4-5-20251101-v1:0", "global.anthropic.claude-opus-4-7", "global.anthropic.claude-fable-5-2", "us.anthropic.claude-opus-4-1-20250805-v1:0"
	for _, tc := range []struct {
		name, model, in, want string
	}{
		{"tool custom", opus45,
			`{"tools":[{"name":"a","custom":{"defer_loading":true},"input_schema":{}},{"name":"b","input_schema":{}},{"custom":1}]}`,
			`{"tools":[{"name":"a","input_schema":{}},{"name":"b","input_schema":{}},{}]}`},
		{"cache_control scope, ttl kept on 4.5", opus45,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","scope":"global","ttl":"1h"}}],` +
				`"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"scope":"global","type":"ephemeral","ttl":"5m"}}]}],` +
				`"tools":[{"name":"a","custom":{},"cache_control":{"type":"ephemeral","scope":"global"}}]}`,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}],` +
				`"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"5m"}}]}],` +
				`"tools":[{"name":"a","cache_control":{"type":"ephemeral"}}]}`},
		{"ttl leaves before 4.5", sonnet4,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}]}`},
		{"ttl stays when the model is unknown, scope still leaves", testARN,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","scope":"global","ttl":"1h"}}]}`,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`},
		{"ttl stays on a mapped name", "my-claude",
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`},
		{"a ttl Bedrock does not know leaves", opus45,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"24h"}},{"type":"text","text":"t","cache_control":{"ttl":60}}]}`,
			`{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}},{"type":"text","text":"t","cache_control":{}}]}`},
		{"system as a string, content as a string", opus45,
			`{"system":"s","messages":[{"role":"user","content":"x"}]}`,
			`{"system":"s","messages":[{"role":"user","content":"x"}]}`},
		{"tool use ids", opus45,
			`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu.01:a/b","name":"a","input":{"id":"x.y"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu.01:a/b","content":"ok"},{"type":"text","text":"id.x"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_02-ok","name":"a","input":{}}]}]}`,
			`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01_a_b","name":"a","input":{"id":"x.y"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01_a_b","content":"ok"},{"type":"text","text":"id.x"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_02-ok","name":"a","input":{}}]}]}`},
		{"thinking enabled on Opus 4.7: adaptive", opus47,
			`{"thinking":{"type":"enabled","budget_tokens":2048,"display":"summarized"}}`,
			`{"thinking":{"type":"adaptive","display":"summarized"}}`},
		{"thinking adaptive with a budget on Fable", fable,
			`{"thinking":{"budget_tokens":2048,"type":"adaptive"}}`,
			`{"thinking":{"type":"adaptive"}}`},
		{"thinking enabled on Fable", fable,
			`{"thinking":{"type":"enabled"}}`,
			`{"thinking":{"type":"adaptive"}}`},
		{"thinking enabled without budget elsewhere", opus45,
			`{"thinking":{"type":"enabled"}}`,
			`{"thinking":{"type":"enabled","budget_tokens":10000}}`},
		{"thinking enabled with budget elsewhere", opus45,
			`{"thinking":{"type":"enabled","budget_tokens":2048}}`,
			`{"thinking":{"type":"enabled","budget_tokens":2048}}`},
		{"thinking disabled", opus47,
			`{"thinking":{"type":"disabled"}}`,
			`{"thinking":{"type":"disabled"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := strings.TrimSuffix(tc.want, "}") + `,"anthropic_version":"bedrock-2023-05-31"}`
			got, err := invokeBody([]byte(tc.in), "", tc.model)
			if err != nil || string(got) != want {
				t.Fatalf("body = %s (%v)\nwant   %s", got, err, want)
			}
		})
	}
}

// TestParseClaude reads the family and version of the model ids Bedrock
// uses.
func TestParseClaude(t *testing.T) {
	for id, want := range map[string]claudeModel{
		"anthropic.claude-opus-4-5-20251101-v1:0":    {"opus", 4, 5},
		"us.anthropic.claude-opus-4-1-20250805-v1:0": {"opus", 4, 1},
		"anthropic.claude-sonnet-4-20250514-v1:0":    {"sonnet", 4, 0},
		"global.anthropic.claude-sonnet-5":           {"sonnet", 5, 0},
		"anthropic.claude-haiku-5-5":                 {"haiku", 5, 5},
		"anthropic.claude-fable-5-2":                 {"fable", 5, 2},
		"anthropic.claude-opus-4-6-v1":               {"opus", 4, 6},
		"Anthropic.Claude-Opus-4.7":                  {"opus", 4, 7},
		testARN:                                      {},
		"amazon.nova-pro-v1:0":                       {},
	} {
		if got := parseClaude(id); got != want {
			t.Errorf("%s = %+v, want %+v", id, got, want)
		}
	}
}

// TestInvokeBodyPreservesBytes: what the plugin has no reason to touch
// leaves exactly as it came: unknown fields, key order, number formats,
// escapes and whitespace.
func TestInvokeBodyPreservesBytes(t *testing.T) {
	in := "{\n  \"model\": \"claude-x\",\n  \"zeta\": 1.50e+3,\n  \"messages\": [ {\"role\":\"user\",\"content\":\"caf\\u00e9 \\ud83d\\ude00\"} ],\n" +
		"  \"max_tokens\": 0012,\n  \"vendor_extension\": {\"b\":2,\"a\":1},\n  \"stream\": true\n}"
	want := "{\n  \"zeta\": 1.50e+3,\n  \"messages\": [ {\"role\":\"user\",\"content\":\"caf\\u00e9 \\ud83d\\ude00\"} ],\n" +
		"  \"max_tokens\": 0012,\n  \"vendor_extension\": {\"b\":2,\"a\":1},\"anthropic_version\":\"bedrock-2023-05-31\"\n}"
	// 0012 is not valid JSON: the plugin refuses what it cannot read.
	if _, err := invokeBody([]byte(in), "", testHaiku); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid number: %v", err)
	}
	in, want = strings.Replace(in, "0012", "12", 1), strings.Replace(want, "0012", "12", 1)
	got, err := invokeBody([]byte(in), "", testHaiku)
	if err != nil || string(got) != want {
		t.Fatalf("body = %q (%v)\nwant   %q", got, err, want)
	}
}

// TestInvokeBodyAllocates: building the body of a large request allocates
// about one copy of it, whatever the number of edits.
func TestInvokeBodyAllocates(t *testing.T) {
	body := largeBody(8 << 20)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := invokeBody(body, "context-1m-2025-08-07", "global.anthropic.claude-opus-4-7")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(2*len(body)) {
		t.Fatalf("allocated %d bytes for a body of %d", allocated, len(body))
	}
	if len(out) > len(body) || !json.Valid(out) || !strings.HasPrefix(string(out), `{"max_tokens":64,"thinking":{"type":"adaptive"},"tools":[{"name":"a","input_schema":{}}],"messages":[`) {
		t.Fatalf("body of %d bytes became %d", len(body), len(out))
	}
}

// largeBody is a Claude Code request of at least size bytes: a long
// conversation, with what InvokeModel refuses at the top and deep inside.
func largeBody(size int) []byte {
	turn := `{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("lorem ipsum ", 1000) + `","cache_control":{"type":"ephemeral","scope":"global"}}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu.x","name":"a","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu.x","content":"ok"}]}`
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-4-7","stream":true,"max_tokens":64,"metadata":{"user_id":"u"},"thinking":{"type":"enabled","budget_tokens":1024},` +
		`"tools":[{"name":"a","input_schema":{},"custom":{"defer_loading":true}}],"messages":[`)
	for b.Len() < size {
		b.WriteString(turn)
		b.WriteByte(',')
	}
	b.WriteString(`{"role":"user","content":"end"}]}`)
	return []byte(b.String())
}
