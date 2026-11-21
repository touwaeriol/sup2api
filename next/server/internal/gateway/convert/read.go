package convert

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// readerBase holds what the four readers share: it numbers blocks by an
// upstream key, keeps them sequential (starting a block stops the open
// one) and collects the deltas of one upstream event.
type readerBase struct {
	out     []Delta
	started bool
	open    string         // key of the open block, "" when none
	keys    map[string]int // block index by key
	n       int            // blocks started
	done    bool           // Finish or Error emitted
	tools   bool           // a tool call block started
	lost    losses
}

// emit appends a delta.
func (r *readerBase) emit(d Delta) { r.out = append(r.out, d) }

// take returns the deltas collected since the last call.
func (r *readerBase) take() []Delta {
	out := r.out
	r.out = nil
	return out
}

// start emits DeltaStart once.
func (r *readerBase) start(id, model string, created int64) {
	if r.started {
		return
	}
	r.started = true
	r.emit(Delta{Op: DeltaStart, Start: &Msg{ID: id, Model: model, Created: created}})
}

// ensure returns the block of key, starting it as b (after stopping the
// open block) unless it is the open one.
func (r *readerBase) ensure(key string, b Block) int {
	if r.open == key {
		return r.keys[key]
	}
	r.start("", "", 0)
	r.stop()
	if r.keys == nil {
		r.keys = map[string]int{}
	}
	i := r.n
	r.n++
	r.keys[key] = i
	r.open = key
	r.tools = r.tools || b.Kind == BlockToolCall
	r.emit(Delta{Op: DeltaBlockStart, Index: i, Block: &b})
	return i
}

// block returns the index of a started block.
func (r *readerBase) block(key string) (int, bool) {
	i, ok := r.keys[key]
	return i, ok
}

// stop stops the open block.
func (r *readerBase) stop() {
	if r.open == "" {
		return
	}
	r.emit(Delta{Op: DeltaBlockStop, Index: r.keys[r.open]})
	r.open = ""
}

// add appends a content delta to the open block key, starting it as b.
func (r *readerBase) add(key string, b Block, op DeltaOp, text string) {
	if text == "" {
		r.ensure(key, b)
		return
	}
	r.emit(Delta{Op: op, Index: r.ensure(key, b), Text: text})
}

// emitBlock emits a block that arrives complete under a key of its own:
// start, content, citations, reasoning state, stop.
func (r *readerBase) emitBlock(key string, b Block) {
	text, args, cites, state := b.Text, b.Args, b.Annotations, b.HasState
	b.Text, b.Args, b.Annotations, b.HasState = "", "", nil, false
	i := r.ensure(key, b)
	op := DeltaText
	if b.Kind == BlockReasoning {
		op = DeltaReasoning
	}
	if text != "" {
		r.emit(Delta{Op: op, Index: i, Text: text})
	}
	if args != "" {
		r.emit(Delta{Op: DeltaArgs, Index: i, Text: args})
	}
	for _, c := range cites {
		r.emit(Delta{Op: DeltaCitation, Index: i, Cite: &c})
	}
	if state {
		r.emit(Delta{Op: DeltaReasoningState, Index: i})
	}
	r.stop()
}

// state marks a started block as carrying reasoning state.
func (r *readerBase) state(key string) {
	if i, ok := r.keys[key]; ok {
		r.emit(Delta{Op: DeltaReasoningState, Index: i})
	}
}

func (r *readerBase) usage(u Usage) {
	u.Set = true
	r.emit(Delta{Op: DeltaUsage, Usage: &u})
}

// finish stops the open block and ends the reply; a natural stop after
// tool calls is a tool-use stop (CONVERT-MATRIX §9.5).
func (r *readerBase) finish(f Finish) {
	if r.done {
		return
	}
	if f.Reason == FinishStop && r.tools {
		f.Reason = FinishToolUse
	}
	r.start("", "", 0)
	r.stop()
	r.done = true
	r.emit(Delta{Op: DeltaFinish, Finish: &f})
}

// fail ends the reply with an upstream error.
func (r *readerBase) fail(e UpstreamError) {
	if r.done {
		return
	}
	r.done = true
	r.emit(Delta{Op: DeltaError, Err: &e})
}

// Losses implements LossReporter.
func (r *readerBase) Losses() []Loss { return r.lost.sorted() }

// msgBuilder accumulates a stream of deltas into a Msg and checks the delta
// contract (Delta).
type msgBuilder struct {
	m       Msg
	started bool
	open    int // index of the open block, -1 when none
	ended   bool
	err     *UpstreamError
}

func newMsgBuilder() msgBuilder { return msgBuilder{open: -1} }

