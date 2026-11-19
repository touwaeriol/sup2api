package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// chatResponses serves an openai.chat client with an openai.responses
// upstream (CONTRACTS §83.2). Being a compatibility layer it is lenient:
//
//   - a request field with an exact Responses equivalent is mapped (legacy
//     functions, custom tools, allowed_tools, web_search_options, ...);
//   - a field without an equivalent whose absence leaves the request
//     semantically correct (stop, seed, penalties, logprobs, participant
//     names, unknown fields, ...) is left out and recorded as a Loss;
//   - a field whose meaning cannot be kept at all (n > 1, audio output,
//     audio input, an empty conversation, ...) is rejected
//     (400 unsupported_conversion);
//   - an explicit value equal to the Chat Completions default (n: 1,
//     logprobs: false, a zero penalty, ...) means the same as leaving it
//     out and is neither rejected nor recorded.
//
// The reply's reasoning summary reaches the client as reasoning_content
// (CONVERT-MATRIX D4); encrypted reasoning does not (a response loss).
//
// The registry entry is stateless; Prepare returns a per-request copy that
// remembers what the response conversion needs (stream_options.include_usage,
// legacy function calling) and what the conversion lost.
type chatResponses struct {
	includeUsage bool
	// legacy: the request used functions without tools, so the response
	// returns message.function_call instead of tool_calls.
	legacy   bool
	prepared bool
	lost     losses
	// reqLosses is how many entries of lost the request has; the rest are
	// the latest response's.
	reqLosses int
}

func (c *chatResponses) From() string { return "openai.chat" }
func (c *chatResponses) To() string   { return "openai.responses" }
func (c *chatResponses) Grade() Grade { return GradeGood }

func (c *chatResponses) Prepare(body []byte, _ Options) (Converter, []byte, error) {
	p := &chatResponses{prepared: true}
	out, err := p.request(body)
	if err != nil {
		return nil, nil, &RequestError{Cause: err}
	}
	p.reqLosses = len(p.lost.list)
	return p, out, nil
}

// Losses implements LossReporter: the request losses, then those of the
// latest response or stream.
func (c *chatResponses) Losses() []Loss { return c.lost.sorted() }

// resetResponseLosses forgets the losses of an earlier response or stream.
func (c *chatResponses) resetResponseLosses() { c.lost.list = c.lost.list[:c.reqLosses] }

// drop records a request path left out, with the category its place in the
// request implies (CONVERT-MATRIX §9.9).
func (c *chatResponses) drop(path string) { c.lost.add(chatLossCategory(path), path) }

// chatToolMembers are the request members whose content is tool
// definitions or a tool choice.
var chatToolMembers = []string{"tools", "functions", "function_call", "tool_choice", "web_search_options"}

// chatLossCategory is the loss category of a chat request path.
func chatLossCategory(path string) LossCategory {
	root, _, _ := strings.Cut(path, ".")
	root, _, _ = strings.Cut(root, "[")
	switch {
	case slices.Contains(chatToolMembers, root), strings.Contains(path, ".tool_calls"):
		return LossTool
	case strings.HasSuffix(path, ".image_url.detail"):
		return LossImage
	case strings.HasSuffix(path, ".reasoning_content"), strings.HasSuffix(path, ".reasoning"):
		return LossReasoning
	}
	return LossOther
}

// ---------------------------------------------------------------- helpers

// errUnprepared fails a response conversion on the registry entry.
var errUnprepared = errors.New("convert: response requires a prepared request")

type jobject = map[string]any

// conversionError names the offending request or response path.
type conversionError struct{ path, reason string }

func (e *conversionError) Error() string {
	return "unsupported_conversion: " + e.path + ": " + e.reason
}

func cfail(path, reason string) error { return &conversionError{path, reason} }

func decodeJObject(raw []byte, path string) (jobject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, cfail(path, "invalid JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, cfail(path, "multiple JSON values")
	}
	o, ok := v.(map[string]any)
	if !ok || o == nil {
		return nil, cfail(path, "expected object")
	}
	return o, nil
}

func jobj(v any, path string) (jobject, error) {
	o, ok := v.(map[string]any)
	if !ok || o == nil {
		return nil, cfail(path, "expected object")
	}
	return o, nil
}

func jlist(v any, path string) ([]any, error) {
	a, ok := v.([]any)
	if !ok {
		return nil, cfail(path, "expected array")
	}
	return a, nil
}

func jstr(v any, path string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", cfail(path, "expected string")
	}
	return s, nil
}

func jnonempty(v any, path string) (string, error) {
	s, err := jstr(v, path)
	if err == nil && s == "" {
		err = cfail(path, "expected nonempty string")
	}
	return s, err
}

func jbool(v any, path string) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, cfail(path, "expected boolean")
	}
	return b, nil
}

func jnumber(v any, path string) (json.Number, error) {
	n, ok := v.(json.Number)
	if !ok {
		return "", cfail(path, "expected number")
	}
	return n, nil
}

func jint(v any, path string, lo, hi int64) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, cfail(path, "expected integer")
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i < lo || i > hi {
		return 0, cfail(path, "integer out of range")
	}
	return i, nil
}

// keep removes the null members of o (null means "not set") and the
// members not listed, recording each removed non-null member as dropped.
func (c *chatResponses) keep(o jobject, path string, names ...string) {
	dropUnknown(o, path, c.drop, names...)
}

// optStr copies the string member k of src to dst when present.
func optStr(dst, src jobject, k, path string) error {
	v, ok := src[k]
	if !ok || v == nil {
		return nil
	}
	s, err := jstr(v, path+"."+k)
	if err != nil {
		return err
	}
	dst[k] = s
	return nil
}

func jpos(path string, i int) string { return fmt.Sprintf("%s[%d]", path, i) }

