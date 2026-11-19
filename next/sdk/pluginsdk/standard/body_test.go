package standard_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// realistic is a request body the golden cases cannot stand for (theirs
// hold only model and stream): unusual key order, white space and line
// breaks, fields no library knows, \u escapes next to raw UTF-8, HTML
// characters, numbers beyond float64 and int64, and a few MiB of history.
func realistic(tail string) []byte {
	var b strings.Builder
	b.WriteString("{\n  \"zeta_unknown\": {\"nested\": [1, 2.50, -0.0, 1e400]},\n")
	b.WriteString("  \"model\" :\t\"gpt-4o-mini\",\n")
	b.WriteString("  \"big\": 123456789012345678901234567890,\n")
	b.WriteString("  \"text\": \"caf\\u00e9 café \\ud83d\\ude00 <b>&amp;</b> \\\"q\\\" \\/\",\n")
	b.WriteString("  \"service_tier\" : \"priority\" ,\n")
	b.WriteString("  \"messages\": [")
	for i := 0; i < 20000; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`{"role":"user","content":[{"type":"text","text":"turn ` + strings.Repeat("x", 100) + `"}],"cache_control":{"type":"ephemeral"}}`)
	}
	b.WriteString("],\n  \"safety_identifier\": \"u-1\",\n  \"user\": \"u1\"" + tail + "\n}")
	return []byte(b.String())
}

func build(t *testing.T, ty standard.Type, protocol string, stream bool, body []byte) ([]byte, error) {
	t.Helper()
	out, err := standard.New(ty).BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: protocol, Model: "gpt-4o-mini", Stream: stream},
		Account: &pluginv1.Account{Id: 1, Type: ty.ID, CredentialsJson: `{"api_key":"sk-test-0123456789"}`},
		Body:    body,
	})
	return out.GetBody(), err
}

// filtering is t filtering service_tier and safety_identifier.
func filtering(t standard.Type) standard.Type {
	t.FilterFields = []string{"service_tier", "safety_identifier"}
	return t
}

// A body without edits is returned as it came, byte for byte.
func TestBodyUnchanged(t *testing.T) {
	streaming, still := realistic(`, "stream": true`), realistic(`, "stream": false`)
	for name, tc := range map[string]struct {
		ty       standard.Type
		protocol string
		stream   bool
		in       []byte
	}{
		"anthropic messages":     {anthropic.APIKey("apikey"), anthropic.ProtocolMessages, true, streaming},
		"openai responses":       {openai.APIKey("apikey"), openai.ProtocolResponses, true, streaming},
		"openai chat, no stream": {openai.APIKey("apikey"), openai.ProtocolChat, false, still},
		"openai websocket turn":  {openai.APIKey("apikey"), openai.ProtocolResponsesWS, true, streaming},
	} {
		in := tc.in
		out, err := build(t, tc.ty, tc.protocol, tc.stream, in)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("%s: body changed (%d -> %d bytes), %v", name, len(in), len(out), err)
		}
	}
}

