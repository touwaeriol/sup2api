package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Responses API wire shapes read by the responses reader; the writer
// builds items as objects of its own (write_responses.go).

type responsesObjectJSON struct {
	ID                string             `json:"id"`
	CreatedAt         int64              `json:"created_at"`
	Status            string             `json:"status"`
	Model             string             `json:"model"`
	Error             *upstreamErrorJSON `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []json.RawMessage   `json:"output"`
	Usage  *responsesUsageJSON `json:"usage"`
}

type responsesItemJSON struct {
	Type             string              `json:"type"`
	CallID           string              `json:"call_id"`
	Name             string              `json:"name"`
	Arguments        string              `json:"arguments"`
	Input            string              `json:"input"`
	EncryptedContent string              `json:"encrypted_content"`
	Content          []responsesPartJSON `json:"content"`
	Summary          []responsesPartJSON `json:"summary"`
}

type responsesPartJSON struct {
	Type        string                    `json:"type"`
	Text        string                    `json:"text"`
	Refusal     string                    `json:"refusal"`
	Annotations []responsesAnnotationJSON `json:"annotations"`
}

type responsesAnnotationJSON struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
}

type responsesUsageJSON struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int64 `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int64 `json:"total_tokens"`
}

func (u *responsesUsageJSON) ir() Usage {
	return Usage{Input: u.InputTokens, CacheRead: u.InputTokensDetails.CachedTokens,
		Output: u.OutputTokens, Reasoning: u.OutputTokensDetails.ReasoningTokens}
}

// responsesEvent is any Responses stream event; only the members of its
// type are set.
type responsesEvent struct {
	Type         string                   `json:"type"`
	Response     *responsesObjectJSON     `json:"response"`
	OutputIndex  int                      `json:"output_index"`
	ContentIndex int                      `json:"content_index"`
	SummaryIndex int                      `json:"summary_index"`
	Item         json.RawMessage          `json:"item"`
	Part         *responsesPartJSON       `json:"part"`
	Delta        json.RawMessage          `json:"delta"`
	Arguments    *string                  `json:"arguments"`
	Input        *string                  `json:"input"`
	Annotation   *responsesAnnotationJSON `json:"annotation"`
	upstreamErrorJSON
}

// responsesReader reads Responses replies (CONVERT-MATRIX §9.7). Blocks are
// keyed "o<output_index>" for items and "o<i>c<content_index>" for the
// parts of a message.
type responsesReader struct {
	readerBase
	sent    map[string]*strings.Builder // tool arguments streamed per block
	summary map[string]int              // last summary index per reasoning block
}

func newResponsesReader(respOpts) Reader {
	return &responsesReader{sent: map[string]*strings.Builder{}, summary: map[string]int{}}
}

func respItemKey(i int) string    { return fmt.Sprintf("o%d", i) }
func respPartKey(i, j int) string { return fmt.Sprintf("o%dc%d", i, j) }

func (r *responsesReader) JSON(body []byte) (*Msg, error) {
	var o responsesObjectJSON
	if err := decodeUpstream(body, &o, "responses reply"); err != nil {
		return nil, err
	}
	if o.Status == "failed" || o.Error != nil {
		e := upstreamErrorJSON{Message: "upstream response failed"}
		if o.Error != nil {
			e = *o.Error
		}
		ue := e.ir()
		return nil, &ue
	}
	blocks, err := r.blocks(o.Output)
	if err != nil {
		return nil, err
	}
	m := &Msg{ID: o.ID, Model: o.Model, Created: o.CreatedAt, Blocks: blocks, Finish: responsesFinish(&o)}
	m.Finish = finalFinish(m)
	if o.Usage != nil {
		m.Usage = o.Usage.ir()
		m.Usage.Set = true
	}
	return m, nil
}

// blocks maps output items to blocks.
func (r *responsesReader) blocks(output []json.RawMessage) ([]Block, error) {
	var out []Block
	for i, raw := range output {
		var it responsesItemJSON
		if err := decodeUpstream(raw, &it, "responses output item"); err != nil {
			return nil, err
		}
		out = append(out, r.itemBlocks(&it, raw, fmt.Sprintf("output[%d]", i))...)
	}
	return out, nil
}

// itemBlocks maps one complete output item.
func (r *responsesReader) itemBlocks(it *responsesItemJSON, raw json.RawMessage, src string) []Block {
	switch it.Type {
	case "message":
		var out []Block
		for j, p := range it.Content {
			psrc := fmt.Sprintf("%s.content[%d]", src, j)
			switch p.Type {
			case "output_text":
				out = append(out, Block{Kind: BlockText, Text: p.Text, Annotations: r.citations(p.Annotations, psrc), Src: psrc})
			case "refusal":
				out = append(out, Block{Kind: BlockRefusal, Text: p.Refusal, Src: psrc})
			default:
				r.lost.add(LossOther, "response."+psrc)
			}
		}
		return out
	case "reasoning":
		return []Block{{Kind: BlockReasoning, Text: it.reasoningText(), HasState: it.EncryptedContent != "", Src: src}}
	case "function_call", "custom_tool_call":
		return []Block{it.toolBlock(src)}
	}
	return []Block{{Kind: BlockOpaque, Raw: append(json.RawMessage(nil), raw...), Src: src}}
}