// isZero reports a JSON number equal to zero.
func isZero(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	return err == nil && f == 0
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// ---------------------------------------------------------------- request

// chatDropped are Chat Completions fields without a Responses equivalent
// whose absence keeps the request correct; they are left out and recorded
// unless their value is the Chat default (neutral).
var chatDropped = map[string]func(any) bool{
	"logprobs":          func(v any) bool { return v == false },
	"top_logprobs":      isZero,
	"logit_bias":        isEmpty,
	"presence_penalty":  isZero,
	"frequency_penalty": isZero,
	"stop":              isEmpty,
	"seed":              func(any) bool { return false },
	"prediction":        func(any) bool { return false },
}

// chatPassThrough are fields with the same name, type and meaning in both
// APIs; the value is checked for its JSON type only.
var chatPassThrough = map[string]string{
	"temperature":            "number",
	"top_p":                  "number",
	"parallel_tool_calls":    "boolean",
	"service_tier":           "string",
	"user":                   "string",
	"safety_identifier":      "string",
	"prompt_cache_key":       "string",
	"prompt_cache_retention": "string",
	"prompt_cache_options":   "object",
	"moderation":             "object",
	"metadata":               "object",
	"store":                  "boolean",
	"stream":                 "boolean",
}

func (c *chatResponses) request(raw []byte) ([]byte, error) {
	in, err := decodeJObject(raw, "request")
	if err != nil {
		return nil, err
	}
	// An explicit null means "not set" for every optional field.
	for k, v := range in {
		if v == nil {
			delete(in, k)
		}
	}
	out := jobject{}
	for k, v := range in {
		switch k {
		case "model", "messages", "max_tokens", "max_completion_tokens", "stream_options", "tools", "tool_choice",
			"functions", "function_call", "web_search_options", "response_format", "reasoning_effort", "verbosity":
			continue
		case "n":
			if i, err := jint(v, "n", 1, 1); err != nil || i != 1 {
				return nil, cfail("n", "only one completion has an equivalent")
			}
			continue
		case "audio":
			return nil, cfail("audio", "audio output has no Responses equivalent")
		case "modalities":
			mods, _ := v.([]any)
			if slices.Contains(mods, any("audio")) {
				return nil, cfail("modalities", "audio output has no Responses equivalent")
			}
			if len(mods) != 1 || mods[0] != "text" {
				c.drop(k)
			}
			continue
		}
		if neutral, ok := chatDropped[k]; ok {
			if !neutral(v) {
				c.drop(k)
			}
			continue
		}
		typ, ok := chatPassThrough[k]
		if !ok {
			c.drop(k)
			continue
		}
		switch typ {
		case "number":
			_, err = jnumber(v, k)
		case "boolean":
			_, err = jbool(v, k)
		case "string":
			_, err = jstr(v, k)
		case "object":
			_, err = jobj(v, k)
		}
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	// Chat Completions does not store by default; Responses does.
	if _, ok := out["store"]; !ok {
		out["store"] = false
	}
	if out["model"], err = jnonempty(in["model"], "model"); err != nil {
		return nil, err
	}
	if err = c.maxTokens(in, out); err != nil {
		return nil, err
	}
	if err = c.streamOptions(in, out); err != nil {
		return nil, err
	}
	if err = c.chatTools(in, out); err != nil {
		return nil, err
	}
	if err = c.chatTextControls(in, out); err != nil {
		return nil, err
	}
	if v, ok := in["reasoning_effort"]; ok {
		effort, err := jnonempty(v, "reasoning_effort")
		if err != nil {
			return nil, err
		}
		out["reasoning"] = jobject{"effort": effort}
	}
	if err = c.chatMessages(in, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// maxTokens maps max_completion_tokens (or its deprecated alias max_tokens)
// to max_output_tokens; when both differ max_completion_tokens wins and
// max_tokens is dropped.
func (c *chatResponses) maxTokens(in, out jobject) error {
	a, hasA := in["max_completion_tokens"]
	b, hasB := in["max_tokens"]
	if hasA && hasB {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		if !bytes.Equal(x, y) {
			c.drop("max_tokens")
		}
	}
	key, v := "max_completion_tokens", a
	if !hasA {
		key, v = "max_tokens", b
	}
	if !hasA && !hasB {
		return nil
	}
	n, err := jint(v, key, 1, math.MaxInt32)
	if err != nil {
		return err
	}
	out["max_output_tokens"] = n
	return nil
}

func (c *chatResponses) streamOptions(in, out jobject) error {
	v, ok := in["stream_options"]
	if !ok {
		return nil
	}
	o, err := jobj(v, "stream_options")
	if err != nil {
		return err
	}
	c.keep(o, "stream_options", "include_usage", "include_obfuscation")
	if x, ok := o["include_usage"]; ok {
		if c.includeUsage, err = jbool(x, "stream_options.include_usage"); err != nil {
			return err
		}
	}
	if x, ok := o["include_obfuscation"]; ok {
		b, err := jbool(x, "stream_options.include_obfuscation")
		if err != nil {
			return err
		}
		out["stream_options"] = jobject{"include_obfuscation": b}
	}
	return nil
}

// chatTools maps tools (function and custom), legacy functions,
// web_search_options, tool_choice and the legacy function_call. Chat
// function tools are not strict unless they say so; Responses function
// tools are strict by default, so strict is always written.
func (c *chatResponses) chatTools(in, out jobject) error {
	mapped := []any{}
	_, hasTools := in["tools"]
	if v, ok := in["tools"]; ok {
		tools, err := jlist(v, "tools")
		if err != nil {
			return err
		}
		for i, t := range tools {
			path := jpos("tools", i)
			tool, err := jobj(t, path)
			if err != nil {
				return err
			}
			var o jobject
			switch tool["type"] {
			case "function":
				c.keep(tool, path, "type", "function")
				o, err = c.functionTool(tool["function"], path+".function")
			case "custom":
				c.keep(tool, path, "type", "custom")
				o, err = c.customTool(tool["custom"], path+".custom")
			default:
				return cfail(path+".type", "only function and custom tools are converted")
			}
			if err != nil {
				return err
			}
			mapped = append(mapped, o)
		}
	}
	if v, ok := in["functions"]; ok {
		fns, err := jlist(v, "functions")
		if err != nil {
			return err
		}
		for i, f := range fns {
			o, err := c.functionTool(f, jpos("functions", i))
			if err != nil {
				return err
			}
			mapped = append(mapped, o)
		}
		// Legacy function calling answers with message.function_call, one
		// call per message.
		if !hasTools {
			c.legacy = true
			if _, set := out["parallel_tool_calls"]; !set && len(fns) > 0 {
				out["parallel_tool_calls"] = false
			}
		}
	}
	if v, ok := in["web_search_options"]; ok {
		ws, err := c.webSearchTool(v)
		if err != nil {
			return err
		}
		mapped = append(mapped, ws)
	}
	if len(mapped) > 0 || hasTools {
		out["tools"] = mapped
	}
	if v, ok := in["tool_choice"]; ok {
		tc, err := c.toolChoice(v)
		if err != nil {
			return err
		}
		out["tool_choice"] = tc
		if _, legacy := in["function_call"]; legacy {
			c.drop("function_call")
		}
	} else if v, ok := in["function_call"]; ok {
		if s, isStr := v.(string); isStr {
			if s != "none" && s != "auto" {
				return cfail("function_call", "expected none, auto or a function")
			}
			out["tool_choice"] = s
		} else {
			o, err := jobj(v, "function_call")
			if err != nil {
				return err
			}
			c.keep(o, "function_call", "name")
			name, err := jnonempty(o["name"], "function_call.name")
			if err != nil {
				return err
			}
			out["tool_choice"] = jobject{"type": "function", "name": name}
		}
	}
	return nil
}

// functionTool maps a chat function definition {name, description?,
// parameters?, strict?} to a Responses function tool.
func (c *chatResponses) functionTool(v any, path string) (jobject, error) {
	fn, err := jobj(v, path)
	if err != nil {
		return nil, err
	}
	c.keep(fn, path, "name", "description", "parameters", "strict")
	name, err := jnonempty(fn["name"], path+".name")
	if err != nil {
		return nil, err
	}
	o := jobject{"type": "function", "name": name, "strict": false}
	if err = optStr(o, fn, "description", path); err != nil {
		return nil, err
	}
	// Omitted parameters mean a function without parameters.
	o["parameters"] = jobject{"type": "object", "properties": jobject{}}
	if ps, ok := fn["parameters"]; ok {
		if o["parameters"], err = jobj(ps, path+".parameters"); err != nil {
			return nil, err
		}
	}
	if s, ok := fn["strict"]; ok {
		if o["strict"], err = jbool(s, path+".strict"); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// customTool maps {name, description?, format?} to a Responses custom tool.
// The grammar format is nested in Chat ({type: grammar, grammar:
// {definition, syntax}}) and flat in Responses.
func (c *chatResponses) customTool(v any, path string) (jobject, error) {
	ct, err := jobj(v, path)
	if err != nil {
		return nil, err
	}
	c.keep(ct, path, "name", "description", "format")
	name, err := jnonempty(ct["name"], path+".name")
	if err != nil {
		return nil, err
	}
	o := jobject{"type": "custom", "name": name}
	if err = optStr(o, ct, "description", path); err != nil {
		return nil, err
	}
	if fv, ok := ct["format"]; ok {
		fp := path + ".format"
		f, err := jobj(fv, fp)
		if err != nil {
			return nil, err
		}
		switch f["type"] {
		case "text":
			c.keep(f, fp, "type")
			o["format"] = jobject{"type": "text"}
		case "grammar":
			c.keep(f, fp, "type", "grammar")
			g, err := jobj(f["grammar"], fp+".grammar")
			if err != nil {
				return nil, err
			}
			c.keep(g, fp+".grammar", "definition", "syntax")
			def, err := jstr(g["definition"], fp+".grammar.definition")
			if err != nil {
				return nil, err
			}
			syntax, err := jnonempty(g["syntax"], fp+".grammar.syntax")
			if err != nil {
				return nil, err
			}
			o["format"] = jobject{"type": "grammar", "syntax": syntax, "definition": def}
		default:
			return nil, cfail(fp+".type", "expected text or grammar")
		}
	}
	return o, nil
}

// webSearchTool maps web_search_options to a Responses web_search tool;
// the chat location nests its fields under approximate, Responses does not.
func (c *chatResponses) webSearchTool(v any) (jobject, error) {
	const path = "web_search_options"
	o, err := jobj(v, path)
	if err != nil {
		return nil, err
	}
	c.keep(o, path, "search_context_size", "user_location")
	ws := jobject{"type": "web_search"}
	if err = optStr(ws, o, "search_context_size", path); err != nil {
		return nil, err
	}
	if lv, ok := o["user_location"]; ok {
		lp := path + ".user_location"
		loc, err := jobj(lv, lp)
		if err != nil {
			return nil, err
		}
		c.keep(loc, lp, "type", "approximate")
		if t, ok := loc["type"]; ok && t != "approximate" {
			return nil, cfail(lp+".type", "expected approximate")
		}
		ul := jobject{"type": "approximate"}
		if av, ok := loc["approximate"]; ok {
			ap := lp + ".approximate"
			a, err := jobj(av, ap)
			if err != nil {
				return nil, err
			}
			c.keep(a, ap, "city", "country", "region", "timezone")
			for _, k := range []string{"city", "country", "region", "timezone"} {
				if err = optStr(ul, a, k, ap); err != nil {
					return nil, err
				}
			}
		}
		ws["user_location"] = ul
	}
	return ws, nil
}

// toolChoice maps none/auto/required, a named function or custom tool, and
// allowed_tools.
func (c *chatResponses) toolChoice(v any) (any, error) {
	if s, isStr := v.(string); isStr {
		switch s {
		case "none", "auto", "required":
			return s, nil
		}
		return nil, cfail("tool_choice", "expected none, auto, required or a tool")
	}
	o, err := jobj(v, "tool_choice")
	if err != nil {
		return nil, err
	}
	switch o["type"] {
	case "function", "custom":
		return c.namedTool(o, "tool_choice")
	case "allowed_tools":
		c.keep(o, "tool_choice", "type", "allowed_tools")
		at, err := jobj(o["allowed_tools"], "tool_choice.allowed_tools")
		if err != nil {
			return nil, err
		}
		c.keep(at, "tool_choice.allowed_tools", "mode", "tools")
		mode, err := jnonempty(at["mode"], "tool_choice.allowed_tools.mode")
		if err != nil {
			return nil, err
		}
		list, err := jlist(at["tools"], "tool_choice.allowed_tools.tools")
		if err != nil {
			return nil, err
		}
		tools := make([]any, 0, len(list))
		for i, t := range list {
			tp := jpos("tool_choice.allowed_tools.tools", i)
			to, err := jobj(t, tp)
			if err != nil {
				return nil, err
			}
			if to["type"] != "function" && to["type"] != "custom" {
				return nil, cfail(tp+".type", "only function and custom tools are converted")
			}
			named, err := c.namedTool(to, tp)
			if err != nil {
				return nil, err
			}
			tools = append(tools, named)
		}
		return jobject{"type": "allowed_tools", "mode": mode, "tools": tools}, nil
	}
	return nil, cfail("tool_choice.type", "expected function, custom or allowed_tools")
}

// namedTool maps {type: function|custom, <type>: {name}} to {type, name}.
func (c *chatResponses) namedTool(o jobject, path string) (jobject, error) {
	typ := o["type"].(string)
	c.keep(o, path, "type", typ)
	inner, err := jobj(o[typ], path+"."+typ)
	if err != nil {
		return nil, err
	}
	c.keep(inner, path+"."+typ, "name")
	name, err := jnonempty(inner["name"], path+"."+typ+".name")
	if err != nil {
		return nil, err
	}
	return jobject{"type": typ, "name": name}, nil
}

// chatTextControls maps response_format to text.format and verbosity to
// text.verbosity.
func (c *chatResponses) chatTextControls(in, out jobject) error {
	text := jobject{}
	if v, ok := in["verbosity"]; ok {
		s, err := jnonempty(v, "verbosity")
		if err != nil {
			return err
		}
		text["verbosity"] = s
	}
	if v, ok := in["response_format"]; ok {
		rf, err := jobj(v, "response_format")
		if err != nil {
			return err
		}
		switch rf["type"] {
		case "text", "json_object":
			c.keep(rf, "response_format", "type")
			text["format"] = jobject{"type": rf["type"]}
		case "json_schema":
			c.keep(rf, "response_format", "type", "json_schema")
			js, err := jobj(rf["json_schema"], "response_format.json_schema")
			if err != nil {
				return err
			}
			c.keep(js, "response_format.json_schema", "name", "description", "schema", "strict")
			f := jobject{"type": "json_schema"}
			if f["name"], err = jnonempty(js["name"], "response_format.json_schema.name"); err != nil {
				return err
			}
			if err = optStr(f, js, "description", "response_format.json_schema"); err != nil {
				return err
			}
			if s, ok := js["schema"]; ok {
				if f["schema"], err = jobj(s, "response_format.json_schema.schema"); err != nil {
					return err
				}
			}
			if s, ok := js["strict"]; ok {
				if f["strict"], err = jbool(s, "response_format.json_schema.strict"); err != nil {
					return err
				}
			}
			text["format"] = f
		default:
			return cfail("response_format.type", "expected text, json_object or json_schema")
		}
	}
	if len(text) > 0 {
		out["text"] = text
	}
	return nil
}

// chatConversation is the state of one messages conversion: which kind of
// call each tool_call_id named, and the legacy function calls still waiting
// for their function message.
type chatConversation struct {
	callKinds map[string]string
	legacy    []legacyCall
}

type legacyCall struct{ name, id string }

// chatMessages maps messages to instructions and input. The system and
// developer messages before the first other message become instructions
// (joined by a blank line); later ones become developer input messages.
// With text.format json_object the leading ones stay developer input
// messages too: Responses requires the word "json" in the input messages
// for that format and does not look at instructions, while Chat
// Completions accepts it in the system message.
func (c *chatResponses) chatMessages(in, out jobject) error {
	msgs, err := jlist(in["messages"], "messages")
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return cfail("messages", "at least one message is required")
	}
	conv := &chatConversation{callKinds: map[string]string{}}
	input := []any{}
	var instructions []string
	leading := !jsonObjectFormat(out)
	for i, m := range msgs {
		path := jpos("messages", i)
		msg, err := jobj(m, path)
		if err != nil {
			return err
		}
		role, err := jnonempty(msg["role"], path+".role")
		if err != nil {
			return err
		}
		if role == "system" || role == "developer" {
			c.keep(msg, path, "role", "content")
			parts, err := c.chatTextParts(msg["content"], path+".content")
			if err != nil {
				return err
			}
			if leading {
				instructions = append(instructions, c.instructionText(parts, path+".content"))
			} else {
				input = append(input, jobject{"type": "message", "role": "developer", "content": partsAny(parts)})
			}
			continue
		}
		leading = false
		items, err := c.chatMessage(msg, role, path, i, conv)
		if err != nil {
			return err
		}
		input = append(input, items...)
	}
	if len(instructions) > 0 {
		out["instructions"] = strings.Join(instructions, "\n\n")
	}
	out["input"] = input
	return nil
}

// jsonObjectFormat reports whether the converted body asks for text.format
// json_object.
func jsonObjectFormat(out jobject) bool {
	text, _ := out["text"].(jobject)
	format, _ := text["format"].(jobject)
	return format["type"] == "json_object"
}

// instructionText joins the text parts of a leading system or developer
// message into instructions, which have no cache breakpoints: one set on a
// part is dropped.
func (c *chatResponses) instructionText(parts []jobject, path string) string {
	var b strings.Builder
	for j, part := range parts {
		if _, ok := part["prompt_cache_breakpoint"]; ok {
			c.drop(jpos(path, j) + ".prompt_cache_breakpoint")
		}
		b.WriteString(part["text"].(string))
	}
	return b.String()
}

// chatMessage maps a message other than system and developer to its input
// items.
func (c *chatResponses) chatMessage(msg jobject, role, path string, index int, conv *chatConversation) ([]any, error) {
	switch role {
	case "user":
		c.keep(msg, path, "role", "content")
		content, err := c.chatUserContent(msg["content"], path+".content")
		if err != nil {
			return nil, err
		}
		return []any{jobject{"type": "message", "role": "user", "content": content}}, nil
	case "assistant":
		return c.chatAssistant(msg, path, index, conv)
	case "tool":
		item, err := c.chatToolMessage(msg, path, conv)
		if err != nil {
			return nil, err
		}
		return []any{item}, nil
	case "function":
		item, err := c.chatFunctionMessage(msg, path, conv)
		if err != nil {
			return nil, err
		}
		return []any{item}, nil
	}
	return nil, cfail(path+".role", "role "+strconv.Quote(role)+" is not converted")
}

// chatToolMessage maps a tool message to the output item of the call it
// answers: custom_tool_call_output for a custom call, function_call_output
// otherwise.
func (c *chatResponses) chatToolMessage(msg jobject, path string, conv *chatConversation) (jobject, error) {
	c.keep(msg, path, "role", "content", "tool_call_id")
	id, err := jnonempty(msg["tool_call_id"], path+".tool_call_id")
	if err != nil {
		return nil, err
	}
	typ := "function_call_output"
	if conv.callKinds[id] == "custom" {
		typ = "custom_tool_call_output"
	}
	item := jobject{"type": typ, "call_id": id}
	if s, ok := msg["content"].(string); ok {
		item["output"] = s
		return item, nil
	}
	parts, err := c.chatTextParts(msg["content"], path+".content")
	if err != nil {
		return nil, err
	}
	item["output"] = partsAny(parts)
	return item, nil
}

// chatFunctionMessage maps a legacy function message to the
// function_call_output of the latest earlier function_call with its name
// still unanswered.
func (c *chatResponses) chatFunctionMessage(msg jobject, path string, conv *chatConversation) (jobject, error) {
	c.keep(msg, path, "role", "content", "name")
	name, err := jnonempty(msg["name"], path+".name")
	if err != nil {
		return nil, err
	}
	id := ""
	for j := len(conv.legacy) - 1; j >= 0; j-- {
		if conv.legacy[j].name == name {
			id = conv.legacy[j].id
			conv.legacy = append(conv.legacy[:j], conv.legacy[j+1:]...)
			break
		}
	}
	if id == "" {
		return nil, cfail(path+".name", "no earlier assistant function_call has this name")
	}
	output := ""
	if cv, ok := msg["content"]; ok {
		if output, err = jstr(cv, path+".content"); err != nil {
			return nil, err
		}
	}
	return jobject{"type": "function_call_output", "call_id": id, "output": output}, nil
}

func partsAny(parts []jobject) []any {
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out
}

// chatTextParts reads a content that is a string or an array of text parts
// and returns Responses input_text parts.
func (c *chatResponses) chatTextParts(v any, path string) ([]jobject, error) {
	if s, ok := v.(string); ok {
		return []jobject{{"type": "input_text", "text": s}}, nil
	}
	parts, err := jlist(v, path)
	if err != nil {
		return nil, cfail(path, "expected string or text parts")
	}
	out := make([]jobject, 0, len(parts))
	for i, part := range parts {
		pp := jpos(path, i)
		o, err := jobj(part, pp)
		if err != nil {
			return nil, err
		}
		if o["type"] != "text" {
			return nil, cfail(pp+".type", "only text parts are allowed here")
		}
		t, err := c.textPart(o, pp)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// textPart maps a chat text part to input_text.
func (c *chatResponses) textPart(o jobject, path string) (jobject, error) {
	c.keep(o, path, "type", "text", "prompt_cache_breakpoint")
	s, err := jstr(o["text"], path+".text")
	if err != nil {
		return nil, err
	}
	t := jobject{"type": "input_text", "text": s}
	if b, ok := o["prompt_cache_breakpoint"]; ok {
		t["prompt_cache_breakpoint"] = b
	}
	return t, nil
}

// chatImageDetail are the image_url.detail values Responses accepts.
var chatImageDetail = map[string]bool{"auto": true, "low": true, "high": true, "original": true}

// chatUserContent maps user content: text, images and files.
func (c *chatResponses) chatUserContent(v any, path string) ([]any, error) {
	if s, ok := v.(string); ok {
		return []any{jobject{"type": "input_text", "text": s}}, nil
	}
	parts, err := jlist(v, path)
	if err != nil {
		return nil, cfail(path, "expected string or content parts")
	}
	out := make([]any, 0, len(parts))
	for i, part := range parts {
		pp := jpos(path, i)
		o, err := jobj(part, pp)
		if err != nil {
			return nil, err
		}
		var item jobject
		switch o["type"] {
		case "text":
			if item, err = c.textPart(o, pp); err != nil {
				return nil, err
			}
		case "image_url":
			c.keep(o, pp, "type", "image_url", "prompt_cache_breakpoint")
			img, err := jobj(o["image_url"], pp+".image_url")
			if err != nil {
				return nil, err
			}
			c.keep(img, pp+".image_url", "url", "detail")
			u, err := jnonempty(img["url"], pp+".image_url.url")
			if err != nil {
				return nil, err
			}
			// Chat's default detail is auto; Responses requires it. A
			// value Responses does not know is read as auto.
			detail := "auto"
			if d, ok := img["detail"]; ok {
				if s, _ := d.(string); chatImageDetail[s] {
					detail = s
				} else {
					c.drop(pp + ".image_url.detail")
				}
			}
			item = jobject{"type": "input_image", "image_url": u, "detail": detail}
		case "file":
			c.keep(o, pp, "type", "file", "prompt_cache_breakpoint")
			f, err := jobj(o["file"], pp+".file")
			if err != nil {
				return nil, err
			}
			c.keep(f, pp+".file", "file_id", "file_data", "filename")
			item = jobject{"type": "input_file"}
			for _, k := range []string{"file_id", "file_data", "filename"} {
				if err = optStr(item, f, k, pp+".file"); err != nil {
					return nil, err
				}
			}
			if item["file_id"] == nil && item["file_data"] == nil {
				return nil, cfail(pp+".file", "file_id or file_data is required")
			}
		case "input_audio":
			return nil, cfail(pp+".type", "Responses input has no audio parts")
		default:
			return nil, cfail(pp+".type", "content part type is not converted")
		}
		if b, ok := o["prompt_cache_breakpoint"]; ok {
			item["prompt_cache_breakpoint"] = b
		}
		out = append(out, item)
	}
	return out, nil
}

// chatAssistant maps an assistant message: its text and refusal, then its
// tool calls (function, custom, legacy function_call). An assistant message
// with nothing in it is left out.
func (c *chatResponses) chatAssistant(msg jobject, path string, index int, conv *chatConversation) ([]any, error) {
	if _, ok := msg["audio"]; ok && msg["audio"] != nil {
		return nil, cfail(path+".audio", "earlier audio responses have no Responses equivalent")
	}
	c.keep(msg, path, "role", "content", "tool_calls", "refusal", "function_call")
	content, err := c.assistantContent(msg, path)
	if err != nil {
		return nil, err
	}
	var items []any
	if len(content) > 0 {
		items = append(items, jobject{"type": "message", "role": "assistant", "content": content})
	}
	if tc, ok := msg["tool_calls"]; ok {
		calls, err := c.assistantToolCalls(tc, path+".tool_calls", conv)
		if err != nil {
			return nil, err
		}
		items = append(items, calls...)
	}
	if fc, ok := msg["function_call"]; ok {
		call, err := c.assistantFunctionCall(fc, path+".function_call", index, conv)
		if err != nil {
			return nil, err
		}
		items = append(items, call)
	}
	if len(items) == 0 {
		c.drop(path)
	}
	return items, nil
}

// assistantContent maps the content and refusal of an assistant message to
// output_text and refusal parts; an empty refusal is dropped.
func (c *chatResponses) assistantContent(msg jobject, path string) ([]any, error) {
	var content []any
	if cv, ok := msg["content"]; ok {
		parts, err := c.assistantParts(cv, path+".content")
		if err != nil {
			return nil, err
		}
		content = append(content, parts...)
	}
	if r, ok := msg["refusal"]; ok {
		s, err := jstr(r, path+".refusal")
		if err != nil {
			return nil, err
		}
		if s != "" {
			content = append(content, jobject{"type": "refusal", "refusal": s})
		} else {
			c.drop(path + ".refusal")
		}
	}
	return content, nil
}

// assistantToolCalls maps an assistant message's tool_calls (at path) to
// function_call and custom_tool_call items, remembering the kind of each
// call for the tool messages answering it.
func (c *chatResponses) assistantToolCalls(v any, path string, conv *chatConversation) ([]any, error) {
	calls, err := jlist(v, path)
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(calls))
	for i, tcv := range calls {
		cp := jpos(path, i)
		call, err := jobj(tcv, cp)
		if err != nil {
			return nil, err
		}
		typ, _ := call["type"].(string)
		if typ != "function" && typ != "custom" {
			return nil, cfail(cp+".type", "only function and custom calls are converted")
		}
		c.keep(call, cp, "id", "type", typ)
		id, err := jnonempty(call["id"], cp+".id")
		if err != nil {
			return nil, err
		}
		inner, err := jobj(call[typ], cp+"."+typ)
		if err != nil {
			return nil, err
		}
		field := "arguments"
		if typ == "custom" {
			field = "input"
		}
		c.keep(inner, cp+"."+typ, "name", field)
		name, err := jnonempty(inner["name"], cp+"."+typ+".name")
		if err != nil {
			return nil, err
		}
		arg, err := jstr(inner[field], cp+"."+typ+"."+field)
		if err != nil {
			return nil, err
		}
		conv.callKinds[id] = typ
		if typ == "custom" {
			items = append(items, jobject{"type": "custom_tool_call", "call_id": id, "name": name, "input": arg})
		} else {
			items = append(items, jobject{"type": "function_call", "call_id": id, "name": name, "arguments": arg})
		}
	}
	return items, nil
}

// assistantFunctionCall maps the legacy function_call (at path) of the
// assistant message at index to a function_call item.
func (c *chatResponses) assistantFunctionCall(v any, path string, index int, conv *chatConversation) (jobject, error) {
	fn, err := jobj(v, path)
	if err != nil {
		return nil, err
	}
	c.keep(fn, path, "name", "arguments")
	name, err := jnonempty(fn["name"], path+".name")
	if err != nil {
		return nil, err
	}
	args, err := jstr(fn["arguments"], path+".arguments")
	if err != nil {
		return nil, err
	}
	// Legacy calls have no id; the function message answering it is
	// matched by name.
	id := fmt.Sprintf("call_legacy_%d", index)
	conv.legacy = append(conv.legacy, legacyCall{name: name, id: id})
	conv.callKinds[id] = "function"
	return jobject{"type": "function_call", "call_id": id, "name": name, "arguments": args}, nil
}

// assistantParts maps assistant content (a string, or text and refusal
// parts) to output_text and refusal parts; empty text is left out.
func (c *chatResponses) assistantParts(v any, path string) ([]any, error) {
	if s, ok := v.(string); ok {
		if s == "" {
			return nil, nil
		}
		return []any{jobject{"type": "output_text", "text": s, "annotations": []any{}}}, nil
	}
	parts, err := jlist(v, path)
	if err != nil {
		return nil, cfail(path, "expected string or text parts")
	}
	var out []any
	for i, part := range parts {
		pp := jpos(path, i)
		o, err := jobj(part, pp)
		if err != nil {
			return nil, err
		}
		switch o["type"] {
		case "text":
			c.keep(o, pp, "type", "text")
			s, err := jstr(o["text"], pp+".text")
			if err != nil {
				return nil, err
			}
			if s != "" {
				out = append(out, jobject{"type": "output_text", "text": s, "annotations": []any{}})
			}
		case "refusal":
			c.keep(o, pp, "type", "refusal")
			s, err := jstr(o["refusal"], pp+".refusal")
			if err != nil {
				return nil, err
			}
			if s != "" {
				out = append(out, jobject{"type": "refusal", "refusal": s})
			}
		default:
			return nil, cfail(pp+".type", "only text and refusal parts are allowed here")
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- response

// chatUnrepresentable are Responses output items a chat completion cannot
// carry: calls the client would have to act on with a tool Chat Completions
// does not have, and generated images. Every other item that is not
// converted (reasoning, web_search_call and other server-side tool work,
// item types added later) is left out of the chat response.
var chatUnrepresentable = map[string]bool{
	"computer_call": true, "local_shell_call": true, "shell_call": true, "apply_patch_call": true,
	"mcp_approval_request": true, "image_generation_call": true,
}

func (c *chatResponses) Response(body []byte) ([]byte, error) {
	if !c.prepared {
		return nil, errUnprepared
	}
	c.resetResponseLosses()
	r, err := decodeJObject(body, "response")
	if err != nil {
		return nil, err
	}
	msg, finish, err := c.chatMessageFromResponse(r)
	if err != nil {
		return nil, err
	}
	id, _ := r["id"].(string)
	model, _ := r["model"].(string)
	out := jobject{"id": id, "object": "chat.completion", "created": responseCreated(r), "model": model,
		"choices": []any{jobject{"index": 0, "message": msg, "finish_reason": finish, "logprobs": nil}}}
	if u, ok := r["usage"]; ok && u != nil {
		usage, err := chatUsage(u)
		if err != nil {
			return nil, err
		}
		out["usage"] = usage
	}
	if st, ok := r["service_tier"].(string); ok {
		out["service_tier"] = st
	}
	return json.Marshal(out)
}

func responseCreated(r jobject) int64 {
	if n, ok := r["created_at"].(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return time.Now().Unix()
}

// chatMessageFromResponse builds the chat message and finish_reason of a
// completed or incomplete response.
func (c *chatResponses) chatMessageFromResponse(r jobject) (jobject, string, error) {
	output := []any{}
	if v, ok := r["output"]; ok && v != nil {
		var err error
		if output, err = jlist(v, "response.output"); err != nil {
			return nil, "", err
		}
	}
	var content chatContent
	var reasoning []string
	toolCalls := []any{}
	var legacyCall jobject
	for i, it := range output {
		path := jpos("response.output", i)
		item, err := jobj(it, path)
		if err != nil {
			return nil, "", err
		}
		typ, _ := item["type"].(string)
		switch typ {
		case "message":
			if err := content.addMessage(item, path); err != nil {
				return nil, "", err
			}
		case "reasoning":
			reasoning = append(reasoning, c.reasoningText(item, path)...)
		case "function_call", "custom_tool_call":
			call, err := chatToolCall(item, path)
			if err != nil {
				return nil, "", err
			}
			if c.legacy {
				if typ != "function_call" || legacyCall != nil {
					return nil, "", cfail(path, "legacy function calling returns one function call")
				}
				legacyCall = call["function"].(jobject)
				continue
			}
			toolCalls = append(toolCalls, call)
		default:
			if chatUnrepresentable[typ] {
				return nil, "", cfail(path+".type", "output item has no chat equivalent")
			}
			// Server-side tool calls whose results are in the text, and
			// unknown items, are left out.
			c.lost.add(responsesItemLoss(typ), path)
		}
	}
	finish, err := chatFinish(r, len(toolCalls) > 0 || legacyCall != nil)
	if err != nil {
		return nil, "", err
	}
	msg := content.message()
	if len(reasoning) > 0 {
		msg["reasoning_content"] = strings.Join(reasoning, reasoningSeparator)
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	if legacyCall != nil {
		msg["function_call"] = legacyCall
		if finish == "tool_calls" {
			finish = "function_call"
		}
	}
	return msg, finish, nil
}

// reasoningSeparator separates the reasoning parts (summary parts, items)
// in reasoning_content.
const reasoningSeparator = "\n\n"

// reasoningText returns the non-empty summary and reasoning texts of a
// reasoning item; its encrypted reasoning cannot reach a chat client (a
// response loss).
func (c *chatResponses) reasoningText(item jobject, path string) []string {
	if s, _ := item["encrypted_content"].(string); s != "" {
		c.lost.add(LossReasoning, path+".encrypted_content")
	}
	var out []string
	for _, k := range []string{"summary", "content"} {
		parts, _ := item[k].([]any)
		for _, p := range parts {
			if o, ok := p.(map[string]any); ok {
				if t, _ := o["text"].(string); t != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// chatContent collects the text, refusal and annotations of a response's
// message items, in output order.
type chatContent struct {
	text, refusal       strings.Builder
	textLen             int // characters of text so far, for annotation offsets
	hasText, hasRefusal bool
	annotations         []any
}

// addMessage adds the output_text and refusal parts of the message item at
// path. Other part types have no chat equivalent and are left out.
func (m *chatContent) addMessage(item jobject, path string) error {
	var parts []any
	if v, ok := item["content"]; ok && v != nil {
		var err error
		if parts, err = jlist(v, path+".content"); err != nil {
			return err
		}
	}
	for j, part := range parts {
		pp := jpos(path+".content", j)
		o, err := jobj(part, pp)
		if err != nil {
			return err
		}
		switch o["type"] {
		case "output_text":
			if err := m.addText(o, pp); err != nil {
				return err
			}
		case "refusal":
			s, err := jstr(o["refusal"], pp+".refusal")
			if err != nil {
				return err
			}
			m.refusal.WriteString(s)
			m.hasRefusal = true
		}
	}
	return nil
}

// addText adds the output_text part o at path and its annotations, their
// offsets moved past the text before it.
func (m *chatContent) addText(o jobject, path string) error {
	s, err := jstr(o["text"], path+".text")
	if err != nil {
		return err
	}
	if as, ok := o["annotations"].([]any); ok {
		for k, a := range as {
			mapped, err := chatAnnotation(a, jpos(path+".annotations", k), m.textLen)
			if err != nil {
				return err
			}
			if mapped != nil {
				m.annotations = append(m.annotations, mapped)
			}
		}
	}
	m.text.WriteString(s)
	m.textLen += len([]rune(s))
	m.hasText = true
	return nil
}

// message is the chat assistant message of what was collected, without tool
// calls: content and refusal are null when absent.
func (m *chatContent) message() jobject {
	msg := jobject{"role": "assistant", "content": nil, "refusal": nil}
	if m.hasText {
		msg["content"] = m.text.String()
	}
	if m.hasRefusal {
		msg["refusal"] = m.refusal.String()
	}
	if len(m.annotations) > 0 {
		msg["annotations"] = m.annotations
	}
	return msg
}

// chatToolCall maps a function_call or custom_tool_call item to a chat
// tool call.
func chatToolCall(item jobject, path string) (jobject, error) {
	id, err := jnonempty(item["call_id"], path+".call_id")
	if err != nil {
		return nil, err
	}
	name, err := jnonempty(item["name"], path+".name")
	if err != nil {
		return nil, err
	}
	if item["type"] == "custom_tool_call" {
		input, err := jstr(item["input"], path+".input")
		if err != nil {
			return nil, err
		}
		return jobject{"id": id, "type": "custom", "custom": jobject{"name": name, "input": input}}, nil
	}
	args, err := jstr(item["arguments"], path+".arguments")
	if err != nil {
		return nil, err
	}
	return jobject{"id": id, "type": "function", "function": jobject{"name": name, "arguments": args}}, nil
}

// chatFinish maps the response status to a finish_reason.
func chatFinish(r jobject, tools bool) (string, error) {
	switch r["status"] {
	case "completed", nil:
		if tools {
			return "tool_calls", nil
		}
		return "stop", nil
	case "incomplete":
		reason := ""
		if d, ok := r["incomplete_details"].(map[string]any); ok {
			reason, _ = d["reason"].(string)
		}
		if reason == "content_filter" {
			return "content_filter", nil
		}
		// max_output_tokens, and any other reason: the output was cut
		// short, which is what length tells a chat client.
		return "length", nil
	}
	return "", cfail("response.status", "only completed or incomplete responses are converted")
}

// chatAnnotation maps a url_citation; character offsets move by base, the
// length of the text before this part. Other annotation types have no chat
// equivalent and map to nil (left out).
func chatAnnotation(v any, path string, base int) (any, error) {
	a, err := jobj(v, path)
	if err != nil {
		return nil, err
	}
	if a["type"] != "url_citation" {
		return nil, nil
	}
	c := jobject{}
	for _, k := range []string{"url", "title"} {
		if x, ok := a[k]; ok {
			c[k] = x
		}
	}
	for _, k := range []string{"start_index", "end_index"} {
		n, err := jint(a[k], path+"."+k, 0, 1<<40)
		if err != nil {
			return nil, err
		}
		c[k] = n + int64(base)
	}
	return jobject{"type": "url_citation", "url_citation": c}, nil
}

// chatUsage maps Responses usage to Chat Completions usage.
func chatUsage(v any) (jobject, error) {
	u, err := jobj(v, "response.usage")
	if err != nil {
		return nil, err
	}
	in, err := jint(u["input_tokens"], "response.usage.input_tokens", 0, 1<<53)
	if err != nil {
		return nil, err
	}
	outTok, err := jint(u["output_tokens"], "response.usage.output_tokens", 0, 1<<53)
	if err != nil {
		return nil, err
	}
	total := in + outTok
	if t, ok := u["total_tokens"]; ok && t != nil {
		if total, err = jint(t, "response.usage.total_tokens", 0, 1<<53); err != nil {
			return nil, err
		}
	}
	out := jobject{"prompt_tokens": in, "completion_tokens": outTok, "total_tokens": total}
	if d, ok := u["input_tokens_details"].(map[string]any); ok {
		if c, ok := d["cached_tokens"]; ok && c != nil {
			n, err := jint(c, "response.usage.input_tokens_details.cached_tokens", 0, 1<<53)
			if err != nil {
				return nil, err
			}
			out["prompt_tokens_details"] = jobject{"cached_tokens": n}
		}
	}
	if d, ok := u["output_tokens_details"].(map[string]any); ok {
		if c, ok := d["reasoning_tokens"]; ok && c != nil {
			n, err := jint(c, "response.usage.output_tokens_details.reasoning_tokens", 0, 1<<53)
			if err != nil {
				return nil, err
			}
			out["completion_tokens_details"] = jobject{"reasoning_tokens": n}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- stream

func (c *chatResponses) NewStream() StreamConverter {
	if c.prepared {
		c.resetResponseLosses()
	}
	return &chatResponsesStream{p: c, created: time.Now().Unix(), tools: map[int64]*chatStreamTool{},
		partBase: map[[2]int64]int{}}
}

// chatStreamTool is one streamed call: its chat tool index, kind
// ("function", "custom" or "legacy" for message.function_call) and what was
// sent of its arguments or input.
type chatStreamTool struct {
	index int
	kind  string
	args  strings.Builder
}

type chatResponsesStream struct {
	p                     *chatResponses
	id, model             string
	created               int64
	started, done, failed bool
	tools                 map[int64]*chatStreamTool
	// textLen counts the characters of content sent; partBase is where
	// each (output_index, content_index) text part started, for
	// annotation offsets.
	textLen  int
	partBase map[[2]int64]int
	// reasoningPart is the reasoning part last streamed (output index,
	// event type, summary or content index); nil before any.
	reasoningPart *[3]any
}

// chatStreamIgnored are Responses events with nothing to add to a chat
// stream: progress and "done" events whose content already went out as
// deltas.
var chatStreamIgnored = map[string]bool{
	"response.queued": true, "response.in_progress": true,
	"response.content_part.done": true,
	"response.output_text.done":  true, "response.refusal.done": true,
	"response.reasoning_summary_part.added": true, "response.reasoning_summary_part.done": true,
	"response.reasoning_summary_text.done": true, "response.reasoning_text.done": true,
}

func (s *chatResponsesStream) Event(ev Event) ([]Event, error) {
	if !s.p.prepared {
		return nil, errUnprepared
	}
	d := bytes.TrimSpace(ev.Data)
	if len(d) == 0 || string(d) == "[DONE]" {
		return nil, nil
	}
	o, err := decodeJObject(d, "stream")
	if err != nil {
		return nil, err
	}
	typ, _ := o["type"].(string)
	if typ == "" {
		typ = ev.Name
	}
	if s.done || s.failed || chatStreamIgnored[typ] {
		return nil, nil
	}
	switch typ {
	case "response.created":
		s.meta(o["response"])
		return s.start(), nil
	case "response.output_item.added":
		return s.itemAdded(o)
	case "response.output_item.done":
		return s.itemDone(o)
	case "response.content_part.added", "response.output_text.delta", "response.output_text.annotation.added",
		"response.refusal.delta":
		return s.contentEvent(typ, o)
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.function_call_arguments.done", "response.custom_tool_call_input.done":
		return s.toolEvent(typ, o)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return s.reasoningDelta(typ, o)
	case "response.completed", "response.incomplete":
		return s.complete(o["response"])
	case "response.failed", "error":
		return s.failure(typ, o), nil
	}
	// Progress events, server-side tool progress
	// (response.web_search_call.*), and events of other families the
	// upstream may add (rate-limit notices and the like), carry no chat
	// content. Content-bearing output items are checked in itemAdded.
	return nil, nil
}

// contentEvent maps the events of a message's text and refusal: deltas and
// annotations become content, refusal and annotations deltas; a new
// output_text part only marks where its text starts.
func (s *chatResponsesStream) contentEvent(typ string, o jobject) ([]Event, error) {
	switch typ {
	case "response.content_part.added":
		if part, ok := o["part"].(map[string]any); ok && part["type"] == "output_text" {
			s.markPart(o)
		}
		return nil, nil
	case "response.output_text.delta":
		delta, err := jstr(o["delta"], "stream.delta")
		if err != nil {
			return nil, err
		}
		s.markPart(o)
		s.textLen += len([]rune(delta))
		return append(s.start(), s.chunk(jobject{"content": delta}, nil)...), nil
	case "response.output_text.annotation.added":
		k := chatPartKey(o)
		base, ok := s.partBase[k]
		if !ok {
			base = s.textLen
		}
		a, err := chatAnnotation(o["annotation"], "stream.annotation", base)
		if err != nil || a == nil {
			return nil, err
		}
		return append(s.start(), s.chunk(jobject{"annotations": []any{a}}, nil)...), nil
	}
	// response.refusal.delta
	delta, err := jstr(o["delta"], "stream.delta")
	if err != nil {
		return nil, err
	}
	return append(s.start(), s.chunk(jobject{"refusal": delta}, nil)...), nil
}

// toolEvent maps the delta and done events of a function call's arguments
// or a custom tool call's input.
func (s *chatResponsesStream) toolEvent(typ string, o jobject) ([]Event, error) {
	t, err := s.tool(o)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		delta, err := jstr(o["delta"], "stream.delta")
		if err != nil {
			return nil, err
		}
		t.args.WriteString(delta)
		return s.toolDelta(t, delta), nil
	}
	field := "arguments"
	if typ == "response.custom_tool_call_input.done" {
		field = "input"
	}
	args, err := jstr(o[field], "stream."+field)
	if err != nil {
		return nil, err
	}
	return s.finishArguments(t, args)
}

// reasoningDelta streams reasoning summary or reasoning text as
// reasoning_content, parts separated as in a JSON reply.
func (s *chatResponsesStream) reasoningDelta(typ string, o jobject) ([]Event, error) {
	delta, err := jstr(o["delta"], "stream.delta")
	if err != nil || delta == "" {
		return nil, err
	}
	index := o["summary_index"]
	if typ == "response.reasoning_text.delta" {
		index = o["content_index"]
	}
	part := [3]any{o["output_index"], typ, index}
	if s.reasoningPart != nil && *s.reasoningPart != part {
		delta = reasoningSeparator + delta
	}
	s.reasoningPart = &part
	return append(s.start(), s.chunk(jobject{"reasoning_content": delta}, nil)...), nil
}

// failure ends the stream on a response.failed or error event with an error
// chunk carrying the upstream's error object.
func (s *chatResponsesStream) failure(typ string, o jobject) []Event {
	s.failed = true
	if typ == "response.failed" {
		e := jobject{"message": "upstream response failed"}
		if r, ok := o["response"].(map[string]any); ok {
			if x, ok := r["error"].(map[string]any); ok {
				e = x
			}
		}
		return s.errorChunk(e)
	}
	if x, ok := o["error"].(map[string]any); ok {
		return s.errorChunk(x)
	}
	e := jobject{}
	for _, k := range []string{"code", "message", "param"} {
		if v, ok := o[k]; ok {
			e[k] = v
		}
	}
	return s.errorChunk(e)
}

func chatPartKey(o jobject) [2]int64 {
	oi, _ := jint(o["output_index"], "", 0, 1<<31)
	ci, _ := jint(o["content_index"], "", 0, 1<<31)
	return [2]int64{oi, ci}
}

// markPart remembers where the text part of o starts.
func (s *chatResponsesStream) markPart(o jobject) {
	k := chatPartKey(o)
	if _, ok := s.partBase[k]; !ok {
		s.partBase[k] = s.textLen
	}
}

// Flush fails a stream that ended without its terminal event. After an
// upstream failure the client already has the error chunk (and the gateway
// records the failure from the upstream events), as with the IR writers.
func (s *chatResponsesStream) Flush() ([]Event, error) {
	if !s.failed && !s.done {
		return nil, cfail("stream", "upstream ended without response.completed")
	}
	return nil, nil
}

func (s *chatResponsesStream) meta(v any) {
	r, ok := v.(map[string]any)
	if !ok {
		return
	}
	if id, ok := r["id"].(string); ok && id != "" {
		s.id = id
	}
	if m, ok := r["model"].(string); ok && m != "" {
		s.model = m
	}
	if n, ok := r["created_at"].(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			s.created = i
		}
	}
}

func (s *chatResponsesStream) chunk(delta jobject, finish any) []Event {
	o := jobject{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
		"choices": []any{jobject{"index": 0, "delta": delta, "finish_reason": finish, "logprobs": nil}}}
	if s.p.includeUsage {
		o["usage"] = nil
	}
	b, _ := json.Marshal(o)
	return []Event{{Data: b}}
}

// start emits the role chunk once, before any content.
func (s *chatResponsesStream) start() []Event {
	if s.started {
		return nil
	}
	s.started = true
	return s.chunk(jobject{"role": "assistant", "content": "", "refusal": nil}, nil)
}

func (s *chatResponsesStream) itemAdded(o jobject) ([]Event, error) {
	item, err := jobj(o["item"], "stream.item")
	if err != nil {
		return nil, err
	}
	typ, _ := item["type"].(string)
	idx, err := jint(o["output_index"], "stream.output_index", 0, 1<<31)
	if err != nil {
		return nil, err
	}
	switch {
	case typ == "function_call" || typ == "custom_tool_call":
	case chatUnrepresentable[typ]:
		return nil, cfail("stream.item.type", "output item has no chat equivalent")
	case typ == "message" || typ == "reasoning":
		// Their text streams as deltas.
		return nil, nil
	default:
		// Server-side tool calls and unknown items are left out.
		s.p.lost.add(responsesItemLoss(typ), jpos("response.output", int(idx)))
		return nil, nil
	}
	if _, dup := s.tools[idx]; dup {
		return nil, cfail("stream.output_index", "tool call added twice")
	}
	return s.addTool(idx, item)
}

func (s *chatResponsesStream) addTool(idx int64, item jobject) ([]Event, error) {
	call, err := chatToolCall(item, "stream.item")
	if err != nil {
		return nil, err
	}
	t := &chatStreamTool{index: len(s.tools), kind: call["type"].(string)}
	field := "arguments"
	if t.kind == "custom" {
		field = "input"
	}
	inner := call[t.kind].(jobject)
	if s.p.legacy {
		if t.kind != "function" || len(s.tools) > 0 {
			return nil, cfail("stream.item", "legacy function calling returns one function call")
		}
		t.kind = "legacy"
	}
	s.tools[idx] = t
	t.args.WriteString(inner[field].(string))
	out := s.start()
	if t.kind == "legacy" {
		return append(out, s.chunk(jobject{"function_call": inner}, nil)...), nil
	}
	call["index"] = t.index
	return append(out, s.chunk(jobject{"tool_calls": []any{call}}, nil)...), nil
}

func (s *chatResponsesStream) itemDone(o jobject) ([]Event, error) {
	item, err := jobj(o["item"], "stream.item")
	if err != nil {
		return nil, err
	}
	idx, err := jint(o["output_index"], "stream.output_index", 0, 1<<31)
	if err != nil {
		return nil, err
	}
	field := "arguments"
	switch item["type"] {
	case "function_call":
	case "custom_tool_call":
		field = "input"
	case "reasoning":
		// Its text streamed as deltas; only the encrypted state is lost.
		if enc, _ := item["encrypted_content"].(string); enc != "" {
			s.p.lost.add(LossReasoning, jpos("response.output", int(idx))+".encrypted_content")
		}
		return nil, nil
	default:
		return nil, nil
	}
	t, ok := s.tools[idx]
	if !ok {
		// An upstream that only sends the finished item.
		return s.addTool(idx, item)
	}
	args, err := jstr(item[field], "stream.item."+field)
	if err != nil {
		return nil, err
	}
	return s.finishArguments(t, args)
}

func (s *chatResponsesStream) tool(o jobject) (*chatStreamTool, error) {
	idx, err := jint(o["output_index"], "stream.output_index", 0, 1<<31)
	if err != nil {
		return nil, err
	}
	t, ok := s.tools[idx]
	if !ok {
		return nil, cfail("stream.output_index", "no tool call at this output index")
	}
	return t, nil
}

func (s *chatResponsesStream) toolDelta(t *chatStreamTool, delta string) []Event {
	switch t.kind {
	case "legacy":
		return s.chunk(jobject{"function_call": jobject{"arguments": delta}}, nil)
	case "custom":
		return s.chunk(jobject{"tool_calls": []any{jobject{"index": t.index, "custom": jobject{"input": delta}}}}, nil)
	}
	return s.chunk(jobject{"tool_calls": []any{jobject{"index": t.index, "function": jobject{"arguments": delta}}}}, nil)
}

// finishArguments sends what the final arguments (or input) add to the
// streamed ones.
func (s *chatResponsesStream) finishArguments(t *chatStreamTool, args string) ([]Event, error) {
	sent := t.args.String()
	if args == sent {
		return nil, nil
	}
	if !strings.HasPrefix(args, sent) {
		return nil, cfail("stream.arguments", "final arguments disagree with the streamed ones")
	}
	rest := args[len(sent):]
	t.args.WriteString(rest)
	return s.toolDelta(t, rest), nil
}

func (s *chatResponsesStream) complete(v any) ([]Event, error) {
	r, err := jobj(v, "stream.response")
	if err != nil {
		return nil, err
	}
	s.meta(r)
	finish, err := chatFinish(r, len(s.tools) > 0)
	if err != nil {
		return nil, err
	}
	if s.p.legacy && finish == "tool_calls" {
		finish = "function_call"
	}
	out := s.start()
	out = append(out, s.chunk(jobject{}, finish)...)
	if s.p.includeUsage {
		if u, ok := r["usage"]; ok && u != nil {
			usage, err := chatUsage(u)
			if err != nil {
				return nil, err
			}
			b, _ := json.Marshal(jobject{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
				"choices": []any{}, "usage": usage})
			out = append(out, Event{Data: b})
		}
	}
	s.done = true
	return append(out, Event{Data: []byte("[DONE]")}), nil
}

func (s *chatResponsesStream) errorChunk(e map[string]any) []Event {
	b, _ := json.Marshal(jobject{"error": e})
	return []Event{{Data: b}}
}
