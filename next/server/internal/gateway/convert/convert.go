// Package convert holds the core's built-in protocol converters
// (ARCHITECTURE 6.6). A converter lets a client endpoint speaking protocol P
// be served by an account type whose upstream speaks protocol Q: the gateway
// converts the request body P→Q before the account type's plugin builds the
// upstream request, and converts the upstream response (JSON or SSE) Q→P on
// the way back. Conversion never goes through a plugin.
//
// Converters never apply a policy: each client request member is mapped,
// left out as a Loss of some category, or rejected with RequestError when
// the request cannot be expressed at all; the gateway decides per account
// whether the losses are acceptable (docs/CONVERT-MATRIX.md §2).
//
// A pair without a converter of its own is served through openai.responses
// when both hops exist (compose.go).
package convert

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Event is one server-sent event. Name is the "event:" field ("" when the
// event had none); Data is the joined "data:" lines without the trailing
// newline.
type Event struct {
	Name string
	Data []byte
}

// Grade is the quality of serving a client protocol with an upstream
// protocol; lower is better. Converters report good, fair or discouraged;
// native and local only label gateway routes.
type Grade int8

const (
	GradeNative      Grade = 0 // no conversion (routes only)
	GradeGood        Grade = 1
	GradeFair        Grade = 2
	GradeDiscouraged Grade = 3 // a composition of two converters
	GradeLocal       Grade = 9 // count_tokens estimated by the core (routes only)
)

// String returns native, good, fair, discouraged or local.
func (g Grade) String() string {
	switch g {
	case GradeNative:
		return "native"
	case GradeGood:
		return "good"
	case GradeFair:
		return "fair"
	case GradeDiscouraged:
		return "discouraged"
	case GradeLocal:
		return "local"
	}
	return fmt.Sprintf("grade(%d)", int8(g))
}

// converted reports whether a converter may report g.
func (g Grade) converted() bool { return g >= GradeGood && g <= GradeDiscouraged }

// LossCategory classifies a Loss for the conversion loss policies
// (CONVERT-MATRIX §2.3): safe rejects tool losses, strict rejects any.
type LossCategory string

const (
	// LossTool: the callable tool set, a forced tool choice or the tool
	// call history seen upstream differs from the client's.
	LossTool LossCategory = "tool"
	// LossReasoning: reasoning configuration ignored, reasoning history
	// dropped, or upstream signatures/encrypted reasoning not carried back.
	LossReasoning LossCategory = "reasoning"
	// LossImage: an input image (or its detail) dropped or replaced by text.
	LossImage LossCategory = "image"
	// LossOther: anything else (sampling, stop, metadata, cache marks,
	// system message position, files, audio, response_format details, ...).
	LossOther LossCategory = "other"
)

// Loss is one member of a request or response a conversion left out or
// altered while keeping the result valid (CONVERT-MATRIX §2.2 class 2).
type Loss struct {
	Category LossCategory
	// Path locates the member in this hop's input protocol: "tools[2]",
	// "messages[3].content[1]" for a request, "response.content[2]" for a
	// response.
	Path string
	// Hop is the conversion step the loss happened in (0 for a single hop).
	Hop int
}

// losses collects the Loss entries of one conversion hop. The zero value is
// ready to use.
type losses struct{ list []Loss }

func (l *losses) add(category LossCategory, path string) {
	l.list = append(l.list, Loss{Category: category, Path: path})
}

// sorted returns the entries ordered by hop, path and category, without
// duplicates.
func (l *losses) sorted() []Loss {
	out := slices.Clone(l.list)
	slices.SortFunc(out, func(a, b Loss) int {
		return cmp.Or(cmp.Compare(a.Hop, b.Hop), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Category, b.Category))
	})
	return slices.Compact(out)
}

// MediaFetcher downloads an http(s) media URL for a conversion that must
// inline it (to Gemini, CONVERT-MATRIX §9.2) and returns its MIME type and
// base64 data. The gateway's implementation caches by URL within a request.
type MediaFetcher func(ctx context.Context, url string) (mime, b64 string, err error)

