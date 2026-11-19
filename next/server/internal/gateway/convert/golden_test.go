package convert

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The golden samples (CONVERT-MATRIX §10.1) live in
// testdata/golden/<from>~<to>/<case>/:
//
//	case.json                 {"options": {"model", "stream", "adapt_model"}, "upstream": path or [paths],
//	                           "media": {"<url>": "fixtures/x.png"},
//	                           "claude_default_max_tokens": {"default": 8192, "<model>": n}, "note"}
//	in.request.json           client request
//	want.request.json         expected upstream request
//	want.request_error.txt    instead of want.request.json: the RequestError message
//	want.losses.json          request losses {"tool": [...], "reasoning", "image", "other"}
//	upstream.response.json    upstream reply (when not referenced by case.json)
//	upstream.stream.sse       upstream stream (likewise)
//	want.response.json        client reply; want.response_error.txt when converting fails
//	want.stream.sse           client stream; want.stream_error.txt when converting fails
//	want.response_losses.json response losses of the reply and the stream together
//
// case.json "upstream" paths are relative to the case directory or to
// testdata ("recorded/anthropic.messages/tools.stream.sse"). Upstream files
// in the case directory are always used. go test -run TestGolden -update
// rewrites the want.* files (normalized: hex ids of 32 or more digits and
// created timestamps); review them before committing.

var update = flag.Bool("update", false, "rewrite the want files of golden samples and IR snapshots")

type goldenSpec struct {
	Options struct {
		Model      string `json:"model"`
		Stream     bool   `json:"stream"`
		AdaptModel string `json:"adapt_model"`
	} `json:"options"`
	Upstream  json.RawMessage   `json:"upstream"`
	Media     map[string]string `json:"media"`
	MaxTokens map[string]int    `json:"claude_default_max_tokens"`
	Note      string            `json:"note"`
}

func TestGolden(t *testing.T) {
	reg := Default()
	dirs, _ := filepath.Glob(filepath.Join("testdata", "golden", "*~*", "*"))
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		name := filepath.Base(filepath.Dir(dir))
		from, to, _ := strings.Cut(name, "~")
		t.Run(name+"/"+filepath.Base(dir), func(t *testing.T) {
			conv, ok := reg.Resolve(from, to)
			if !ok {
				t.Skipf("no converter %s -> %s (TestGoldenCoverage fails for it)", from, to)
			}
			runGolden(t, reg, conv, dir)
		})
	}
}

func runGolden(t *testing.T, reg *Registry, conv Converter, dir string) {
	gc := loadCase(t, dir)
	in := mustRead(t, filepath.Join(dir, "in.request.json"))
	opt := gc.options()
	bound, out, err := conv.Prepare(in, opt)
	if err != nil {
		var re *RequestError
		if !errors.As(err, &re) {
			t.Fatalf("Prepare failed without RequestError: %v", err)
		}
		checkText(t, dir, "want.request_error.txt", err.Error())
		return
	}
	checkText(t, dir, "want.request_error.txt", "")
	checkJSON(t, dir, "want.request.json", out)
	checkJSON(t, dir, "want.losses.json", lossJSON(Losses(bound), false))

	var respLost []Loss
	ups := gc.upstreams(t, dir)
	for _, up := range ups {
		body := mustRead(t, up)
		if strings.HasSuffix(up, ".sse") {
			got, err := runStream(bound, parseSSE(body))
			checkSSE(t, dir, "want.stream.sse", got)
			checkText(t, dir, "want.stream_error.txt", errText(err))
		} else {
			got, err := bound.Response(body)
			checkJSON(t, dir, "want.response.json", got)
			checkText(t, dir, "want.response_error.txt", errText(err))
		}
		respLost = append(respLost, Losses(bound)...)
	}
	if len(ups) > 0 {
		checkJSON(t, dir, "want.response_losses.json", lossJSON(respLost, true))
	}
	if hops := Hops(conv); len(hops) == 3 {
		checkChain(t, reg, hops, in, opt, out, bound, ups)
	}
}

