package convert

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"slices"
	"strings"
)

// The request side of the conversions to Gemini generateContent
// (CONVERT-MATRIX §9, §9.9): OpenAI chat and responses clients through
// canonReq. gemini.generate and gemini.stream_generate take the same body;
// the endpoint decides streaming and the body carries no model.

// thoughtSignatureBypass is the thoughtSignature Google documents for
// model turns Gemini did not produce; Gemini 3 rejects a function call
// without a signature in the current turn (CONVERT-MATRIX §3.2).
const thoughtSignatureBypass = "context_engineering_is_the_way_to_go"

// geminiStopLimit is the most stop sequences Gemini accepts.
const geminiStopLimit = 5

// geminiMediaTypes are the inline data types Gemini accepts besides audio/*
// and video/* (CONVERT-MATRIX §9.2).
var geminiMediaTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/heic": true,
	"image/heif": true, "application/pdf": true, "text/plain": true}

func geminiMediaSupported(mt string) bool {
	return geminiMediaTypes[mt] || strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "video/")
}

// geminiParams are the scalar params Gemini takes: canon name, Gemini name.
var geminiParams = [][2]string{{"temperature", "temperature"}, {"top_p", "topP"}, {"top_k", "topK"}, {"seed", "seed"},
	{"presence_penalty", "presencePenalty"}, {"frequency_penalty", "frequencyPenalty"}}

// geminiContent is one turn of contents.
type geminiContent struct {
	Role  string    `json:"role"`
	Parts []jobject `json:"parts"`
}

// geminiBuilder writes a canonReq as a Gemini generateContent request.
type geminiBuilder struct {
	c        *canonReq
	opt      Options
	l        *losses
	contents []geminiContent
	// callNames are the function names of the calls written, by id: a
	// functionResponse names its function.
	callNames map[string]string
}

func buildGeminiRequest(c *canonReq, opt Options, l *losses) (any, error) {
	b := &geminiBuilder{c: c, opt: opt, l: l, callNames: map[string]string{}}
	if err := b.conversation(); err != nil {
		return nil, err
	}
	if len(b.contents) == 0 {
		return nil, cfail(c.msgs[0].path, "a conversation of system messages only cannot be converted")
	}
	out := jobject{"contents": b.contents}
	if sys := b.system(); sys != nil {
		out["systemInstruction"] = sys
	}
	b.tools(out)
	if gc := b.generationConfig(); len(gc) > 0 {
		out["generationConfig"] = gc
	}
	return out, nil
}

// system collects the system prompt and every system or developer message
// into systemInstruction; one after the conversation started moves there
// (an "other" loss, the position changes).
func (b *geminiBuilder) system() jobject {
	var parts []jobject
	add := func(ps []canonPart) {
		for _, p := range ps {
			b.dropCache(p)
			switch {
			case p.kind != partText && p.kind != partRefusal:
				b.l.add(partLossCategory(p), p.path)
			case p.text != "":
				parts = append(parts, jobject{"text": p.text})
			}
		}
	}
	add(b.c.system)
	talked := false
	for _, m := range b.c.msgs {
		switch {
		case !m.isSystem():
			talked = true
			continue
		case talked:
			b.l.add(LossOther, m.path)
		}
		add(m.parts)
	}
	if len(parts) == 0 {
		return nil
	}
	return jobject{"parts": parts}
}

// dropCache records a cache mark as lost: Gemini caches through
// cachedContent only (CONVERT-MATRIX D7).
func (b *geminiBuilder) dropCache(p canonPart) {
	if p.cache != nil {
		b.l.add(LossOther, p.cache.path)
	}
}

// conversation writes the messages as contents: assistant turns as role
// model, tool results as functionResponse parts of a user turn;
// consecutive messages of one role share a turn. Every model turn is
// signed with the bypass thoughtSignature.
func (b *geminiBuilder) conversation() error {
	for _, m := range b.c.msgs {
		if m.isSystem() {
			continue
		}
		var parts []jobject
		for _, p := range m.parts {
			ps, err := b.part(p, m.role)
			if err != nil {
				return err
			}
			parts = append(parts, ps...)
		}
		role := roleUser
		if m.role == roleAssistant {
			role = "model"
		}
		b.appendContent(role, parts)
	}
	for _, c := range b.contents {
		if c.Role == "model" {
			signTurn(c.Parts)
		}
	}
	return nil
}

