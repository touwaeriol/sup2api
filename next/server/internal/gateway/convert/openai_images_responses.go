package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Image protocols an imagesResponses converter serves.
const (
	protocolImages     = "openai.images"
	protocolImagesEdit = "openai.images_edit"
)

// imagesResponses serves an openai.images or openai.images_edit client with
// an openai.responses upstream through its image_generation tool
// (CONTRACTS §121): the request becomes one user message (the prompt, then
// the images of an edit) and a forced call of the tool carrying the image
// parameters; the reply's generated images become the image API's reply.
//
// The top-level model of the Responses request is the account type's
// adapted model (Options.AdaptModel), the chat model driving the tool; the
// client's model goes into the tool (ModelPath), where the account's model
// mapping writes too.
//
// The registry entry is stateless; Prepare returns a per-request copy that
// remembers the losses.
type imagesResponses struct {
	from     string
	prepared bool
	lost     losses
	// reqLosses is how many entries of lost the request has; the rest are
	// the latest response's.
	reqLosses int
}

func newImagesResponses(from string) *imagesResponses { return &imagesResponses{from: from} }

func (c *imagesResponses) From() string { return c.from }
func (c *imagesResponses) To() string   { return relay }
func (c *imagesResponses) Grade() Grade { return GradeGood }

// ModelPath implements ModelLocator: the client's image model travels in
// the tool.
func (c *imagesResponses) ModelPath() string { return "tools.0.model" }

// ResponseModelPaths implements ResponseModelLocator: the image model the
// upstream ran is echoed in the response's tools, its top-level model is
// the chat model.
func (c *imagesResponses) ResponseModelPaths() (json, sse string) {
	return "tools.0.model", "response.tools.0.model"
}

// terminal implements terminal: the image_generation tool is the relay's
// own, so the pair is never composed with a hop to another platform.
func (c *imagesResponses) terminal() {}

// Losses implements LossReporter.
func (c *imagesResponses) Losses() []Loss { return c.lost.sorted() }

// edit reports the edits endpoint.
func (c *imagesResponses) edit() bool { return c.from == protocolImagesEdit }

// eventPrefix is the prefix of the client's stream events.
func (c *imagesResponses) eventPrefix() string {
	if c.edit() {
		return "image_edit."
	}
	return "image_generation."
}

// imagesDropped are the request members without a place in the Responses
// request: left out and recorded.
var imagesDropped = []string{"user", "safety_identifier", "style"}

// imagesInstructed are the tool parameters also stated in instructions, in
// this order: the Codex backend sizes the image from the instructions only
// (CONTRACTS §121.2).
var imagesInstructed = []string{"size", "quality", "background"}

func (c *imagesResponses) Prepare(body []byte, opt Options) (Converter, []byte, error) {
	if opt.AdaptModel == "" {
		return nil, nil, fmt.Errorf("convert: %s -> %s needs the model of the account type's adapted endpoint", c.from, relay)
	}
	p := &imagesResponses{from: c.from, prepared: true}
	out, err := p.request(body, opt.AdaptModel)
	if err != nil {
		return nil, nil, &RequestError{Cause: err}
	}
	p.reqLosses = len(p.lost.list)
	return p, out, nil
}

func (c *imagesResponses) request(raw []byte, model string) ([]byte, error) {
	in, err := decodeJObject(raw, "request")
	if err != nil {
		return nil, err
	}
	prompt, err := jstr(in["prompt"], "prompt")
	if err != nil {
		return nil, err
	}
	if n, ok := in["n"]; ok && n != nil {
		if _, err := jint(n, "n", 1, 1); err != nil {
			return nil, cfail("n", "only one image per request")
		}
	}
	if f, ok := in["response_format"]; ok && f != nil && f != "b64_json" {
		return nil, cfail("response_format", "only b64_json")
	}
	content := []any{jobject{"type": "input_text", "text": prompt}}
	tool := jobject{"type": "image_generation", "action": "generate"}
	if c.edit() {
		tool["action"] = "edit"
		if content, err = c.editImages(in, content); err != nil {
			return nil, err
		}
	}
	out := jobject{"model": model, "tool_choice": jobject{"type": "image_generation"},
		"input": []any{jobject{"type": "message", "role": "user", "content": content}}}
	if s, ok := in["stream"]; ok && s != nil {
		out["stream"] = s
	}
	for _, k := range members(in) {
		switch {
		case k == "prompt" || k == "n" || k == "response_format" || k == "stream":
		case slices.Contains(imagesDropped, k):
			c.lost.add(LossOther, k)
		default:
			tool[k] = in[k]
		}
	}
	out["tools"] = []any{tool}
	if s := imagesInstructions(tool); s != "" {
		out["instructions"] = s
	}
	return json.Marshal(out)
}