// checkChain asserts a composed converter behaves as its two hops run one
// after the other (CONVERT-MATRIX §4).
func checkChain(t *testing.T, reg *Registry, hops []string, in []byte, opt Options, out []byte, bound Converter, ups []string) {
	t.Helper()
	a, okA := reg.Resolve(hops[0], hops[1])
	b, okB := reg.Resolve(hops[1], hops[2])
	if !okA || !okB {
		t.Fatalf("composition %v: a hop does not resolve", hops)
	}
	ba, mid, err := a.Prepare(in, opt)
	if err != nil {
		t.Fatalf("hop 0: %v", err)
	}
	bb, chained, err := b.Prepare(mid, opt)
	if err != nil {
		t.Fatalf("hop 1: %v", err)
	}
	if !bytes.Equal(normalizeJSON(chained), normalizeJSON(out)) {
		t.Errorf("composed request differs from the chain:\n%s\nchain:\n%s", out, chained)
	}
	for _, up := range ups {
		body := mustRead(t, up)
		var got, want []byte
		if strings.HasSuffix(up, ".sse") {
			evs, _ := runEvents(bb.NewStream(), parseSSE(body))
			want, _ = runStream(ba, evs)
			got, _ = runStream(bound, parseSSE(body))
		} else {
			mid, _ := bb.Response(body)
			want, _ = ba.Response(mid)
			got, _ = bound.Response(body)
		}
		if !bytes.Equal(normalizeOutput(got), normalizeOutput(want)) {
			t.Errorf("composed reply to %s differs from the chain:\n%s\nchain:\n%s", up, got, want)
		}
	}
}

func loadCase(t *testing.T, dir string) goldenSpec {
	t.Helper()
	var gc goldenSpec
	if raw, err := os.ReadFile(filepath.Join(dir, "case.json")); err == nil {
		if err := json.Unmarshal(raw, &gc); err != nil {
			t.Fatalf("case.json: %v", err)
		}
	}
	return gc
}

func (gc goldenSpec) options() Options {
	return Options{Ctx: context.Background(), Model: gc.Options.Model, Stream: gc.Options.Stream,
		Fetch: fixtureFetcher(gc.Media), ClaudeDefaultMaxTokens: gc.maxTokens, AdaptModel: gc.Options.AdaptModel}
}

// maxTokens stands for the models of the gateway's claude_max_tokens rule
// (the fixture key keeps the setting's former name), {"default": 8192}
// unless case.json gives one.
func (gc goldenSpec) maxTokens(model string) int {
	if n, ok := gc.MaxTokens[model]; ok {
		return n
	}
	if n, ok := gc.MaxTokens["default"]; ok {
		return n
	}
	return 8192
}

