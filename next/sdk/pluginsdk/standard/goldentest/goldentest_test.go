package goldentest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// counts are the cases of each file. They are frozen: a file never gains
// or loses a case.
var counts = map[string]int{"anthropic": 343, "gemini": 371, "openai": 441, "relay": 343}

// TestFixtures checks the shape of every case: a known kind, a unique name,
// exactly one of an answer and an error, an execute case that decodes and
// carries JSON bodies, a classify case with its clock.
func TestFixtures(t *testing.T) {
	if got := Plugins(); !slices.Equal(got, []string{"anthropic", "gemini", "openai", "relay"}) {
		t.Fatalf("plugins = %v", got)
	}
	kinds := []string{KindExecute, KindTest, KindModels, KindValidate, KindClassify}
	for _, plugin := range Plugins() {
		f, err := Load(plugin)
		if err != nil {
			t.Fatal(err)
		}
		if f.Plugin != plugin || f.AccountType == "" || len(f.PassHeaders) == 0 || len(f.Cases) != counts[plugin] {
			t.Errorf("%s: plugin %q type %q, %d pass headers, %d cases (want %d)", plugin, f.Plugin, f.AccountType, len(f.PassHeaders), len(f.Cases), counts[plugin])
		}
		seen, byKind := map[string]bool{}, map[string]int{}
		for i := range f.Cases {
			c := &f.Cases[i]
			if seen[c.Name] || c.Name == "" {
				t.Errorf("%s: case name %q is empty or used twice", plugin, c.Name)
			}
			seen[c.Name] = true
			byKind[c.Kind]++
			if !slices.Contains(kinds, c.Kind) {
				t.Errorf("%s: %s: kind %q", plugin, c.Name, c.Kind)
			}
			if (c.Want == nil) == (c.Error == nil) || len(c.Input) == 0 {
				t.Errorf("%s: %s: wants exactly one of an answer and an error, and an input", plugin, c.Name)
			}
			if (c.Now != "") != (c.Kind == KindClassify) {
				t.Errorf("%s: %s: now %q", plugin, c.Name, c.Now)
			}
			if c.Kind != KindExecute {
				continue
			}
			in, want, err := c.Execute()
			if err != nil {
				t.Errorf("%s: %s: %v", plugin, c.Name, err)
				continue
			}
			if !json.Valid([]byte(in.Body)) || (want != nil && !json.Valid([]byte(want.Body))) {
				t.Errorf("%s: %s: a body is not JSON", plugin, c.Name)
			}
			if want != nil && (len(want.Headers) != 1 || want.Method == "" || want.URL == "") {
				t.Errorf("%s: %s: want %+v", plugin, c.Name, want)
			}
		}
		for _, k := range kinds {
			if byKind[k] == 0 {
				t.Errorf("%s: no %s case", plugin, k)
			}
		}
	}
	if _, err := Load("azure_openai"); err == nil {
		t.Error("loaded cases of a plugin that has none")
	}
}

// find returns the first case of plugin that satisfies ok.
func find(t *testing.T, plugin string, ok func(*Case) bool) *Case {
	t.Helper()
	f, err := Load(plugin)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Cases {
		if ok(&f.Cases[i]) {
			return &f.Cases[i]
		}
	}
	t.Fatal("no such case")
	return nil
}

// TestCheckExecute proves Check accepts the recorded request and reports
// every part of it that differs.
func TestCheckExecute(t *testing.T) {
	c := find(t, "openai", func(c *Case) bool {
		if c.Kind != KindExecute || c.Error != nil {
			return false
		}
		in, want, _ := c.Execute()
		return in.Body != want.Body && len(want.DefaultHeaders) > 1
	})
	_, want, _ := c.Execute()
	answer := func(change func(*UpstreamRequest)) Subject {
		return Subject{Execute: func(context.Context, *ExecuteInput) (*UpstreamRequest, error) {
			out := *want
			out.Headers, out.DefaultHeaders = cloneUpper(want.Headers), cloneUpper(want.DefaultHeaders)
			change(&out)
			return &out, nil
		}}
	}
	// Header names are not case-sensitive.
	if d := c.Check(t.Context(), answer(func(*UpstreamRequest) {})); len(d) != 0 {
		t.Fatalf("the recorded request differs: %v", d)
	}
	for name, change := range map[string]func(*UpstreamRequest){
		"method":         func(r *UpstreamRequest) { r.Method = "PUT" },
		"url":            func(r *UpstreamRequest) { r.URL += "?x=1" },
		"upstream model": func(r *UpstreamRequest) { r.UpstreamModel += "x" },
		"body bytes":     func(r *UpstreamRequest) { r.Body = " " + r.Body },
		"body value":     func(r *UpstreamRequest) { r.Body = "{}" },
		"credential":     func(r *UpstreamRequest) { r.Headers = nil },
		"moved header": func(r *UpstreamRequest) {
			for k, v := range r.Headers {
				r.DefaultHeaders[k] = v
			}
			r.Headers = nil
		},
		"extra default header": func(r *UpstreamRequest) { r.DefaultHeaders["cookie"] = "a=b" },
		"header value":         func(r *UpstreamRequest) { r.DefaultHeaders["CONTENT-TYPE"] = "text/plain" },
		"header twice":         func(r *UpstreamRequest) { r.DefaultHeaders["content-type"] = r.DefaultHeaders["CONTENT-TYPE"] },
	} {
		n := 1
		if name == "moved header" {
			n = 2 // missing from one set, extra in the other
		}
		if d := c.Check(t.Context(), answer(change)); len(d) != n {
			t.Errorf("%s: differences %v, want %d", name, d, n)
		}
	}
	if d := c.Check(t.Context(), Subject{Execute: func(context.Context, *ExecuteInput) (*UpstreamRequest, error) { return nil, nil }}); len(d) != 1 {
		t.Errorf("no request: %v", d)
	}
	if d := c.Check(t.Context(), Subject{Execute: func(context.Context, *ExecuteInput) (*UpstreamRequest, error) {
		return nil, errors.New("boom")
	}}); len(d) != 1 {
		t.Errorf("an error where a request is recorded: %v", d)
	}
	if d := c.Check(t.Context(), Subject{}); len(d) != 1 {
		t.Errorf("a subject without Execute: %v", d)
	}
}