// Options are the request facts a conversion needs beyond the body.
type Options struct {
	Ctx context.Context
	// Model is the client model (from the body, or the path for Gemini).
	Model string
	// Stream reports whether the client asked for a stream.
	Stream bool
	// Fetch downloads media URLs; nil makes a conversion that needs a
	// download fail with RequestError.
	Fetch MediaFetcher
	// ClaudeDefaultMaxTokens gives max_tokens for a request converted to
	// Anthropic without an output limit (the gateway's claude_max_tokens
	// rule, CONVERT-MATRIX §9.4); 0 or nil uses the converter's fallback.
	ClaudeDefaultMaxTokens func(model string) int
	// AdaptModel is the model of the account type's adapted endpoint the
	// request is served on (manifest.AdaptOptions.Model): the chat model an
	// image request is generated with (CONTRACTS §121.3).
	AdaptModel string
}

// Converter converts between a client protocol (From) and an upstream
// protocol (To). Registry entries are stateless and safe for concurrent use;
// Prepare returns a converter bound to one request, and per-stream state
// lives in the StreamConverter returned by NewStream.
type Converter interface {
	// From is the client endpoint protocol, e.g. "anthropic.messages".
	From() string
	// To is the upstream protocol, e.g. "openai.chat".
	To() string
	// Grade is GradeGood, GradeFair or GradeDiscouraged.
	Grade() Grade
	// Prepare converts a client request body (From) to an upstream request
	// body (To) and returns a converter bound to this request (the response
	// format may depend on it). The bound converter has the same From and
	// To; it is unusable when err is non-nil.
	Prepare(body []byte, opt Options) (Converter, []byte, error)
	// Response converts a complete non-stream upstream response body (To)
	// to the client format (From). Called on a bound converter only.
	Response(body []byte) ([]byte, error)
	// NewStream starts converting one upstream event stream, with state
	// independent of other calls. Called on a bound converter only.
	NewStream() StreamConverter
}

// StreamConverter converts one upstream SSE stream event by event. It may
// emit zero or more client events per upstream event and hold state between
// events (e.g. to synthesize start/stop events).
type StreamConverter interface {
	// Event converts one upstream event. ev.Data is only valid during the
	// call; copy it to keep it.
	Event(ev Event) ([]Event, error)
	// Flush is called once when the upstream stream ended (normally or
	// not) and returns any trailing client events.
	Flush() ([]Event, error)
}

// LossReporter is implemented by a bound converter. After Prepare it lists
// the request losses; after Response or a stream's Flush it also lists the
// response losses (paths starting with "response."). Sorted, without
// duplicates.
type LossReporter interface {
	Losses() []Loss
}

// HopReporter is implemented by a composed converter: the protocols it
// passes through, client first.
type HopReporter interface {
	Hops() []string
}

// ModelLocator is implemented by a converter whose upstream request carries
// the client's model elsewhere than the upstream endpoint's model path:
// ModelPath is its body path, where the gateway writes the account's
// mapping (CONTRACTS §121.4).
type ModelLocator interface {
	ModelPath() string
}

// ResponseModelLocator is implemented by a converter whose upstream
// response names the model that served the client's model elsewhere than
// the upstream endpoint's usage rules read it: the paths in a JSON
// response and in an SSE event, which replace the rules' model paths
// (the upstream model recorded, never a price; CONTRACTS §121.4).
type ResponseModelLocator interface {
	ResponseModelPaths() (json, sse string)
}

// terminal is implemented by a converter whose upstream request uses what
// only its upstream protocol has (the Responses image_generation tool): it
// is never the first hop of a composition.
type terminal interface {
	terminal()
}

// Losses returns the losses of a bound converter, nil when it reports none.
func Losses(c Converter) []Loss {
	if r, ok := c.(LossReporter); ok {
		return r.Losses()
	}
	return nil
}

// Hops returns the protocols a converter passes through: [From, To] unless
// it is a HopReporter.
func Hops(c Converter) []string {
	if h, ok := c.(HopReporter); ok {
		return h.Hops()
	}
	return []string{c.From(), c.To()}
}

// PairInfo describes one protocol pair a Registry can convert.
type PairInfo struct {
	From, To string
	Grade    Grade
	Hops     []string
}

type pair struct{ from, to string }

// Registry holds converters keyed by (client protocol, upstream protocol),
// and the compositions of two of them through openai.responses for the
// pairs without a converter of their own. The zero value and a nil
// *Registry are empty registries. Registry implements
// core.ProtocolConverters.
type Registry struct {
	mu       sync.RWMutex
	m        map[pair]Converter
	composed map[pair]Converter
}

var _ core.ProtocolConverters = (*Registry)(nil)

// ErrDuplicate is returned when a pair is registered twice.
var ErrDuplicate = errors.New("convert: converter already registered")

