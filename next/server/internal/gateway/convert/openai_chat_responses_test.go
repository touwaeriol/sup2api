package convert

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func chatResponsesPrepared(t *testing.T, body string) (*chatResponses, string) {
	t.Helper()
	conv, out, err := (&chatResponses{}).Prepare([]byte(body), Options{})
	if err != nil {
		t.Fatalf("prepare %s: %v", body, err)
	}
	return conv.(*chatResponses), string(out)
}

func TestChatResponsesRegistered(t *testing.T) {
	c, ok := Default().Resolve("openai.chat", "openai.responses")
	if !ok || c.From() != "openai.chat" || c.To() != "openai.responses" {
		t.Fatal("openai.chat -> openai.responses is not a built-in pair")
	}
}

func TestChatResponsesRequestMapping(t *testing.T) {
	_, out := chatResponsesPrepared(t, `{
		"model": "gpt-5", "stream": true, "stream_options": {"include_usage": true},
		"temperature": 0.5, "top_p": 0.9, "max_completion_tokens": 300, "max_tokens": 300,
		"n": 1, "logprobs": false, "presence_penalty": 0, "frequency_penalty": 0, "logit_bias": {}, "stop": null,
		"parallel_tool_calls": false, "service_tier": "priority", "user": "u1", "safety_identifier": "s1",
		"prompt_cache_key": "k1", "metadata": {"a": "b"}, "reasoning_effort": "high", "verbosity": "low",
		"response_format": {"type": "json_schema", "json_schema": {"name": "out", "description": "d", "schema": {"type": "object"}, "strict": true}},
		"tools": [
			{"type": "function", "function": {"name": "get", "description": "fetch", "parameters": {"type": "object", "properties": {"q": {"type": "string"}}}}},
			{"type": "function", "function": {"name": "now", "strict": true}}
		],
		"tool_choice": {"type": "function", "function": {"name": "get"}},
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "developer", "content": [{"type": "text", "text": "use tools"}]},
			{"role": "user", "content": [{"type": "text", "text": "look"}, {"type": "image_url", "image_url": {"url": "https://x/i.png"}},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,AA", "detail": "low"}},
				{"type": "file", "file": {"file_id": "file_1"}}]},
			{"role": "assistant", "content": "calling", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get", "arguments": "{\"q\":\"x\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "result"},
			{"role": "tool", "tool_call_id": "call_2", "content": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}]},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_3", "type": "function", "function": {"name": "now", "arguments": "{}"}}]},
			{"role": "system", "content": "late note"},
			{"role": "user", "content": "thanks"}
		]}`)
	j := gjson.Parse(out)
	for path, want := range map[string]string{
		"model": "gpt-5", "stream": "true", "store": "false", "temperature": "0.5", "top_p": "0.9",
		"max_output_tokens": "300", "parallel_tool_calls": "false", "service_tier": "priority", "user": "u1",
		"safety_identifier": "s1", "prompt_cache_key": "k1", "metadata.a": "b", "reasoning.effort": "high",
		"text.verbosity": "low", "text.format.type": "json_schema", "text.format.name": "out", "text.format.description": "d",
		"text.format.schema.type": "object", "text.format.strict": "true",
		"tools.0.type": "function", "tools.0.name": "get", "tools.0.description": "fetch", "tools.0.strict": "false",
		"tools.0.parameters.properties.q.type": "string",
		"tools.1.name":                         "now", "tools.1.strict": "true", "tools.1.parameters.type": "object",
		"tool_choice.type": "function", "tool_choice.name": "get",
		"instructions": "be brief\n\nuse tools",
		"input.#":      "8",
		"input.0.role": "user", "input.0.content.0.type": "input_text", "input.0.content.0.text": "look",
		"input.0.content.1.type": "input_image", "input.0.content.1.image_url": "https://x/i.png", "input.0.content.1.detail": "auto",
		"input.0.content.2.detail": "low", "input.0.content.3.type": "input_file", "input.0.content.3.file_id": "file_1",
		"input.1.type": "message", "input.1.role": "assistant", "input.1.content.0.type": "output_text", "input.1.content.0.text": "calling",
		"input.2.type": "function_call", "input.2.call_id": "call_1", "input.2.name": "get", "input.2.arguments": `{"q":"x"}`,
		"input.3.type": "function_call_output", "input.3.call_id": "call_1", "input.3.output": "result",
		"input.4.output.1.type": "input_text", "input.4.output.1.text": "b",
		"input.5.type": "function_call", "input.5.call_id": "call_3",
		"input.6.role": "developer", "input.6.content.0.text": "late note",
		"input.7.role": "user", "input.7.content.0.text": "thanks",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, absent := range []string{"messages", "stream_options", "max_tokens", "max_completion_tokens", "n", "logprobs",
		"presence_penalty", "frequency_penalty", "logit_bias", "stop", "response_format", "reasoning_effort", "verbosity"} {
		if j.Get(absent).Exists() {
			t.Errorf("%s left in the Responses request", absent)
		}
	}
}

func TestChatResponsesRequestDefaults(t *testing.T) {
	_, out := chatResponsesPrepared(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],"store":true,"response_format":{"type":"json_object"},
		"tool_choice":"required","stream_options":{"include_obfuscation":false}}`)
	j := gjson.Parse(out)
	if j.Get("store").Raw != "true" || j.Get("text.format.type").String() != "json_object" || j.Get("tool_choice").String() != "required" ||
		j.Get("instructions").Exists() || j.Get("stream").Exists() || j.Get("max_output_tokens").Exists() ||
		j.Get("stream_options.include_obfuscation").Raw != "false" {
		t.Fatalf("defaults: %s", out)
	}
}

// TestChatResponsesJSONObjectSystem: with response_format json_object the
// leading system and developer messages stay developer input messages (the
// Responses check for the word "json" reads input, not instructions); plain
// and json_schema requests still turn them into instructions.
func TestChatResponsesJSONObjectSystem(t *testing.T) {
	const msgs = `"messages":[{"role":"system","content":"Reply in JSON."},{"role":"developer","content":[{"type":"text","text":"be brief"}]},{"role":"user","content":"hi"}]`
	_, out := chatResponsesPrepared(t, `{"model":"m","response_format":{"type":"json_object"},`+msgs+`}`)
	j := gjson.Parse(out)
	if j.Get("instructions").Exists() || j.Get("text.format.type").String() != "json_object" || j.Get("input.#").Int() != 3 {
		t.Fatalf("json_object: %s", out)
	}
	for i, want := range []string{"Reply in JSON.", "be brief"} {
		item := j.Get("input." + strconv.Itoa(i))
		if item.Get("type").String() != "message" || item.Get("role").String() != "developer" ||
			item.Get("content.#").Int() != 1 || item.Get("content.0.type").String() != "input_text" || item.Get("content.0.text").String() != want {
			t.Errorf("json_object input.%d = %s", i, item.Raw)
		}
	}
	if j.Get("input.2.role").String() != "user" {
		t.Errorf("json_object input.2 = %s", j.Get("input.2").Raw)
	}

	for _, rf := range []string{``, `"response_format":{"type":"text"},`,
		`"response_format":{"type":"json_schema","json_schema":{"name":"o","schema":{"type":"object"}}},`} {
		_, out := chatResponsesPrepared(t, `{"model":"m",`+rf+msgs+`}`)
		j := gjson.Parse(out)
		if j.Get("instructions").String() != "Reply in JSON.\n\nbe brief" || j.Get("input.#").Int() != 1 || j.Get("input.0.role").String() != "user" {
			t.Errorf("%s: %s", rf, out)
		}
	}
}

func TestChatResponsesRequestRejections(t *testing.T) {
	base := func(extra string) string {
		return `{"model":"m","messages":[{"role":"user","content":"hi"}]` + extra + `}`
	}
	for _, tc := range []struct{ body, path string }{
		{base(`,"n":2`), "n"},
		{base(`,"n":"1"`), "n"},
		{base(`,"audio":{"voice":"x","format":"wav"}`), "audio"},
		{base(`,"modalities":["text","audio"]`), "modalities"},
		{base(`,"max_tokens":0`), "max_tokens"},
		{base(`,"temperature":"hot"`), "temperature"},
		{base(`,"tools":[{"type":"web_search"}]`), "tools[0].type"},
		{base(`,"tools":[{"type":"custom","custom":{}}]`), "tools[0].custom.name"},
		{base(`,"tools":[{"type":"custom","custom":{"name":"c","format":{"type":"regex"}}}]`), "tools[0].custom.format.type"},
		{base(`,"tools":[{"type":"custom","custom":{"name":"c","format":{"type":"grammar","grammar":{"definition":"x"}}}}]`), "tools[0].custom.format.grammar.syntax"},
		{base(`,"tool_choice":"any"`), "tool_choice"},
		{base(`,"tool_choice":{"type":"mcp"}`), "tool_choice.type"},
		{base(`,"tool_choice":{"type":"allowed_tools","allowed_tools":{}}`), "tool_choice.allowed_tools.mode"},
		{base(`,"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"mcp"}]}}`), "tool_choice.allowed_tools.tools[0].type"},
		{base(`,"function_call":"required"`), "function_call"},
		{base(`,"web_search_options":{"user_location":{"type":"exact"}}`), "web_search_options.user_location.type"},
		{base(`,"response_format":{"type":"grammar"}`), "response_format.type"},
		{`{"model":"m","messages":[]}`, "messages"},
		{`{"model":"m"}`, "messages"},
		{`{"messages":[{"role":"user","content":"hi"}]}`, "model"},
		{`{"model":"m","messages":[{"role":"critic","content":"x"}]}`, "messages[0].role"},
		{`{"model":"m","messages":[{"role":"function","content":"x"}]}`, "messages[0].name"},
		{`{"model":"m","messages":[{"role":"function","name":"f","content":"x"}]}`, "messages[0].name"},
		{`{"model":"m","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AA","format":"wav"}}]}]}`, "messages[0].content[0].type"},
		{`{"model":"m","messages":[{"role":"user","content":[{"type":"video"}]}]}`, "messages[0].content[0].type"},
		{`{"model":"m","messages":[{"role":"user","content":[{"type":"file","file":{"filename":"a.pdf"}}]}]}`, "messages[0].content[0].file"},
		{`{"model":"m","messages":[{"role":"system","content":[{"type":"image_url","image_url":{"url":"u"}}]}]}`, "messages[0].content[0].type"},
		{`{"model":"m","messages":[{"role":"assistant","content":"x","audio":{"id":"a"}}]}`, "messages[0].audio"},
		{`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"mcp"}]}]}`, "messages[0].tool_calls[0].type"},
		{`{"model":"m","messages":[{"role":"tool","content":"x"}]}`, "messages[0].tool_call_id"},
	} {
		_, _, err := (&chatResponses{}).Prepare([]byte(tc.body), Options{})
		var re *RequestError
		var ce *conversionError
		if !errors.As(err, &re) || !errors.As(err, &ce) || ce.path != tc.path {
			t.Errorf("%s: err = %v, want a request error at %s", tc.body, err, tc.path)
		}
	}
}