func (b *geminiBuilder) appendContent(role string, parts []jobject) {
	if len(parts) == 0 {
		return
	}
	if n := len(b.contents); n > 0 && b.contents[n-1].Role == role {
		b.contents[n-1].Parts = append(b.contents[n-1].Parts, parts...)
		return
	}
	b.contents = append(b.contents, geminiContent{Role: role, Parts: parts})
}

// signTurn writes the bypass thoughtSignature on the first function call
// of a model turn, else on its first text part.
func signTurn(parts []jobject) {
	has := func(key string) func(jobject) bool {
		return func(p jobject) bool { _, ok := p[key]; return ok }
	}
	i := slices.IndexFunc(parts, has("functionCall"))
	if i < 0 {
		i = slices.IndexFunc(parts, has("text"))
	}
	if i >= 0 {
		parts[i]["thoughtSignature"] = thoughtSignatureBypass
	}
}

// part writes one message part of a message of role.
func (b *geminiBuilder) part(p canonPart, role string) ([]jobject, error) {
	b.dropCache(p)
	switch p.kind {
	case partText:
		if role == roleUser {
			return userTextParts(p.text), nil
		}
		return textParts(p.text), nil
	case partRefusal:
		return textParts(p.text), nil
	case partImage, partFile:
		o, err := b.media(p)
		return someParts(o), err
	case partToolCall:
		return []jobject{b.functionCall(p)}, nil
	case partToolResult:
		o, err := b.functionResponse(p)
		return someParts(o), err
	}
	return nil, nil
}

// textParts is a text part, none for empty text.
func textParts(text string) []jobject {
	if text == "" {
		return nil
	}
	return []jobject{{"text": text}}
}

func someParts(o jobject) []jobject {
	if o == nil {
		return nil
	}
	return []jobject{o}
}

func inlineDataPart(mt, data string) jobject {
	return jobject{"inlineData": jobject{"mimeType": mt, "data": data}}
}

// markdownDataImage matches a markdown image whose target is a base64 data
// URL.
var markdownDataImage = regexp.MustCompile(`!\[[^\]]*\]\((data:[^;,()\s]+;base64,[A-Za-z0-9+/=]+)\)`)

// userTextParts splits the markdown data URL images out of user text as
// inlineData parts (CONVERT-MATRIX §9.2); an image of a type Gemini does not
// accept stays text.
func userTextParts(text string) []jobject {
	var out []jobject
	last := 0
	for _, m := range markdownDataImage.FindAllStringSubmatchIndex(text, -1) {
		mt, data, _ := parseDataURL(text[m[2]:m[3]])
		if mt = mediaType(mt); !geminiMediaSupported(mt) {
			continue
		}
		out = append(out, textParts(text[last:m[0]])...)
		out = append(out, inlineDataPart(mt, data))
		last = m[1]
	}
	return append(out, textParts(text[last:])...)
}

// mediaType returns a MIME type without parameters, in lower case.
func mediaType(mt string) string {
	if t, _, err := mime.ParseMediaType(mt); err == nil {
		return t
	}
	return strings.ToLower(strings.TrimSpace(mt))
}

// media writes an image or a file as inline data, nil when it is left
// out: a file id names an OpenAI file Gemini does not know. A filename is
// a label Gemini has no field for, not content; it is not kept.
func (b *geminiBuilder) media(p canonPart) (jobject, error) {
	if p.detail != nil {
		b.l.add(LossImage, p.detail.path)
	}
	src := p.url
	if p.data != "" {
		src = p.data
	}
	if !isHTTPURL(src) && !strings.HasPrefix(src, "data:") {
		b.l.add(partLossCategory(p), p.path)
		return nil, nil
	}
	mt, data, err := b.inline(src)
	if err != nil {
		return nil, cfail(p.path, err.Error())
	}
	return inlineDataPart(mt, data), nil
}