// apply adds d to the message.
func (b *msgBuilder) apply(d Delta) error {
	switch d.Op {
	case DeltaStart:
		if d.Start != nil && !b.started {
			b.m.ID, b.m.Model, b.m.Created = d.Start.ID, d.Start.Model, d.Start.Created
		}
		b.started = true
		return nil
	case DeltaBlockStart:
		if d.Block == nil || d.Index != len(b.m.Blocks) {
			return fmt.Errorf("convert: block %d started out of order", d.Index)
		}
		b.m.Blocks = append(b.m.Blocks, *d.Block)
		b.open = d.Index
		return nil
	case DeltaBlockStop:
		if d.Index == b.open {
			b.open = -1
		}
		return nil
	case DeltaUsage:
		if d.Usage != nil {
			b.m.Usage = *d.Usage
		}
		return nil
	case DeltaFinish:
		b.open = -1
		b.ended = true
		if d.Finish != nil {
			b.m.Finish = *d.Finish
		}
		return nil
	case DeltaError:
		b.ended = true
		b.err = d.Err
		if b.err == nil {
			b.err = &UpstreamError{Message: "upstream error"}
		}
		return nil
	case DeltaReasoningState:
		if d.Index < 0 || d.Index >= len(b.m.Blocks) {
			return fmt.Errorf("convert: reasoning state for unknown block %d", d.Index)
		}
		b.m.Blocks[d.Index].HasState = true
		return nil
	}
	if d.Index != b.open || b.open < 0 {
		return fmt.Errorf("convert: delta for block %d which is not open", d.Index)
	}
	blk := &b.m.Blocks[d.Index]
	switch d.Op {
	case DeltaText, DeltaReasoning:
		blk.Text += d.Text
	case DeltaArgs:
		blk.Args += d.Text
	case DeltaCitation:
		if d.Cite != nil {
			blk.Annotations = append(blk.Annotations, *d.Cite)
		}
	default:
		return fmt.Errorf("convert: unknown delta op %d", d.Op)
	}
	return nil
}

// collect builds the Msg of a complete delta sequence: an upstream error
// is returned as the error, a sequence without DeltaFinish fails.
func collect(ds []Delta) (*Msg, error) {
	b := newMsgBuilder()
	for _, d := range ds {
		if err := b.apply(d); err != nil {
			return nil, err
		}
	}
	if b.err != nil {
		return nil, b.err
	}
	if !b.ended {
		return nil, errors.New("convert: upstream reply has no end")
	}
	return &b.m, nil
}

// synthCallID returns a stable call id for an upstream tool call without
// one: "call_" and 16 hex digits of a hash of the reply id and position.
func synthCallID(seed string, i int) string {
	h := sha1.Sum([]byte(seed + "#" + strconv.Itoa(i)))
	return "call_" + hex.EncodeToString(h[:8])
}

// eventData returns the trimmed data of an SSE event and whether it carries
// a JSON payload ("" and "[DONE]" do not).
func eventData(ev Event) ([]byte, bool) {
	d := bytes.TrimSpace(ev.Data)
	return d, len(d) > 0 && string(d) != "[DONE]"
}

// decode unmarshals an upstream payload, naming where it came from.
func decodeUpstream(data []byte, v any, what string) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("convert: invalid %s: %w", what, err)
	}
	return nil
}

// upstreamError maps the error objects the four protocols use
// ({message, type, code}, Gemini's {code, message, status}).
type upstreamErrorJSON struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Status  string          `json:"status"`
	Code    json.RawMessage `json:"code"`
}

func (e upstreamErrorJSON) ir() UpstreamError {
	out := UpstreamError{Type: e.Type, Message: e.Message}
	if out.Type == "" {
		out.Type = e.Status
	}
	code := string(bytes.Trim(e.Code, `"`))
	if n, err := strconv.Atoi(code); err == nil {
		out.Status = n
	} else if out.Type == "" {
		out.Type = code
	}
	if out.Message == "" {
		out.Message = "upstream error"
	}
	return out
}

// errorPayload returns the {"error": ...} object of an event, if any.
func errorPayload(data []byte) (UpstreamError, bool) {
	var w struct {
		Error *upstreamErrorJSON `json:"error"`
	}
	if json.Unmarshal(data, &w) != nil || w.Error == nil {
		return UpstreamError{}, false
	}
	return w.Error.ir(), true
}

// jsonText returns a JSON string value, or the concatenated text of an
// array of {"type": "text", "text": ...} parts; "" otherwise.
func jsonText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	for _, p := range parts {
		s += p.Text
	}
	return s
}

// compactJSON returns raw without insignificant spaces, "{}" when empty.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if len(bytes.TrimSpace(raw)) == 0 || json.Compact(&buf, raw) != nil {
		return "{}"
	}
	return buf.String()
}