// NewRegistry returns a registry holding cs. It panics on invalid or
// duplicate converters (a programming error).
func NewRegistry(cs ...Converter) *Registry {
	r := &Registry{}
	for _, c := range cs {
		if err := r.Register(c); err != nil {
			panic(err)
		}
	}
	return r
}

// Default returns a new registry with the built-in converters
// (builtins.go).
func Default() *Registry { return NewRegistry(builtinConverters()...) }

// Register adds c. From and To must be non-empty and different, and Grade
// one a converter may report.
func (r *Registry) Register(c Converter) error {
	if c == nil {
		return errors.New("convert: nil converter")
	}
	from, to := c.From(), c.To()
	if from == "" || to == "" || from == to {
		return fmt.Errorf("convert: invalid converter %q -> %q", from, to)
	}
	if g := c.Grade(); !g.converted() {
		return fmt.Errorf("convert: converter %s -> %s has grade %s", from, to, g)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[pair]Converter{}
	}
	k := pair{from, to}
	if _, ok := r.m[k]; ok {
		return fmt.Errorf("%w: %s -> %s", ErrDuplicate, from, to)
	}
	r.m[k] = c
	r.compose()
	return nil
}

// Resolve returns the converter serving a clientProtocol endpoint with an
// upstreamProtocol account: the registered one, else the composition
// through openai.responses (CONVERT-MATRIX §4). Identical protocols need no
// converter and report false.
func (r *Registry) Resolve(clientProtocol, upstreamProtocol string) (Converter, bool) {
	if r == nil || clientProtocol == upstreamProtocol {
		return nil, false
	}
	k := pair{clientProtocol, upstreamProtocol}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c, ok := r.m[k]; ok {
		return c, true
	}
	c, ok := r.composed[k]
	return c, ok
}

// CanConvert reports whether Resolve finds a converter.
func (r *Registry) CanConvert(clientProtocol, upstreamProtocol string) bool {
	_, ok := r.Resolve(clientProtocol, upstreamProtocol)
	return ok
}

// Conversion implements core.ProtocolConverters.
func (r *Registry) Conversion(clientProtocol, upstreamProtocol string) (core.Conversion, bool) {
	c, ok := r.Resolve(clientProtocol, upstreamProtocol)
	if !ok {
		return core.Conversion{}, false
	}
	return core.Conversion{Rank: int(c.Grade()), Grade: c.Grade().String(), Hops: Hops(c)}, true
}

// Pairs lists the convertible pairs, compositions included, sorted by From
// then To.
func (r *Registry) Pairs() []PairInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]PairInfo, 0, len(r.m)+len(r.composed))
	for _, m := range []map[pair]Converter{r.m, r.composed} {
		for _, c := range m {
			out = append(out, PairInfo{From: c.From(), To: c.To(), Grade: c.Grade(), Hops: Hops(c)})
		}
	}
	r.mu.RUnlock()
	slices.SortFunc(out, func(a, b PairInfo) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return out
}

// compose rebuilds the compositions: from -> relay -> to for every pair of
// registered hops whose (from, to) has no converter of its own and spans
// two platforms (gemini.generate and gemini.stream_generate are one API in
// two stream modes, not a conversion). Called with r.mu held.
func (r *Registry) compose() {
	r.composed = map[pair]Converter{}
	for k1, a := range r.m {
		if _, last := a.(terminal); k1.to != relay || last {
			continue
		}
		for k2, b := range r.m {
			k := pair{k1.from, k2.to}
			if k2.from != relay || platformOf(k.from) == platformOf(k.to) {
				continue
			}
			if _, direct := r.m[k]; !direct {
				r.composed[k] = &composed{a: a, b: b}
			}
		}
	}
}

// platformOf is the platform of a protocol: the part before the first ".".
func platformOf(protocol string) string {
	p, _, _ := strings.Cut(protocol, ".")
	return p
}

// AppendSSE appends ev in wire format ("event: ...", one "data:" line per
// data line, blank line).
func AppendSSE(dst []byte, ev Event) []byte {
	if ev.Name != "" {
		dst = append(dst, "event: "...)
		dst = append(dst, ev.Name...)
		dst = append(dst, '\n')
	}
	data := ev.Data
	for {
		i := bytes.IndexByte(data, '\n')
		line := data
		if i >= 0 {
			line = data[:i]
		}
		dst = append(dst, "data: "...)
		dst = append(dst, line...)
		dst = append(dst, '\n')
		if i < 0 {
			break
		}
		data = data[i+1:]
	}
	return append(dst, '\n')
}