// inline returns the media type and base64 data of a data URL, or of an
// http(s) URL downloaded through Options.Fetch (CONVERT-MATRIX D6). Media
// that cannot be fetched or that Gemini does not accept cannot be
// converted.
func (b *geminiBuilder) inline(url string) (string, string, error) {
	mt, data, ok := parseDataURL(url)
	switch {
	case ok:
	case !isHTTPURL(url):
		return "", "", errors.New("invalid data URL")
	case b.opt.Fetch == nil:
		return "", "", fmt.Errorf("media %s cannot be downloaded", url)
	default:
		var err error
		if mt, data, err = b.opt.Fetch(b.ctx(), url); err != nil {
			return "", "", fmt.Errorf("downloading media %s: %v", url, err)
		}
	}
	if mt = mediaType(mt); !geminiMediaSupported(mt) {
		return "", "", fmt.Errorf("media type %q is not supported by Gemini", mt)
	}
	return mt, data, nil
}

func (b *geminiBuilder) ctx() context.Context {
	if b.opt.Ctx != nil {
		return b.opt.Ctx
	}
	return context.Background()
}

// functionCall writes a tool call. A custom tool's input goes as the
// argument of a function taking {"input": string}; function arguments that
// are not a JSON object become {} (a tool loss).
func (b *geminiBuilder) functionCall(p canonPart) jobject {
	call := p.call
	b.callNames[call.id] = call.name
	var args any = jobject{}
	switch {
	case call.kind == ToolCustom:
		b.markCustom(call.name)
		args = jobject{"input": call.args}
	case strings.TrimSpace(call.args) == "":
	default:
		if o, err := decodeJObject([]byte(call.args), p.path); err == nil {
			args = o
		} else {
			b.l.add(LossTool, p.path)
		}
	}
	return jobject{"functionCall": jobject{"id": call.id, "name": call.name, "args": args}}
}

// markCustom notes a custom tool sent as a function, for the response side
// to unwrap its calls.
func (b *geminiBuilder) markCustom(name string) {
	if b.c.resp.customTools == nil {
		b.c.resp.customTools = map[string]bool{}
	}
	b.c.resp.customTools[name] = true
}

// functionResponse writes a tool result, named by its call; a result of no
// call in the request is a tool loss. Text that is a JSON object is the
// response as is, other text {"output": text}, an error {"error": text}.
// Images and files go inline as response parts on Gemini 3 and later.
func (b *geminiBuilder) functionResponse(p canonPart) (jobject, error) {
	r := p.result
	name, ok := b.callNames[r.id]
	if !ok {
		b.l.add(LossTool, p.path)
		return nil, nil
	}
	var text []string
	var media []jobject
	for _, part := range r.parts {
		b.dropCache(part)
		switch {
		case part.kind == partText || part.kind == partRefusal:
			text = append(text, part.text)
		case (part.kind == partImage || part.kind == partFile) && !geminiLegacy(b.c.model):
			o, err := b.media(part)
			if err != nil {
				return nil, err
			}
			media = append(media, someParts(o)...)
		default:
			b.l.add(partLossCategory(part), part.path)
		}
	}
	fr := jobject{"id": r.id, "name": name, "response": functionOutput(strings.Join(text, "\n"), r.isError)}
	if len(media) > 0 {
		fr["parts"] = media
	}
	return jobject{"functionResponse": fr}, nil
}

func functionOutput(text string, isError bool) jobject {
	if isError {
		return jobject{"error": text}
	}
	if o, err := decodeJObject([]byte(text), ""); err == nil {
		return o
	}
	return jobject{"output": text}
}

// generationConfig writes the output limit, sampling, stop sequences, the
// output format and thinking.
func (b *geminiBuilder) generationConfig() jobject {
	c := b.c
	gc := jobject{}
	if c.maxTokens > 0 {
		gc["maxOutputTokens"] = c.maxTokens
	}
	for _, n := range geminiParams {
		if p, ok := c.take(n[0]); ok {
			gc[n[1]] = p.value
		}
	}
	if p, ok := c.take("stop"); ok {
		stops := p.value.([]any)
		if len(stops) > geminiStopLimit {
			b.l.add(LossOther, p.path)
			stops = stops[:geminiStopLimit]
		}
		gc["stopSequences"] = stops
	}
	b.format(gc)
	// A client asking for reasoning or its summary sees the thoughts.
	_, summary := c.take("reasoning_summary")
	if t := geminiThinking(c.model, c.reasoning, summary || c.reasoning != nil, b.l); t != nil {
		gc["thinkingConfig"] = t
	}
	return gc
}