// The declared edits change their own bytes and nothing else.
func TestBodyEdits(t *testing.T) {
	in := realistic("")
	end := bytes.LastIndexByte(in, '}')
	t.Run("stream usage is added at the end", func(t *testing.T) {
		out, err := build(t, openai.APIKey("apikey"), openai.ProtocolChat, true, in)
		want := string(in[:end]) + `,"stream_options":{"include_usage":true}` + string(in[end:])
		if err != nil || string(out) != want {
			t.Fatalf("err %v, tail %q", err, tail(out))
		}
	})
	// A stream-only account type: the core forces "stream": true into the
	// body while meta.stream keeps the client's false. The answer streams,
	// so it must carry the usage chunk or the host reads no usage.
	t.Run("a body the core made stream asks for usage too", func(t *testing.T) {
		in := realistic(`, "stream": true`)
		end := bytes.LastIndexByte(in, '}')
		out, err := build(t, openai.APIKey("apikey"), openai.ProtocolChat, false, in)
		want := string(in[:end]) + `,"stream_options":{"include_usage":true}` + string(in[end:])
		if err != nil || string(out) != want {
			t.Fatalf("err %v, tail %q", err, tail(out))
		}
	})
	t.Run("filter fields and the edit in one pass", func(t *testing.T) {
		out, err := build(t, filtering(openai.APIKey("apikey")), openai.ProtocolChat, true, in)
		want := strings.Replace(strings.Replace(string(in), ",\n  \"service_tier\" : \"priority\"", "", 1), ",\n  \"safety_identifier\": \"u-1\"", "", 1)
		end := strings.LastIndexByte(want, '}')
		want = want[:end] + `,"stream_options":{"include_usage":true}` + want[end:]
		if err != nil || string(out) != want {
			t.Fatalf("err %v, tail %q", err, tail(out))
		}
	})
	t.Run("an existing stream_options is edited in place", func(t *testing.T) {
		in := realistic(",\n  \"stream_options\": {\"x\": 1, \"include_usage\": false}")
		out, err := build(t, openai.APIKey("apikey"), openai.ProtocolChat, true, in)
		want := strings.Replace(string(in), `"include_usage": false`, `"include_usage": true`, 1)
		if err != nil || string(out) != want {
			t.Fatalf("err %v, tail %q", err, tail(out))
		}
	})
	t.Run("filter fields remove their members only", func(t *testing.T) {
		out, err := build(t, filtering(openai.APIKey("apikey")), openai.ProtocolResponses, false, in)
		// A member goes with the separator before it.
		want := strings.Replace(strings.Replace(string(in), ",\n  \"service_tier\" : \"priority\"", "", 1), ",\n  \"safety_identifier\": \"u-1\"", "", 1)
		if err != nil || string(out) != want {
			t.Fatalf("err %v, head %q, tail %q", err, out[:200], tail(out))
		}
	})
}

func tail(b []byte) []byte { return b[max(0, len(b)-120):] }

// The core hands over valid JSON without duplicate keys. An edit that
// would have to choose between two members, or edit something that is not
// JSON, refuses the body; members elsewhere are not the edit's business,
// and a filter field goes wherever it appears.
func TestBodyAmbiguous(t *testing.T) {
	chat := openai.APIKey("apikey")
	for name, tc := range map[string]struct {
		ty       standard.Type
		protocol string
		in, want string // want "" = refused
	}{
		"duplicate on the edited path":       {chat, openai.ProtocolChat, `{"stream_options":{"include_usage":false},"stream_options":{}}`, ""},
		"duplicate inside the edited object": {chat, openai.ProtocolChat, `{"stream_options":{"include_usage":false,"include_usage":false}}`, ""},
		"duplicate elsewhere":                {chat, openai.ProtocolChat, `{"a":1,"a":2}`, `{"a":1,"a":2,"stream_options":{"include_usage":true}}`},
		"filter field twice":                 {filtering(openai.APIKey("apikey")), openai.ProtocolResponses, `{"service_tier":"a","x":1,"service_tier":"b"}`, `{"x":1}`},
		"not JSON, edited":                   {chat, openai.ProtocolChat, `{"model":`, ""},
		"not JSON, filtered":                 {filtering(openai.APIKey("apikey")), openai.ProtocolResponses, `{"model":`, ""},
		// Without an edit the library does not read the body at all.
		"not JSON, not edited": {anthropic.APIKey("apikey"), anthropic.ProtocolMessages, `{"model":`, `{"model":`},
	} {
		out, err := build(t, tc.ty, tc.protocol, true, []byte(tc.in))
		switch {
		case tc.want == "" && status.Code(err) != codes.InvalidArgument:
			t.Errorf("%s: %s, %v; want INVALID_ARGUMENT", name, out, err)
		case tc.want != "" && (err != nil || string(out) != tc.want):
			t.Errorf("%s: %s, %v; want %s", name, out, err, tc.want)
		}
	}
}