// Fields without an equivalent whose absence keeps the request correct are
// left out, and each left-out path is reported.
func TestChatResponsesRequestDropped(t *testing.T) {
	conv, out, err := (&chatResponses{}).Prepare([]byte(`{"model":"m","max_tokens":10,"max_completion_tokens":20,
		"stop":["\n"],"seed":7,"presence_penalty":0.5,"frequency_penalty":-1,"logit_bias":{"50256":-100},
		"logprobs":true,"top_logprobs":3,"prediction":{"type":"content","content":"x"},"made_up":1,
		"modalities":["text","image"],"stream_options":{"include_usage":true,"other":true},
		"tools":[{"type":"function","cache_control":{},"function":{"name":"f","extra":1}}],
		"tool_choice":"auto","function_call":"none",
		"response_format":{"type":"json_schema","json_schema":{"name":"o","extra":true}},
		"messages":[
			{"role":"system","name":"sys","content":[{"type":"text","text":"s","prompt_cache_breakpoint":{"mode":"explicit"}}]},
			{"role":"user","name":"bob","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},
				{"type":"image_url","image_url":{"url":"u","detail":"max"}}]},
			{"role":"assistant","name":"bot","content":"","refusal":""},
			{"role":"assistant","content":"ok","refusal":""},
			{"role":"user","content":"again"}
		]}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"other:frequency_penalty", "tool:function_call", "other:logit_bias", "other:logprobs",
		"other:made_up", "other:max_tokens",
		"other:messages[0].content[0].prompt_cache_breakpoint", "other:messages[0].name",
		"other:messages[1].content[0].cache_control", "image:messages[1].content[1].image_url.detail",
		"other:messages[1].name", "other:messages[2]", "other:messages[2].name", "other:messages[2].refusal",
		"other:messages[3].refusal", "other:modalities", "other:prediction", "other:presence_penalty",
		"other:response_format.json_schema.extra", "other:seed", "other:stop", "other:stream_options.other",
		"tool:tools[0].cache_control", "tool:tools[0].function.extra", "other:top_logprobs"}
	if got := lossStrings(Losses(conv)); !slices.Equal(got, want) {
		t.Errorf("losses:\n got %q\nwant %q", got, want)
	}
	j := gjson.ParseBytes(out)
	for _, absent := range []string{"stop", "seed", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs",
		"top_logprobs", "prediction", "made_up", "modalities", "stream_options", "max_tokens", "max_completion_tokens", "tools.0.cache_control", "tools.0.extra",
		"text.format.extra", "input.0.name", "input.0.content.0.cache_control"} {
		if j.Get(absent).Exists() {
			t.Errorf("%s left in the Responses request: %s", absent, out)
		}
	}
	for path, want := range map[string]string{
		"instructions": "s", "tool_choice": "auto", "max_output_tokens": "20", "input.#": "3", "input.0.content.1.detail": "auto",
		"input.1.role": "assistant", "input.1.content.#": "1", "input.1.content.0.text": "ok", "input.2.content.0.text": "again",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q (%s)", path, got, want, out)
		}
	}
	// Neutral values mean "not set" and are not reported.
	conv, _, err = (&chatResponses{}).Prepare([]byte(`{"model":"m","n":1,"logprobs":false,"top_logprobs":0,"presence_penalty":0,
		"frequency_penalty":0.0,"logit_bias":{},"stop":[],"modalities":["text"],"seed":null,"messages":[{"role":"user","content":"hi"}]}`), Options{})
	if err != nil || len(Losses(conv)) != 0 {
		t.Errorf("neutral values: %v %v", err, Losses(conv))
	}
	if len(Losses(&chatResponses{})) != 0 {
		t.Error("unprepared converter reports losses")
	}
	// Reasoning history is a reasoning loss.
	conv, _, err = (&chatResponses{}).Prepare([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"},
		{"role":"assistant","content":"a","reasoning_content":"thought"},{"role":"user","content":"more"}]}`), Options{})
	if got := lossStrings(Losses(conv)); err != nil || !slices.Equal(got, []string{"reasoning:messages[1].reasoning_content"}) {
		t.Errorf("reasoning history: %v %q", err, got)
	}
}