// format writes JSON output; Gemini decoding with a schema is always
// constrained, so a non-strict schema or a description is lost.
func (b *geminiBuilder) format(gc jobject) {
	f := b.c.format
	if f == nil || f.typ == "text" {
		return
	}
	gc["responseMimeType"] = "application/json"
	if f.typ != "json_schema" {
		return
	}
	if f.schema != nil {
		gc["responseJsonSchema"] = f.schema
	}
	if f.description != "" {
		b.l.add(LossOther, f.path+".description")
	}
	if f.strict == nil || !*f.strict {
		b.l.add(LossOther, f.path+".strict")
	}
}

// tools writes the function declarations, Google Search for a web search
// tool, and the tool choice.
func (b *geminiBuilder) tools(out jobject) {
	var decls, tools []jobject
	search := false
	for _, t := range b.c.tools {
		if t.kind != "web_search" {
			decls = append(decls, b.declaration(t))
			continue
		}
		b.webSearch(t)
		if !search {
			search = true
			tools = append(tools, jobject{"googleSearch": jobject{}})
		}
	}
	if len(decls) > 0 {
		tools = append([]jobject{{"functionDeclarations": decls}}, tools...)
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	if cfg := b.toolConfig(decls); cfg != nil {
		out["toolConfig"] = jobject{"functionCallingConfig": cfg}
	}
}

// declaration writes a function or custom tool; the schema goes as JSON
// Schema. Details other than the default strict: false are lost.
func (b *geminiBuilder) declaration(t canonTool) jobject {
	for _, d := range t.details {
		if d.name != "strict" || d.value != false {
			b.l.add(LossOther, d.path)
		}
	}
	o := jobject{"name": t.name}
	if t.description != "" {
		o["description"] = t.description
	}
	switch {
	case t.kind == ToolCustom:
		b.markCustom(t.name)
		o["parametersJsonSchema"] = customInputSchema
	case t.schema != nil:
		o["parametersJsonSchema"] = t.schema
	}
	return o
}

// webSearch records the web search options Google Search does not take.
func (b *geminiBuilder) webSearch(t canonTool) {
	for _, d := range t.details {
		b.l.add(LossOther, d.path)
	}
	if len(t.location) > 0 {
		b.l.add(LossOther, t.path+".user_location")
	}
}

// toolConfig writes the tool choice as a function calling config; nil when
// there is nothing to say. parallel_tool_calls: false has no Gemini form.
func (b *geminiBuilder) toolConfig(decls []jobject) jobject {
	if p, ok := b.c.take("parallel_tool_calls"); ok && p.value == false {
		b.l.add(LossOther, p.path)
	}
	ch := b.c.choice
	switch {
	case ch == nil:
		return nil
	case len(decls) == 0:
		// Without functions a choice other than none or auto cannot be kept.
		if ch.mode != "none" && ch.mode != "auto" {
			b.l.add(LossTool, ch.path)
		}
		return nil
	}
	declared := func(name string) bool {
		return slices.ContainsFunc(decls, func(d jobject) bool { return d["name"] == name })
	}
	switch ch.mode {
	case "none", "auto":
		return jobject{"mode": strings.ToUpper(ch.mode)}
	case "required":
		return jobject{"mode": "ANY"}
	case "tool":
		if declared(ch.name) {
			return jobject{"mode": "ANY", "allowedFunctionNames": []string{ch.name}}
		}
	case "allowed":
		mode := "AUTO"
		if ch.allowedMode == "required" {
			mode = "ANY"
		}
		return jobject{"mode": mode, "allowedFunctionNames": ch.allowed}
	}
	b.l.add(LossTool, ch.path)
	return nil
}
