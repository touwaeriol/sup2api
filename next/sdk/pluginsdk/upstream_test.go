package pluginsdk

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

func TestUpstreamBuildsTheTwoHeaderKinds(t *testing.T) {
	body := []byte(`{"model":"m","stream":true}`)
	got, err := NewUpstream("", "https://api.example.com/v1/messages?beta=true").
		Header("Authorization", "Bearer k").
		Headers(http.Header{"X-Amz-Date": {"20261118T000000Z"}}).
		DefaultHeader("Anthropic-Version", "2023-06-01").
		DefaultHeaders(http.Header{"Accept": {"a", "b"}}).
		DefaultHeader("x-empty", "").
		Body(body).Model("m-upstream").ResponseStream("aws-eventstream-chunk").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	want := &pluginv1.UpstreamRequest{
		Url:            "https://api.example.com/v1/messages?beta=true",
		Headers:        map[string]string{"authorization": "Bearer k", "x-amz-date": "20261118T000000Z"},
		DefaultHeaders: map[string]string{"anthropic-version": "2023-06-01", "accept": "a, b"},
		Body:           body,
		UpstreamModel:  "m-upstream",
		ResponseStream: "aws-eventstream-chunk",
	}
	if !proto.Equal(got, want) {
		t.Fatalf("built %v\nwant  %v", got, want)
	}
}

// RemoveHeader writes an authoritative empty value, which the host reads as
// "do not send this header"; a later Header of the name replaces it.
func TestUpstreamRemoveHeader(t *testing.T) {
	got, err := NewUpstream("", "https://api.example.com/v1").
		Header("Authorization", "Bearer k").
		RemoveHeader("X-Amz-Security-Token").
		RemoveHeader("x-later").Header("X-Later", "v").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"authorization": "Bearer k", "x-amz-security-token": "", "x-later": "v"}
	if !maps.Equal(got.GetHeaders(), want) {
		t.Fatalf("headers %v, want %v", got.GetHeaders(), want)
	}
}

func TestUpstreamBuildReportsWhatTheHostRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		build *Upstream
		want  string
	}{
		"relative url":      {NewUpstream("", "/v1/messages"), "not an absolute"},
		"other scheme":      {NewUpstream("", "ftp://example.com/x"), "not an absolute"},
		"content-length":    {NewUpstream("", "https://example.com").Header("Content-Length", "3"), `"content-length" belongs to the host's transport`},
		"accept-encoding":   {NewUpstream("", "https://example.com").DefaultHeader("Accept-Encoding", "gzip"), `"accept-encoding" belongs to the host's transport`},
		"connection":        {NewUpstream("", "https://example.com").DefaultHeader("connection", "close"), "transport"},
		"host":              {NewUpstream("", "https://example.com").Header("Host", "internal.example"), `"host" belongs to the host's transport`},
		"transfer-encoding": {NewUpstream("", "https://example.com").Header("transfer-encoding", "chunked"), "transport"},
		"line break":        {NewUpstream("", "https://example.com").Header("x-a", "b\r\nx-c: d"), "malformed"},
		"several at once":   {NewUpstream("", "nope").Header("connection", "x"), "transport"},
		"websocket is fine": {NewUpstream("GET", "wss://example.com/v1/realtime"), ""},
	} {
		got, err := tc.build.Build()
		if tc.want == "" {
			if err != nil || got == nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if status.Code(err) != codes.InvalidArgument || got != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, tc.want)
		}
	}
}