func cloneUpper(h map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		b := []byte(k)
		for i, ch := range b {
			if ch >= 'a' && ch <= 'z' {
				b[i] = ch - 'a' + 'A'
			}
		}
		out[string(b)] = v
	}
	return out
}

// TestCheckError: the message decides, with or without a plugin's wrapping;
// the code does not.
func TestCheckError(t *testing.T) {
	c := find(t, "anthropic", func(c *Case) bool { return c.Kind == KindExecute && c.Error != nil })
	fail := func(err error) Subject {
		return Subject{Execute: func(context.Context, *ExecuteInput) (*UpstreamRequest, error) { return nil, err }}
	}
	for _, msg := range []string{c.Error.Message, "account 42: " + c.Error.Message, "account -1: rpc error: code = Internal desc = " + c.Error.Message} {
		if d := c.Check(t.Context(), fail(errors.New(msg))); len(d) != 0 {
			t.Errorf("%q: %v", msg, d)
		}
	}
	if d := c.Check(t.Context(), fail(errors.New("another failure"))); len(d) != 1 {
		t.Errorf("another message: %v", d)
	}
	if d := c.Check(t.Context(), fail(nil)); len(d) != 1 {
		t.Errorf("no error: %v", d)
	}
}

// TestCheckMessages covers the kinds whose answer is a protocol message.
func TestCheckMessages(t *testing.T) {
	c := find(t, "gemini", func(c *Case) bool { return c.Kind == KindTest && c.Error == nil })
	want := &pluginv1.BuildTestRequestResponse{}
	if err := protojson.Unmarshal(c.Want, want); err != nil {
		t.Fatal(err)
	}
	test := func(out *pluginv1.BuildTestRequestResponse) Subject {
		return Subject{Test: func(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
			return out, nil
		}}
	}
	if d := c.Check(t.Context(), test(want)); len(d) != 0 {
		t.Fatalf("the recorded answer differs: %v", d)
	}
	if d := c.Check(t.Context(), test(&pluginv1.BuildTestRequestResponse{Method: want.Method, Url: want.Url})); len(d) != 1 {
		t.Errorf("another answer: %v", d)
	}
	if d := c.Check(t.Context(), test(nil)); len(d) != 1 {
		t.Errorf("no answer: %v", d)
	}
	if d := c.Check(t.Context(), Subject{}); len(d) != 1 {
		t.Errorf("a subject without Test: %v", d)
	}

	// A classification is asked with the case's clock.
	cl := find(t, "openai", func(c *Case) bool { return c.Kind == KindClassify })
	var asked time.Time
	cl.Check(t.Context(), Subject{Classify: func(_ context.Context, now time.Time, _ *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
		asked = now
		return &pluginv1.ClassifyErrorResponse{}, nil
	}})
	if asked.Format(time.RFC3339) != cl.Now {
		t.Errorf("classify clock %v, want %s", asked, cl.Now)
	}
}

// TestSelect: Forwarded keeps the execute cases of the protocols on an
// account of the type with a base URL of its own; BaseURL reads it where
// the presets do (settings first, a blank or non-string value is none).
func TestSelect(t *testing.T) {
	f, err := Load("openai")
	if err != nil {
		t.Fatal(err)
	}
	keep := Forwarded("apikey", "openai.chat")
	kept := 0
	for i := range f.Cases {
		c := &f.Cases[i]
		if !keep(c) {
			continue
		}
		kept++
		in, _, _ := c.Execute()
		if base, ok := c.BaseURL(); !ok || base == "" || in.Meta.Protocol != "openai.chat" || in.Account.Type != "apikey" {
			t.Errorf("%s: kept with base URL %q %v", c.Name, base, ok)
		}
	}
	if kept == 0 || kept == len(f.Cases) {
		t.Fatalf("kept %d of %d cases", kept, len(f.Cases))
	}
	for doc, want := range map[[2]string]string{
		{`{"base_url":"https://s"}`, `{"base_url":"https://c"}`}: "https://s",
		{`{}`, `{"base_url":"https://c"}`}:                       "https://c",
		{`{"base_url":"  "}`, `{"base_url":"https://c"}`}:        "",
		{`{"base_url":123}`, `{}`}:                               "",
		{`{}`, `{}`}:                                             "",
	} {
		input, _ := json.Marshal(ExecuteInput{Account: &Account{ID: 1, SettingsJSON: doc[0], CredentialsJSON: doc[1]}, Body: "{}"})
		c := &Case{Kind: KindExecute, Input: input, Error: &Error{Message: "x"}}
		if got, ok := c.BaseURL(); got != want || ok != (want != "") {
			t.Errorf("%v: %q %v, want %q", doc, got, ok, want)
		}
	}
	if _, ok := (&Case{Kind: KindTest}).BaseURL(); ok {
		t.Error("a test case has a base URL")
	}
}