// editImages moves the images of an edit after the prompt, each an
// input_image part with the members as given, and its mask into the tool.
func (c *imagesResponses) editImages(in jobject, content []any) ([]any, error) {
	if v, ok := in["images"]; ok && v != nil {
		images, err := jlist(v, "images")
		if err != nil {
			return nil, err
		}
		for i, img := range images {
			o, err := jobj(img, jpos("images", i))
			if err != nil {
				return nil, err
			}
			part := jobject{"type": "input_image"}
			for _, k := range members(o) {
				part[k] = o[k]
			}
			content = append(content, part)
		}
		delete(in, "images")
	}
	if m, ok := in["mask"]; ok && m != nil {
		in["input_image_mask"] = m
		delete(in, "mask")
	}
	return content, nil
}

// imagesInstructions is the sentence stating the instructed parameters the
// tool has, "" when it has none.
func imagesInstructions(tool jobject) string {
	var parts []string
	for _, k := range imagesInstructed {
		if v, ok := tool[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %v", k, v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Generate the image with the image_generation tool using " + strings.Join(parts, ", ") + "."
}

// ---------------------------------------------------------------- response

// images are the images among the output items of a finished response
// (index i at indexes[i]): each image_generation_call with a result. The
// other items (a failed image call, the assistant's message) are response
// losses. The upstream's answer is the answer: with no image, there are
// none (CONTRACTS §121.7).
func (c *imagesResponses) images(items []jobject, indexes []int) []jobject {
	var out []jobject
	for i, item := range items {
		if r, ok := item["result"].(string); ok && r != "" && item["type"] == "image_generation_call" {
			out = append(out, item)
			continue
		}
		c.lost.add(LossOther, jpos("response.output", indexes[i]))
	}
	return out
}

// imageMeta are the members of an image_generation_call item the reply
// repeats.
var imageMeta = []string{"background", "output_format", "quality", "size"}

// copyMembers copies the members names of src present in it to dst.
func copyMembers(dst, src jobject, names []string) {
	for _, k := range names {
		if v, ok := src[k]; ok && v != nil {
			dst[k] = v
		}
	}
}

// outputItems lists the output items of response r and their indexes.
func outputItems(r jobject) ([]jobject, []int, error) {
	var items []jobject
	var indexes []int
	v, ok := r["output"]
	if !ok || v == nil {
		return nil, nil, nil
	}
	list, err := jlist(v, "response.output")
	if err != nil {
		return nil, nil, err
	}
	for i, x := range list {
		item, err := jobj(x, jpos("response.output", i))
		if err != nil {
			return nil, nil, err
		}
		items, indexes = append(items, item), append(indexes, i)
	}
	return items, indexes, nil
}

// Response converts a complete Responses reply: the images in output order
// as data (empty without one), the first image's background, format,
// quality and size, and the image tool's usage (CONTRACTS §121.7).
func (c *imagesResponses) Response(body []byte) ([]byte, error) {
	if !c.prepared {
		return nil, errUnprepared
	}
	c.lost.list = c.lost.list[:c.reqLosses]
	r, err := decodeJObject(body, "response")
	if err != nil {
		return nil, err
	}
	items, indexes, err := outputItems(r)
	if err != nil {
		return nil, err
	}
	images := c.images(items, indexes)
	out := jobject{"created": responseCreated(r)}
	if len(images) > 0 {
		copyMembers(out, images[0], imageMeta)
	}
	data := make([]any, len(images))
	for i, img := range images {
		d := jobject{"b64_json": img["result"]}
		copyMembers(d, img, []string{"revised_prompt"})
		data[i] = d
	}
	out["data"] = data
	imagesUsage(out, r)
	return json.Marshal(out)
}

// imagesUsage sets the image tool's usage of response r, as given, as the
// usage of out; out has none when r has none.
func imagesUsage(out, r jobject) {
	tu, _ := r["tool_usage"].(map[string]any)
	if u, ok := tu["image_gen"]; ok && u != nil {
		out["usage"] = u
	}
}

// ---------------------------------------------------------------- stream

func (c *imagesResponses) NewStream() StreamConverter {
	if c.prepared {
		c.lost.list = c.lost.list[:c.reqLosses]
	}
	return &imagesStream{p: c}
}

// imagesStream converts a Responses stream: partial images as they come,
// the finished items kept until the terminal event, which sends one
// completed event per image (the last with the usage) or an error event.
type imagesStream struct {
	p            *imagesResponses
	created      any
	items        []jobject
	indexes      []int
	done, failed bool
}

func (s *imagesStream) Event(ev Event) ([]Event, error) {
	if !s.p.prepared {
		return nil, errUnprepared
	}
	d := bytes.TrimSpace(ev.Data)
	if len(d) == 0 || string(d) == "[DONE]" || s.done || s.failed {
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
	switch typ {
	case "response.created":
		if r, ok := o["response"].(map[string]any); ok {
			s.created = r["created_at"]
		}
	case "response.image_generation_call.partial_image":
		return s.partial(o), nil
	case "response.output_item.done":
		return nil, s.itemDone(o)
	case "response.completed", "response.incomplete":
		return s.complete(o["response"])
	case "response.failed":
		e := jobject{"message": "upstream response failed"}
		if r, ok := o["response"].(map[string]any); ok {
			if x, ok := r["error"].(map[string]any); ok {
				e = x
			}
		}
		return s.fail(e), nil
	case "error":
		if x, ok := o["error"].(map[string]any); ok {
			return s.fail(x), nil
		}
		e := jobject{}
		copyMembers(e, o, []string{"code", "message", "param"})
		return s.fail(e), nil
	}
	return nil, nil
}

// event is one client event of type name.
func (s *imagesStream) event(name string, o jobject) Event {
	o["type"] = name
	b, _ := json.Marshal(o)
	return Event{Name: name, Data: b}
}

// partial forwards a partial image.
func (s *imagesStream) partial(o jobject) []Event {
	out := jobject{"b64_json": o["partial_image_b64"], "created_at": s.created}
	copyMembers(out, o, append([]string{"partial_image_index"}, imageMeta...))
	return []Event{s.event(s.p.eventPrefix()+"partial_image", out)}
}

// itemDone keeps a finished output item.
func (s *imagesStream) itemDone(o jobject) error {
	item, err := jobj(o["item"], "stream.item")
	if err != nil {
		return err
	}
	i, err := jint(o["output_index"], "stream.output_index", 0, 1<<31)
	if err != nil {
		return err
	}
	s.items, s.indexes = append(s.items, item), append(s.indexes, int(i))
	return nil
}

// complete ends the stream with the images of the terminal response, one
// completed event each (none without an image): its output when it lists any, else the items the
// stream finished (the Codex backend ends with an empty output).
func (s *imagesStream) complete(v any) ([]Event, error) {
	r, err := jobj(v, "stream.response")
	if err != nil {
		return nil, err
	}
	s.done = true
	items, indexes, err := outputItems(r)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		items, indexes = s.items, s.indexes
	}
	if s.created == nil {
		s.created = r["created_at"]
	}
	images := s.p.images(items, indexes)
	out := make([]Event, len(images))
	for i, img := range images {
		o := jobject{"b64_json": img["result"], "created_at": s.created}
		copyMembers(o, img, imageMeta)
		if i == len(images)-1 {
			imagesUsage(o, r)
		}
		out[i] = s.event(s.p.eventPrefix()+"completed", o)
	}
	return out, nil
}

// fail ends the stream with an error event carrying the upstream's error.
func (s *imagesStream) fail(e map[string]any) []Event {
	s.failed = true
	return []Event{s.event("error", jobject{"error": e})}
}

// Flush fails a stream that ended without its terminal event.
func (s *imagesStream) Flush() ([]Event, error) {
	if !s.failed && !s.done {
		return nil, cfail("stream", "upstream ended without response.completed")
	}
	return nil, nil
}