// A request written by hand is held to the same rules as a built one, the
// lower-case names included.
func TestValidateUpstreamRequest(t *testing.T) {
	for _, ok := range []*pluginv1.UpstreamRequest{
		{Url: "https://example.com", Headers: map[string]string{"authorization": "x"}},
		{Url: "https://example.com", Method: "PATCH", Headers: map[string]string{"x-a": "tab\tand obs-text \xff"}},
	} {
		if err := ValidateUpstreamRequest(ok); err != nil {
			t.Fatal(err)
		}
	}
	for name, r := range map[string]*pluginv1.UpstreamRequest{
		"nil":                 nil,
		"mixed case":          {Url: "https://example.com", Headers: map[string]string{"Authorization": "x"}},
		"mixed default":       {Url: "https://example.com", DefaultHeaders: map[string]string{"X-A": "x"}},
		"empty name":          {Url: "https://example.com", Headers: map[string]string{"": "x"}},
		"transport header":    {Url: "https://example.com", DefaultHeaders: map[string]string{"content-length": "1"}},
		"no host":             {Url: "https:///x"},
		"name with (":         {Url: "https://example.com", Headers: map[string]string{"x-(a)": "x"}},
		"name with /":         {Url: "https://example.com", Headers: map[string]string{"x/a": "x"}},
		"name with a tab":     {Url: "https://example.com", DefaultHeaders: map[string]string{"x\ta": "x"}},
		"name with a space":   {Url: "https://example.com", Headers: map[string]string{"x a": "x"}},
		"name with a colon":   {Url: "https://example.com", Headers: map[string]string{"x:a": "x"}},
		"non-ASCII name":      {Url: "https://example.com", Headers: map[string]string{"x-ü": "x"}},
		"value with NUL":      {Url: "https://example.com", Headers: map[string]string{"x-a": "a\x00b"}},
		"value with CR LF":    {Url: "https://example.com", DefaultHeaders: map[string]string{"x-a": "a\r\nx-b: c"}},
		"value with DEL":      {Url: "https://example.com", Headers: map[string]string{"x-a": "a\x7fb"}},
		"method with a space": {Url: "https://example.com", Method: "GET X"},
		"method with CR LF":   {Url: "https://example.com", Method: "GET\r\n"},
		"method with (":       {Url: "https://example.com", Method: "P(ST"},
	} {
		if err := ValidateUpstreamRequest(r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRequestModel(t *testing.T) {
	meta := &pluginv1.RequestMeta{Model: "meta-model"}
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"body wins":     {`{"model":"body-model"}`, "body-model"},
		"no body field": {`{"input":"x"}`, "meta-model"},
		"empty string":  {`{"model":""}`, "meta-model"},
		"not a string":  {`{"model":7}`, "meta-model"},
		"no body":       {``, "meta-model"},
	} {
		if got := RequestModel(&pluginv1.ExecuteRequest{Meta: meta, Body: []byte(tc.body)}); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := RequestModel(nil); got != "" {
		t.Errorf("nil request = %q", got)
	}
}

func TestRequestStream(t *testing.T) {
	for _, tc := range []struct {
		meta bool
		body string
		want bool
	}{
		{false, `{"stream":true}`, true},
		{true, `{"stream":false}`, true},
		{true, ``, true},
		{false, `{"stream":false}`, false},
		{false, `{"stream":"true"}`, false},
		{false, `{"model":"m"}`, false},
	} {
		in := &pluginv1.ExecuteRequest{Meta: &pluginv1.RequestMeta{Stream: tc.meta}, Body: []byte(tc.body)}
		if got := RequestStream(in); got != tc.want {
			t.Errorf("meta %v, body %s: %v", tc.meta, tc.body, got)
		}
	}
	if RequestStream(nil) {
		t.Error("nil request streams")
	}
}

func TestUpstreamHeaderMaps(t *testing.T) {
	got, err := NewUpstream("", "https://example.com").
		HeaderMap(map[string]string{"Authorization": "Bearer k", "x-empty": ""}).
		DefaultHeaderMap(map[string]string{"Accept": "a", "authorization": "default"}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got.GetHeaders(), map[string]string{"authorization": "Bearer k"}) ||
		!maps.Equal(got.GetDefaultHeaders(), map[string]string{"accept": "a", "authorization": "default"}) {
		t.Fatalf("headers %v, defaults %v", got.GetHeaders(), got.GetDefaultHeaders())
	}
}

func TestWebSocketURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.example.com/v1/responses?a=1": "wss://api.example.com/v1/responses?a=1",
		"http://127.0.0.1:8080/v1/responses":       "ws://127.0.0.1:8080/v1/responses",
		"wss://already.example.com/x":              "wss://already.example.com/x",
	} {
		if got := WebSocketURL(in); got != want {
			t.Errorf("WebSocketURL(%q) = %q, want %q", in, got, want)
		}
	}
}