// upstreams lists the upstream files of a case: the case.json references,
// then the upstream.* files of the case directory.
func (gc goldenSpec) upstreams(t *testing.T, dir string) []string {
	t.Helper()
	var refs []string
	if len(gc.Upstream) > 0 {
		var one string
		if json.Unmarshal(gc.Upstream, &one) == nil {
			refs = []string{one}
		} else if err := json.Unmarshal(gc.Upstream, &refs); err != nil {
			t.Fatalf("case.json upstream: %v", err)
		}
	}
	var out []string
	for _, ref := range refs {
		p := filepath.Join(dir, ref)
		if _, err := os.Stat(p); err != nil {
			p = filepath.Join("testdata", ref)
		}
		out = append(out, p)
	}
	for _, name := range []string{"upstream.response.json", "upstream.stream.sse"} {
		if p := filepath.Join(dir, name); fileExists(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// fixtureFetcher serves media URLs from testdata files, never the network.
func fixtureFetcher(media map[string]string) MediaFetcher {
	return func(_ context.Context, url string) (string, string, error) {
		path, ok := media[url]
		if !ok {
			return "", "", fmt.Errorf("no fixture for %s", url)
		}
		data, err := os.ReadFile(filepath.Join("testdata", path))
		if err != nil {
			return "", "", err
		}
		mt := mime.TypeByExtension(filepath.Ext(path))
		if mt == "" {
			mt = http.DetectContentType(data)
		}
		return mt, base64.StdEncoding.EncodeToString(data), nil
	}
}

// runStream feeds events through a new stream of c and returns the client
// stream in wire format.
func runStream(c Converter, evs []Event) ([]byte, error) {
	out, err := runEvents(c.NewStream(), evs)
	var buf []byte
	for _, ev := range out {
		buf = AppendSSE(buf, ev)
	}
	return buf, err
}

// runEvents feeds events one by one, then flushes; it stops at the first
// error and returns what was produced until then.
func runEvents(sc StreamConverter, evs []Event) ([]Event, error) {
	var out []Event
	for _, ev := range evs {
		got, err := sc.Event(ev)
		out = append(out, cloneEvents(got)...)
		if err != nil {
			return out, err
		}
	}
	got, err := sc.Flush()
	return append(out, cloneEvents(got)...), err
}

func cloneEvents(evs []Event) []Event {
	out := make([]Event, len(evs))
	for i, ev := range evs {
		out[i] = Event{Name: ev.Name, Data: bytes.Clone(ev.Data)}
	}
	return out
}

// parseSSE splits a wire-format stream into events, as the gateway does.
func parseSSE(b []byte) []Event {
	var out []Event
	var cur Event
	has := false
	flush := func() {
		if has {
			out = append(out, cur)
		}
		cur, has = Event{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			cur.Name, has = strings.TrimSpace(line[len("event:"):]), true
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line[len("data:"):], " ")
			if len(cur.Data) > 0 {
				cur.Data = append(cur.Data, '\n')
			}
			cur.Data, has = append(cur.Data, d...), true
		}
	}
	flush()
	return out
}

// lossJSON renders losses by category, without empty categories; response
// losses only (paths under "response.") or request losses only. A later
// hop's paths are marked "<hop>:".
func lossJSON(ls []Loss, response bool) []byte {
	out := map[LossCategory][]string{}
	for _, l := range ls {
		if strings.HasPrefix(l.Path, "response.") != response {
			continue
		}
		p := l.Path
		if l.Hop > 0 {
			p = fmt.Sprintf("%d:%s", l.Hop, p)
		}
		if !slices.Contains(out[l.Category], p) {
			out[l.Category] = append(out[l.Category], p)
		}
	}
	for _, v := range out {
		slices.Sort(v)
	}
	b, _ := json.Marshal(out)
	return b
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var (
	hexID     = regexp.MustCompile(`[0-9a-fA-F]{32,}`)
	timeField = map[string]bool{"created": true, "created_at": true}
)

// normalizeValue replaces long hex ids and creation timestamps.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if _, num := e.(json.Number); num && timeField[k] {
				x[k] = json.Number("0")
				continue
			}
			x[k] = normalizeValue(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normalizeValue(e)
		}
	case string:
		return hexID.ReplaceAllString(x, "<hex>")
	}
	return v
}

// normalizeJSON returns a JSON document normalized and indented; other
// data comes back unchanged.
func normalizeJSON(b []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return b
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(normalizeValue(v))
	return out.Bytes()
}

// normalizeSSE renders a stream with normalized, compact JSON data.
func normalizeSSE(b []byte) []byte {
	var out []byte
	for _, ev := range parseSSE(b) {
		var buf bytes.Buffer
		if json.Compact(&buf, normalizeJSON(ev.Data)) == nil {
			ev.Data = buf.Bytes()
		}
		out = AppendSSE(out, ev)
	}
	return out
}

// normalizeOutput normalizes a reply that is a JSON document or a stream.
func normalizeOutput(b []byte) []byte {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return normalizeJSON(b)
	}
	return normalizeSSE(b)
}

func checkJSON(t *testing.T, dir, name string, got []byte) {
	t.Helper()
	if got != nil {
		got = normalizeJSON(got)
	}
	checkFile(t, dir, name, got, normalizeJSON)
}

func checkSSE(t *testing.T, dir, name string, got []byte) {
	t.Helper()
	checkFile(t, dir, name, normalizeSSE(got), normalizeSSE)
}

func checkText(t *testing.T, dir, name, got string) {
	t.Helper()
	var b []byte
	if got != "" {
		b = []byte(got + "\n")
	}
	checkFile(t, dir, name, b, func(b []byte) []byte { return b })
}

// checkFile compares got with dir/name (after norm), or rewrites the file
// under -update; a nil got means the file must not exist.
func checkFile(t *testing.T, dir, name string, got []byte, norm func([]byte) []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if *update {
		if got == nil {
			_ = os.Remove(path)
			return
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	switch {
	case err != nil && got == nil:
	case err != nil:
		t.Errorf("%s missing (run with -update and review):\n%s", path, got)
	case got == nil:
		t.Errorf("%s exists but nothing was produced", path)
	case !bytes.Equal(norm(want), got):
		t.Errorf("%s differs:\ngot:\n%s\nwant:\n%s", path, got, norm(want))
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