func (it *responsesItemJSON) toolBlock(src string) Block {
	b := Block{Kind: BlockToolCall, ToolID: it.CallID, ToolName: it.Name, ToolKind: ToolFunction, Args: it.Arguments, Src: src}
	if it.Type == "custom_tool_call" {
		b.ToolKind, b.Args = ToolCustom, it.Input
	}
	return b
}

// reasoningText joins the summary parts, or the full reasoning text when
// the upstream returned that instead.
func (it *responsesItemJSON) reasoningText() string {
	parts := it.Summary
	if len(parts) == 0 {
		parts = it.Content
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, "\n\n")
}

func (r *responsesReader) citations(as []responsesAnnotationJSON, src string) []Citation {
	var out []Citation
	for _, a := range as {
		if c, ok := r.citation(a, src); ok {
			out = append(out, c)
		}
	}
	return out
}

func (r *responsesReader) citation(a responsesAnnotationJSON, src string) (Citation, bool) {
	if a.Type != "url_citation" {
		r.lost.add(LossOther, "response."+src+".annotations")
		return Citation{}, false
	}
	return Citation{URL: a.URL, Title: a.Title, Start: a.StartIndex, End: a.EndIndex}, true
}

func responsesFinish(o *responsesObjectJSON) Finish {
	if o.Status != "incomplete" {
		return Finish{Reason: FinishStop, Raw: o.Status}
	}
	raw := ""
	if o.IncompleteDetails != nil {
		raw = o.IncompleteDetails.Reason
	}
	if raw == "content_filter" {
		return Finish{Reason: FinishFilter, Raw: raw}
	}
	return Finish{Reason: FinishLength, Raw: raw}
}

func (r *responsesReader) End() ([]Delta, error) { return nil, nil }

func (r *responsesReader) Event(ev Event) ([]Delta, error) {
	d, ok := eventData(ev)
	if r.done || !ok {
		return nil, nil
	}
	var e responsesEvent
	if err := decodeUpstream(d, &e, "responses event"); err != nil {
		return nil, err
	}
	if e.Type == "" {
		e.Type = ev.Name
	}
	if e.Type == "error" {
		ue, nested := errorPayload(d)
		if !nested {
			ue = e.upstreamErrorJSON.ir()
		}
		r.fail(ue)
		return r.take(), nil
	}
	if err := r.event(&e); err != nil {
		return nil, err
	}
	return r.take(), nil
}

func (r *responsesReader) event(e *responsesEvent) error {
	switch e.Type {
	case "response.created", "response.in_progress", "response.queued":
		if o := e.Response; o != nil {
			r.start(o.ID, o.Model, o.CreatedAt)
		}
	case "response.output_item.added":
		return r.itemAdded(e)
	case "response.output_item.done":
		return r.itemDone(e)
	case "response.content_part.added":
		r.partAdded(e)
	case "response.content_part.done":
		if k := respPartKey(e.OutputIndex, e.ContentIndex); r.open == k {
			r.stop()
		}
	case "response.output_text.delta", "response.refusal.delta":
		r.partAdded(e)
		r.add(respPartKey(e.OutputIndex, e.ContentIndex), Block{}, DeltaText, jsonText(e.Delta))
	case "response.output_text.annotation.added":
		r.annotation(e)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		r.reasoning(e)
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		r.args(respItemKey(e.OutputIndex), jsonText(e.Delta))
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		return r.argsDone(respItemKey(e.OutputIndex), e.Arguments, e.Input)
	case "response.completed", "response.incomplete":
		return r.completed(e.Response)
	case "response.failed":
		ue := upstreamErrorJSON{Message: "upstream response failed"}
		if e.Response != nil && e.Response.Error != nil {
			ue = *e.Response.Error
		}
		r.fail(ue.ir())
	}
	// Progress events, server tool progress and event families added
	// later carry nothing the IR holds.
	return nil
}

func (r *responsesReader) item(e *responsesEvent) (*responsesItemJSON, error) {
	var it responsesItemJSON
	if err := decodeUpstream(e.Item, &it, "responses output item"); err != nil {
		return nil, err
	}
	return &it, nil
}

func (r *responsesReader) itemAdded(e *responsesEvent) error {
	it, err := r.item(e)
	if err != nil {
		return err
	}
	key, src := respItemKey(e.OutputIndex), fmt.Sprintf("output[%d]", e.OutputIndex)
	switch it.Type {
	case "reasoning":
		r.ensure(key, Block{Kind: BlockReasoning, Src: src})
	case "function_call", "custom_tool_call":
		b := it.toolBlock(src)
		args := b.Args
		b.Args = ""
		r.ensure(key, b)
		r.args(key, args)
	}
	return nil
}