// Legacy function calling: functions become function tools, function_call
// the tool choice, assistant function_call items with a synthesized call id
// that the function message answering it (matched by name) reuses; the
// response comes back as message.function_call.
func TestChatResponsesLegacyFunctions(t *testing.T) {
	c, out := chatResponsesPrepared(t, `{"model":"m",
		"functions":[{"name":"get","description":"fetch","parameters":{"type":"object","properties":{"q":{"type":"string"}}}},{"name":"now"}],
		"function_call":{"name":"get"},
		"messages":[
			{"role":"user","content":"look"},
			{"role":"assistant","content":null,"function_call":{"name":"get","arguments":"{\"q\":\"x\"}"}},
			{"role":"function","name":"get","content":"result"},
			{"role":"user","content":"more"}
		]}`)
	j := gjson.Parse(out)
	for path, want := range map[string]string{
		"tools.#": "2", "tools.0.type": "function", "tools.0.name": "get", "tools.0.description": "fetch", "tools.0.strict": "false",
		"tools.0.parameters.properties.q.type": "string", "tools.1.name": "now", "tools.1.parameters.type": "object",
		"tool_choice.type": "function", "tool_choice.name": "get", "parallel_tool_calls": "false",
		"input.#": "4", "input.1.type": "function_call", "input.1.call_id": "call_legacy_1", "input.1.name": "get",
		"input.1.arguments": `{"q":"x"}`, "input.2.type": "function_call_output", "input.2.call_id": "call_legacy_1", "input.2.output": "result",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if j.Get("functions").Exists() || j.Get("function_call").Exists() {
		t.Errorf("legacy fields left: %s", out)
	}
	_, out = chatResponsesPrepared(t, `{"model":"m","functions":[{"name":"f"}],"function_call":"none","messages":[{"role":"user","content":"x"}]}`)
	if gjson.Get(out, "tool_choice").String() != "none" {
		t.Errorf("function_call none: %s", out)
	}

	resp, err := c.Response([]byte(`{"id":"r","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"get","arguments":"{}"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r := gjson.ParseBytes(resp)
	if r.Get("choices.0.finish_reason").String() != "function_call" || r.Get("choices.0.message.function_call.name").String() != "get" ||
		r.Get("choices.0.message.function_call.arguments").String() != "{}" || r.Get("choices.0.message.tool_calls").Exists() {
		t.Errorf("legacy response: %s", resp)
	}
	if _, err := c.Response([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"a","name":"get","arguments":"{}"},{"type":"function_call","call_id":"b","name":"now","arguments":"{}"}]}`)); err == nil {
		t.Error("two legacy function calls converted")
	}

	chunks, err := chatStreamRun(t, c,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"get","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"q\":1}"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`choices.0.delta.role=assistant`, `choices.0.delta.function_call.name=get`, `choices.0.delta.function_call.arguments={"q":`,
		`choices.0.delta.function_call.arguments=1}`, `choices.0.finish_reason=function_call`}
	if len(chunks) != len(want)+1 {
		t.Fatalf("legacy stream:\n%s", strings.Join(chunks, "\n"))
	}
	for i, w := range want {
		k, v, _ := strings.Cut(w, "=")
		if got := gjson.Get(chunks[i], k).String(); got != v || gjson.Get(chunks[i], "choices.0.delta.tool_calls").Exists() {
			t.Errorf("chunk %d %s = %q, want %q\n%s", i, k, got, v, chunks[i])
		}
	}

	// With tools as well, the response uses tool_calls.
	c, _ = chatResponsesPrepared(t, `{"model":"m","functions":[{"name":"f"}],"tools":[{"type":"function","function":{"name":"g"}}],"messages":[{"role":"user","content":"x"}]}`)
	resp, _ = c.Response([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}"}]}`))
	if gjson.GetBytes(resp, "choices.0.message.tool_calls.0.function.name").String() != "f" || gjson.GetBytes(resp, "choices.0.finish_reason").String() != "tool_calls" {
		t.Errorf("functions with tools: %s", resp)
	}
}

func TestChatResponsesCustomTools(t *testing.T) {
	_, out := chatResponsesPrepared(t, `{"model":"m",
		"tools":[
			{"type":"function","function":{"name":"get"}},
			{"type":"custom","custom":{"name":"shell","description":"run a command"}},
			{"type":"custom","custom":{"name":"text","format":{"type":"text"}}},
			{"type":"custom","custom":{"name":"sql","format":{"type":"grammar","grammar":{"definition":"start: SELECT","syntax":"lark"}}}}
		],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"get"}},{"type":"custom","custom":{"name":"shell"}}]}},
		"messages":[
			{"role":"user","content":"go"},
			{"role":"assistant","tool_calls":[
				{"id":"call_f","type":"function","function":{"name":"get","arguments":"{}"}},
				{"id":"call_c","type":"custom","custom":{"name":"shell","input":"ls -la"}}]},
			{"role":"tool","tool_call_id":"call_f","content":"fn result"},
			{"role":"tool","tool_call_id":"call_c","content":"file list"},
			{"role":"tool","tool_call_id":"call_unknown","content":[{"type":"text","text":"x"}]}
		]}`)
	j := gjson.Parse(out)
	for path, want := range map[string]string{
		"tools.1.type": "custom", "tools.1.name": "shell", "tools.1.description": "run a command",
		"tools.2.format.type": "text", "tools.3.format.type": "grammar", "tools.3.format.syntax": "lark",
		"tools.3.format.definition": "start: SELECT",
		"tool_choice.type":          "allowed_tools", "tool_choice.mode": "required", "tool_choice.tools.#": "2",
		"tool_choice.tools.0.type": "function", "tool_choice.tools.0.name": "get",
		"tool_choice.tools.1.type": "custom", "tool_choice.tools.1.name": "shell",
		"input.1.type": "function_call", "input.2.type": "custom_tool_call", "input.2.call_id": "call_c",
		"input.2.name": "shell", "input.2.input": "ls -la",
		"input.3.type": "function_call_output", "input.3.call_id": "call_f",
		"input.4.type": "custom_tool_call_output", "input.4.call_id": "call_c", "input.4.output": "file list",
		"input.5.type": "function_call_output", "input.5.output.0.type": "input_text",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if j.Get("tools.1.custom").Exists() || j.Get("tools.3.format.grammar").Exists() || j.Get("tools.1.parameters").Exists() {
		t.Errorf("chat shapes left: %s", out)
	}
	_, out = chatResponsesPrepared(t, `{"model":"m","tools":[{"type":"custom","custom":{"name":"shell"}}],"tool_choice":{"type":"custom","custom":{"name":"shell"}},"messages":[{"role":"user","content":"x"}]}`)
	if gjson.Get(out, "tool_choice.type").String() != "custom" || gjson.Get(out, "tool_choice.name").String() != "shell" {
		t.Errorf("custom tool choice: %s", out)
	}
}

func TestChatResponsesWebSearchOptions(t *testing.T) {
	_, out := chatResponsesPrepared(t, `{"model":"m","tools":[{"type":"function","function":{"name":"f"}}],
		"web_search_options":{"search_context_size":"high","user_location":{"type":"approximate","approximate":{"city":"Paris","country":"FR","region":"IDF","timezone":"Europe/Paris"}}},
		"messages":[{"role":"user","content":"news"}]}`)
	j := gjson.Parse(out)
	for path, want := range map[string]string{
		"tools.#": "2", "tools.1.type": "web_search", "tools.1.search_context_size": "high",
		"tools.1.user_location.type": "approximate", "tools.1.user_location.city": "Paris", "tools.1.user_location.country": "FR",
		"tools.1.user_location.region": "IDF", "tools.1.user_location.timezone": "Europe/Paris",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if j.Get("web_search_options").Exists() || j.Get("tools.1.user_location.approximate").Exists() {
		t.Errorf("chat shapes left: %s", out)
	}
	_, out = chatResponsesPrepared(t, `{"model":"m","web_search_options":{},"messages":[{"role":"user","content":"news"}]}`)
	if gjson.Get(out, "tools").Raw != `[{"type":"web_search"}]` {
		t.Errorf("empty web_search_options: %s", out)
	}
}

// New parameters both APIs share pass through, as do content-part cache
// breakpoints; detail original is kept.
func TestChatResponsesSharedParameters(t *testing.T) {
	c, out := chatResponsesPrepared(t, `{"model":"m","prompt_cache_options":{"mode":"explicit"},"moderation":{"enabled":true},
		"messages":[{"role":"user","content":[{"type":"text","text":"a","prompt_cache_breakpoint":{"mode":"explicit","ttl":"30m"}},
			{"type":"image_url","image_url":{"url":"u","detail":"original"},"prompt_cache_breakpoint":{"mode":"explicit"}}]},
			{"role":"assistant","content":[{"type":"text","text":"x"},{"type":"refusal","refusal":"no"}],"refusal":"still no"},
			{"role":"developer","content":"late"}]}`)
	j := gjson.Parse(out)
	for path, want := range map[string]string{
		"prompt_cache_options.mode": "explicit", "moderation.enabled": "true",
		"input.0.content.0.prompt_cache_breakpoint.ttl": "30m", "input.0.content.1.detail": "original",
		"input.0.content.1.prompt_cache_breakpoint.mode": "explicit",
		"input.1.content.#": "3", "input.1.content.0.type": "output_text", "input.1.content.1.type": "refusal",
		"input.1.content.1.refusal": "no", "input.1.content.2.refusal": "still no",
		"input.2.role": "developer", "input.2.content.0.text": "late",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q (%s)", path, got, want, out)
		}
	}
	if len(c.Losses()) != 0 {
		t.Errorf("losses %v", c.Losses())
	}
}

func TestChatResponsesJSON(t *testing.T) {
	c, _ := chatResponsesPrepared(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	out, err := c.Response([]byte(`{"id":"resp_1","object":"response","created_at":1700000000,"model":"gpt-5","status":"completed","service_tier":"default",
		"output":[{"type":"reasoning","id":"rs","summary":[{"type":"summary_text","text":"Plan."},{"type":"summary_text","text":"Act."}],"encrypted_content":"gAAA"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello ","annotations":[{"type":"url_citation","start_index":0,"end_index":5,"url":"https://a","title":"A"}]},{"type":"output_text","text":"world"}]},
			{"type":"function_call","call_id":"call_9","name":"get","arguments":"{}"}],
		"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	j := gjson.ParseBytes(out)
	for path, want := range map[string]string{
		"id": "resp_1", "object": "chat.completion", "created": "1700000000", "model": "gpt-5", "service_tier": "default",
		"choices.0.index": "0", "choices.0.finish_reason": "tool_calls", "choices.0.message.role": "assistant",
		"choices.0.message.content": "Hello world", "choices.0.message.tool_calls.0.id": "call_9",
		"choices.0.message.reasoning_content": "Plan.\n\nAct.",
		"choices.0.message.tool_calls.0.type": "function", "choices.0.message.tool_calls.0.function.name": "get",
		"choices.0.message.tool_calls.0.function.arguments": "{}",
		"choices.0.message.annotations.0.type":              "url_citation", "choices.0.message.annotations.0.url_citation.url": "https://a",
		"choices.0.message.annotations.0.url_citation.end_index": "5",
		"usage.prompt_tokens": "12", "usage.completion_tokens": "5", "usage.total_tokens": "17",
		"usage.prompt_tokens_details.cached_tokens": "3", "usage.completion_tokens_details.reasoning_tokens": "2",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if j.Get("choices.0.message.refusal").Type != gjson.Null {
		t.Errorf("refusal: %s", out)
	}

	for _, tc := range []struct{ body, finish string }{
		{`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, "stop"},
		{`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`, "length"},
		{`{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`, "content_filter"},
		{`{"status":"incomplete","incomplete_details":{"reason":"some_new_reason"},"output":[]}`, "length"},
		{`{"status":"incomplete","output":[]}`, "length"},
	} {
		out, err := c.Response([]byte(tc.body))
		if err != nil || gjson.GetBytes(out, "choices.0.finish_reason").String() != tc.finish {
			t.Errorf("%s: %s %v", tc.body, out, err)
		}
	}
	for _, bad := range []string{
		`{"status":"failed","output":[]}`,
		`{"status":"cancelled","output":[]}`,
		`{"status":"completed","output":[{"type":"computer_call","call_id":"c"}]}`,
		`{"status":"completed","output":[{"type":"image_generation_call","result":"AA"}]}`,
		`{"status":"completed","output":[{"type":"custom_tool_call","call_id":"c","name":"x"}]}`,
	} {
		if _, err := c.Response([]byte(bad)); err == nil {
			t.Errorf("%s converted", bad)
		}
	}
	if _, err := (&chatResponses{}).Response([]byte(`{}`)); !errors.Is(err, errUnprepared) {
		t.Errorf("unprepared converter: %v", err)
	}
}

// Custom tool calls convert both ways; server-side tool calls, reasoning,
// unknown items, unknown parts and non-URL annotations are left out;
// url_citation offsets of later text parts move by the text before them.
func TestChatResponsesJSONLenient(t *testing.T) {
	c, _ := chatResponsesPrepared(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	out, err := c.Response([]byte(`{"id":"r","status":"completed","output":[
		{"type":"web_search_call","id":"ws","status":"completed","action":{"type":"search","query":"q"}},
		{"type":"some_future_item","id":"x"},
		{"type":"message","role":"assistant","content":[
			{"type":"output_text","text":"Héllo ","annotations":[{"type":"url_citation","start_index":0,"end_index":5,"url":"https://a","title":"A"}]},
			{"type":"output_audio","data":"AA"},
			{"type":"output_text","text":"world","annotations":[{"type":"file_citation","file_id":"f","index":0},{"type":"url_citation","start_index":0,"end_index":5,"url":"https://b","title":"B"}]}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"!","annotations":[{"type":"url_citation","start_index":0,"end_index":1,"url":"https://c","title":"C"}]}]},
		{"type":"custom_tool_call","call_id":"call_c","name":"shell","input":"ls"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	j := gjson.ParseBytes(out)
	for path, want := range map[string]string{
		"choices.0.message.content": "Héllo world!", "choices.0.finish_reason": "tool_calls",
		"choices.0.message.annotations.#":                          "3",
		"choices.0.message.annotations.0.url_citation.end_index":   "5",
		"choices.0.message.annotations.1.url_citation.url":         "https://b",
		"choices.0.message.annotations.1.url_citation.start_index": "6", "choices.0.message.annotations.1.url_citation.end_index": "11",
		"choices.0.message.annotations.2.url_citation.start_index": "11",
		"choices.0.message.tool_calls.0.id":                        "call_c", "choices.0.message.tool_calls.0.type": "custom",
		"choices.0.message.tool_calls.0.custom.name": "shell", "choices.0.message.tool_calls.0.custom.input": "ls",
	} {
		if got := j.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q (%s)", path, got, want, out)
		}
	}
	if j.Get("choices.0.message.tool_calls.0.function").Exists() {
		t.Errorf("custom call has a function: %s", out)
	}
	if got, want := lossStrings(c.Losses()), []string{"tool:response.output[0]", "other:response.output[1]"}; !slices.Equal(got, want) {
		t.Errorf("response losses = %q, want %q", got, want)
	}
}

func TestChatResponsesStreamLenient(t *testing.T) {
	c, _ := chatResponsesPrepared(t, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := chatStreamRun(t, c,
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5","created_at":1700000000}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"web_search_call","id":"ws"}}`,
		`{"type":"response.web_search_call.searching","output_index":0,"item_id":"ws"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws","status":"completed"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"some_future_item"}}`,
		`{"type":"response.content_part.added","output_index":2,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":2,"content_index":0,"delta":"ab"}`,
		`{"type":"response.output_text.delta","output_index":2,"content_index":1,"delta":"cd"}`,
		`{"type":"response.output_text.annotation.added","output_index":2,"content_index":1,"annotation_index":0,"annotation":{"type":"url_citation","start_index":0,"end_index":2,"url":"https://a","title":"A"}}`,
		`{"type":"response.output_text.annotation.added","output_index":2,"content_index":1,"annotation_index":1,"annotation":{"type":"file_citation","file_id":"f"}}`,
		`{"type":"response.output_item.added","output_index":3,"item":{"type":"custom_tool_call","call_id":"call_c","name":"shell","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":3,"item_id":"ctc","delta":"ls "}`,
		`{"type":"response.custom_tool_call_input.done","output_index":3,"item_id":"ctc","input":"ls -la"}`,
		`{"type":"response.output_item.done","output_index":3,"item":{"type":"custom_tool_call","call_id":"call_c","name":"shell","input":"ls -la"}}`,
		`{"type":"response.output_item.done","output_index":4,"item":{"type":"function_call","call_id":"call_f","name":"get","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`)
	if err != nil {
		t.Fatalf("stream: %v (%v)", err, out)
	}
	want := []string{
		`choices.0.delta.role=assistant`,
		`choices.0.delta.content=ab`,
		`choices.0.delta.content=cd`,
		`choices.0.delta.annotations.0.url_citation.start_index=2`,
		`choices.0.delta.tool_calls.0.type=custom`,
		`choices.0.delta.tool_calls.0.custom.input=ls `,
		`choices.0.delta.tool_calls.0.custom.input=-la`,
		`choices.0.delta.tool_calls.0.function.arguments={}`,
		`choices.0.finish_reason=tool_calls`,
	}
	if len(out) != len(want)+1 || out[len(out)-1] != "[DONE]" {
		t.Fatalf("chunks:\n%s", strings.Join(out, "\n"))
	}
	for i, w := range want {
		k, v, _ := strings.Cut(w, "=")
		if got := gjson.Get(out[i], k).String(); got != v {
			t.Errorf("chunk %d %s = %q, want %q\n%s", i, k, got, v, out[i])
		}
	}
	for path, want := range map[string]string{"choices.0.delta.tool_calls.0.custom.name": "shell", "choices.0.delta.tool_calls.0.id": "call_c",
		"choices.0.delta.tool_calls.0.index": "0", "choices.0.delta.annotations.0.url_citation.end_index": "4"} {
		i := 4
		if strings.Contains(path, "annotations") {
			i = 3
		}
		if got := gjson.Get(out[i], path).String(); got != want {
			t.Errorf("chunk %d %s = %q, want %q", i, path, got, want)
		}
	}
	// A function call only announced by output_item.done gets index 1, id
	// and name in one chunk.
	if gjson.Get(out[7], "choices.0.delta.tool_calls.0.index").Int() != 1 || gjson.Get(out[7], "choices.0.delta.tool_calls.0.id").String() != "call_f" ||
		gjson.Get(out[7], "choices.0.delta.tool_calls.0.function.name").String() != "get" {
		t.Errorf("done-only call: %s", out[7])
	}
	if got, want := lossStrings(c.Losses()), []string{"tool:response.output[0]", "other:response.output[1]"}; !slices.Equal(got, want) {
		t.Errorf("response losses = %q, want %q", got, want)
	}
}

// Reasoning streams as reasoning_content, parts separated by a blank line;
// encrypted reasoning is a response loss.
func TestChatResponsesStreamReasoning(t *testing.T) {
	c, _ := chatResponsesPrepared(t, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := chatStreamRun(t, c,
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Pl"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"an."}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":1,"delta":"Act."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs","encrypted_content":"gAAA"}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"ok"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`)
	if err != nil {
		t.Fatalf("stream: %v (%v)", err, out)
	}
	var reasoning string
	for _, o := range out {
		reasoning += gjson.Get(o, "choices.0.delta.reasoning_content").String()
	}
	if reasoning != "Plan.\n\nAct." {
		t.Errorf("reasoning_content = %q", reasoning)
	}
	if got := lossStrings(c.Losses()); !slices.Equal(got, []string{"reasoning:response.output[0].encrypted_content"}) {
		t.Errorf("response losses = %q", got)
	}
	// Like a pair converter, the losses are those of the latest stream.
	if _, err := chatStreamRun(t, c, `{"type":"response.created","response":{"id":"resp_2","model":"gpt-5"}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ok"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`); err != nil {
		t.Fatal(err)
	}
	if got := c.Losses(); len(got) != 0 {
		t.Errorf("losses of an earlier stream kept: %v", got)
	}
}

func chatStreamRun(t *testing.T, c *chatResponses, events ...string) ([]string, error) {
	t.Helper()
	s := c.NewStream()
	var out []string
	for _, e := range events {
		name := gjson.Get(e, "type").String()
		evs, err := s.Event(Event{Name: name, Data: []byte(e)})
		if err != nil {
			return out, err
		}
		for _, ev := range evs {
			out = append(out, string(ev.Data))
		}
	}
	evs, err := s.Flush()
	for _, ev := range evs {
		out = append(out, string(ev.Data))
	}
	return out, err
}

func TestChatResponsesStream(t *testing.T) {
	c, _ := chatResponsesPrepared(t, `{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	out, err := chatStreamRun(t, c,
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5","created_at":1700000000,"status":"in_progress","output":[]}}`,
		`{"type":"response.in_progress","response":{}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"thinking"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"Hel"}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"lo"}`,
		`{"type":"response.output_text.done","output_index":1,"content_index":0,"text":"Hello"}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"call_1","name":"get","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"q\":"}`,
		`{"type":"response.function_call_arguments.done","output_index":2,"arguments":"{\"q\":1}"}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","call_id":"call_1","name":"get","arguments":"{\"q\":1}"}}`,
		`{"type":"response.output_item.added","output_index":3,"item":{"type":"function_call","call_id":"call_2","name":"now","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":3,"item":{"type":"function_call","call_id":"call_2","name":"now","arguments":"{}"}}`,
		`{"type":"codex.rate_limits","primary":{}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","status":"completed","usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17,"input_tokens_details":{"cached_tokens":3}}}}`,
	)
	if err != nil {
		t.Fatalf("stream: %v (%v)", err, out)
	}
	want := []string{
		`choices.0.delta.role=assistant`,
		`choices.0.delta.reasoning_content=thinking`,
		`choices.0.delta.content=Hel`,
		`choices.0.delta.content=lo`,
		`choices.0.delta.tool_calls.0.id=call_1`,
		`choices.0.delta.tool_calls.0.function.arguments={"q":`,
		`choices.0.delta.tool_calls.0.function.arguments=1}`,
		`choices.0.delta.tool_calls.0.index=1`,
		`choices.0.delta.tool_calls.0.function.arguments={}`,
		`choices.0.finish_reason=tool_calls`,
		`usage.prompt_tokens=12`,
	}
	if len(out) != len(want)+1 || out[len(out)-1] != "[DONE]" {
		t.Fatalf("chunks:\n%s", strings.Join(out, "\n"))
	}
	for i, w := range want {
		k, v, _ := strings.Cut(w, "=")
		if got := gjson.Get(out[i], k).String(); got != v {
			t.Errorf("chunk %d %s = %q, want %q\n%s", i, k, got, v, out[i])
		}
		if gjson.Get(out[i], "id").String() != "resp_1" || gjson.Get(out[i], "object").String() != "chat.completion.chunk" ||
			gjson.Get(out[i], "model").String() != "gpt-5" {
			t.Errorf("chunk %d identity: %s", i, out[i])
		}
	}
	if gjson.Get(out[10], "choices.#").Int() != 0 || gjson.Get(out[10], "usage.prompt_tokens_details.cached_tokens").Int() != 3 ||
		gjson.Get(out[1], "usage").Type != gjson.Null || !gjson.Get(out[1], "usage").Exists() {
		t.Errorf("usage chunks: %s / %s", out[10], out[1])
	}

	// Without include_usage: no usage chunk and no usage field; length.
	c, _ = chatResponsesPrepared(t, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err = chatStreamRun(t, c,
		`{"type":"response.output_text.delta","output_index":0,"delta":"x"}`,
		`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":1}}}`)
	if err != nil || len(out) != 4 || gjson.Get(out[2], "choices.0.finish_reason").String() != "length" || gjson.Get(out[2], "usage").Exists() {
		t.Fatalf("incomplete stream: %v %v", out, err)
	}

	// A failed response ends the stream with an error chunk.
	out, err = chatStreamRun(t, c,
		`{"type":"response.output_text.delta","output_index":0,"delta":"x"}`,
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`)
	if err != nil || len(out) != 3 || gjson.Get(out[2], "error.message").String() != "boom" {
		t.Fatalf("failed stream: %v %v", out, err)
	}
	// A truncated stream fails at Flush.
	if _, err = chatStreamRun(t, c, `{"type":"response.output_text.delta","output_index":0,"delta":"x"}`); err == nil {
		t.Fatal("truncated stream flushed cleanly")
	}
	// Output items a chat completion cannot carry are refused.
	if _, err = chatStreamRun(t, c, `{"type":"response.output_item.added","output_index":0,"item":{"type":"computer_call","call_id":"c"}}`); err == nil {
		t.Fatal("computer_call converted")
	}
	// Final arguments must extend the streamed ones.
	_, err = chatStreamRun(t, c,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c","name":"f","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\""}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"b\":1}"}`)
	if err == nil {
		t.Fatal("diverging arguments accepted")
	}
}

// The request conversion keeps JSON numbers exactly as sent.
func TestChatResponsesNumbersVerbatim(t *testing.T) {
	_, out := chatResponsesPrepared(t, `{"model":"m","temperature":0.30000000000000004,"messages":[{"role":"user","content":"hi"}]}`)
	var v map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out), &v)
	if string(v["temperature"]) != "0.30000000000000004" {
		t.Fatalf("temperature = %s", v["temperature"])
	}
}