func (r *responsesReader) itemDone(e *responsesEvent) error {
	it, err := r.item(e)
	if err != nil {
		return err
	}
	key, src := respItemKey(e.OutputIndex), fmt.Sprintf("output[%d]", e.OutputIndex)
	_, started := r.block(key)
	switch it.Type {
	case "message":
		if !r.messageStarted(e.OutputIndex) {
			r.emitBlocks(key, r.itemBlocks(it, e.Item, src))
		}
		r.stopItem(e.OutputIndex)
		return nil
	case "reasoning":
		if !started {
			r.emitBlocks(key, r.itemBlocks(it, e.Item, src))
			return nil
		}
		if _, streamed := r.summary[key]; !streamed && r.open == key {
			r.add(key, Block{}, DeltaReasoning, it.reasoningText())
		}
		if it.EncryptedContent != "" {
			r.state(key)
		}
	case "function_call", "custom_tool_call":
		if !started {
			r.emitBlocks(key, r.itemBlocks(it, e.Item, src))
			return nil
		}
		if err := r.argsDone(key, &it.Arguments, &it.Input); err != nil {
			return err
		}
	default:
		r.emitBlocks(key, r.itemBlocks(it, e.Item, src))
		return nil
	}
	if r.open == key {
		r.stop()
	}
	return nil
}

// messageStarted reports whether a part of message item i started.
func (r *responsesReader) messageStarted(i int) bool {
	prefix := respItemKey(i) + "c"
	for k := range r.keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// stopItem stops the open block if it is a part of message item i.
func (r *responsesReader) stopItem(i int) {
	if strings.HasPrefix(r.open, respItemKey(i)+"c") {
		r.stop()
	}
}

// emitBlocks emits complete blocks, keyed under key.
func (r *responsesReader) emitBlocks(key string, bs []Block) {
	for j, b := range bs {
		r.emitBlock(fmt.Sprintf("%s#%d", key, j), b)
	}
}

// partAdded starts the block of a message part unless it is open.
func (r *responsesReader) partAdded(e *responsesEvent) {
	key := respPartKey(e.OutputIndex, e.ContentIndex)
	if r.open == key {
		return
	}
	b := Block{Kind: BlockText, Src: fmt.Sprintf("output[%d].content[%d]", e.OutputIndex, e.ContentIndex)}
	if e.Type == "response.refusal.delta" || (e.Part != nil && e.Part.Type == "refusal") {
		b.Kind = BlockRefusal
	}
	if e.Part != nil && e.Part.Type != "output_text" && e.Part.Type != "refusal" {
		return
	}
	r.ensure(key, b)
}

func (r *responsesReader) annotation(e *responsesEvent) {
	key := respPartKey(e.OutputIndex, e.ContentIndex)
	if e.Annotation == nil || r.open != key {
		r.lost.add(LossOther, fmt.Sprintf("response.output[%d].content[%d].annotations", e.OutputIndex, e.ContentIndex))
		return
	}
	if c, ok := r.citation(*e.Annotation, fmt.Sprintf("output[%d].content[%d]", e.OutputIndex, e.ContentIndex)); ok {
		r.emit(Delta{Op: DeltaCitation, Index: r.keys[key], Cite: &c})
	}
}

// reasoning appends summary or reasoning text; summary parts after the
// first are separated by a blank line.
func (r *responsesReader) reasoning(e *responsesEvent) {
	key := respItemKey(e.OutputIndex)
	text := jsonText(e.Delta)
	if last, ok := r.summary[key]; ok && e.SummaryIndex > last {
		text = "\n\n" + text
	}
	r.summary[key] = e.SummaryIndex
	r.add(key, Block{Kind: BlockReasoning, Src: fmt.Sprintf("output[%d]", e.OutputIndex)}, DeltaReasoning, text)
}

func (r *responsesReader) args(key, delta string) {
	if delta == "" || r.open != key {
		return
	}
	sb := r.sent[key]
	if sb == nil {
		sb = &strings.Builder{}
		r.sent[key] = sb
	}
	sb.WriteString(delta)
	r.add(key, Block{}, DeltaArgs, delta)
}

// argsDone sends what the final arguments (or input) add to the streamed
// ones.
func (r *responsesReader) argsDone(key string, args, input *string) error {
	final := args
	if final == nil || *final == "" {
		final = input
	}
	if final == nil || r.open != key {
		return nil
	}
	sent := ""
	if sb := r.sent[key]; sb != nil {
		sent = sb.String()
	}
	if !strings.HasPrefix(*final, sent) {
		return errors.New("convert: final tool arguments disagree with the streamed ones")
	}
	r.args(key, (*final)[len(sent):])
	return nil
}

// completed ends the reply; a stream that only sent the final response
// gets its blocks from the output.
func (r *responsesReader) completed(o *responsesObjectJSON) error {
	if o == nil {
		o = &responsesObjectJSON{Status: "completed"}
	}
	r.start(o.ID, o.Model, o.CreatedAt)
	if r.n == 0 && len(o.Output) > 0 {
		bs, err := r.blocks(o.Output)
		if err != nil {
			return err
		}
		r.emitBlocks("final", bs)
	}
	if o.Usage != nil {
		r.usage(o.Usage.ir())
	}
	r.finish(responsesFinish(o))
	return nil
}
