package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// transportHeaders belong to the host's transport (§5.4).
var transportHeaders = []string{"content-length", "transfer-encoding", "connection", "accept-encoding"}

// duplicateKey returns the path of the first key that repeats within one
// JSON object of body, "" when none does. body is valid JSON.
func duplicateKey(body []byte) string {
	type object struct {
		keys    map[string]bool
		path    string
		wantKey bool
	}
	var stack []*object // nil entries are arrays
	path := ""
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil { // io.EOF: the end
			return ""
		}
		top := (*object)(nil)
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if key, ok := tok.(string); ok && top != nil && top.wantKey {
			if top.keys[key] {
				return strings.TrimPrefix(top.path+"."+key, ".")
			}
			top.keys[key], top.wantKey, path = true, false, top.path+"."+key
			continue
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, &object{keys: map[string]bool{}, path: path, wantKey: true})
			continue
		case json.Delim('['):
			stack = append(stack, nil)
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		// A value ended: its object, if any, expects the next key.
		if len(stack) > 0 && stack[len(stack)-1] != nil {
			stack[len(stack)-1].wantKey, path = true, stack[len(stack)-1].path
		}
	}
}

// expectedModel is the model reference the relayed request must keep: the
// declared mapping, else the body's model, else the meta's.
func expectedModel(c *Case) string {
	switch {
	case c.UpstreamModel != "":
		return c.UpstreamModel
	case gjson.GetBytes(c.Body, "model").String() != "":
		return gjson.GetBytes(c.Body, "model").String()
	}
	return c.Meta.GetModel()
}

// modelKept reports how r loses the model reference want: the body's model
// must be want or, with no model in the body, the URL must name it.
func modelKept(r *pluginv1.UpstreamRequest, want string) string {
	if got := gjson.GetBytes(r.GetBody(), "model"); got.Exists() {
		if got.String() != want {
			return fmt.Sprintf("the relayed body's model is %q, want %q", got.String(), want)
		}
		return ""
	}
	for _, form := range []string{want, url.PathEscape(want), url.QueryEscape(want)} {
		if strings.Contains(r.GetUrl(), form) {
			return ""
		}
	}
	return fmt.Sprintf("neither the relayed body nor the URL %q names the model %q", r.GetUrl(), want)
}

// credentialValues are the string values of an account's credentials
// long enough to recognise in a header.
func credentialValues(a *pluginv1.Account) []string {
	var out []string
	var walk func(gjson.Result)
	walk = func(v gjson.Result) {
		switch {
		case v.IsObject() || v.IsArray():
			v.ForEach(func(_, e gjson.Result) bool { walk(e); return true })
		case v.Type == gjson.String && len(v.Str) >= 8:
			out = append(out, v.Str)
		}
	}
	walk(gjson.Parse(a.GetCredentialsJson()))
	return out
}

// failoverAccount is the account of a failover attempt: Case.Failover, or
// Account with another id and every credential string suffixed.
func failoverAccount(c *Case) (*pluginv1.Account, error) {
	if c.Failover != nil {
		return c.Failover, nil
	}
	a := proto.Clone(c.Account).(*pluginv1.Account)
	a.Id += 1000
	a.Name += " failover"
	creds := []byte(a.GetCredentialsJson())
	var err error
	gjson.ParseBytes(creds).ForEach(func(k, v gjson.Result) bool {
		if v.Type == gjson.String && v.Str != "" {
			creds, err = sjson.SetBytes(creds, escapeKey(k.Str), v.Str+".failover")
		}
		return err == nil
	})
	a.CredentialsJson = string(creds)
	return a, err
}

// escapeKey makes a JSON key a literal sjson path.
func escapeKey(k string) string {
	r := strings.NewReplacer(".", `\.`, "*", `\*`, "?", `\?`, "|", `\|`, "#", `\#`, "@", `\@`, ":", `\:`)
	return r.Replace(k)
}

// differ names the first field in which two requests differ, "" when they
// are equal.
func differ(a, b *pluginv1.UpstreamRequest) string {
	if proto.Equal(a, b) {
		return ""
	}
	fields := []struct {
		name string
		x, y any
	}{
		{"method", a.GetMethod(), b.GetMethod()},
		{"url", a.GetUrl(), b.GetUrl()},
		{"headers", a.GetHeaders(), b.GetHeaders()},
		{"default_headers", a.GetDefaultHeaders(), b.GetDefaultHeaders()},
		{"body", string(a.GetBody()), string(b.GetBody())},
		{"upstream_model", a.GetUpstreamModel(), b.GetUpstreamModel()},
		{"response_stream", a.GetResponseStream(), b.GetResponseStream()},
	}
	for _, f := range fields {
		// fmt prints maps sorted, and a nil map like an empty one.
		if x, y := fmt.Sprint(f.x), fmt.Sprint(f.y); x != y {
			return fmt.Sprintf("%s: %s vs %s", f.name, clip(x), clip(y))
		}
	}
	return "unknown fields"
}

func clip(s string) string {
	if len(s) > 160 {
		return fmt.Sprintf("%q...(%d bytes)", s[:160], len(s))
	}
	return fmt.Sprintf("%q", s)
}

// paddingKey is the top-level field that brings a body to the size limit.
const paddingKey = "conformance_padding"

// pad returns body grown to at most size bytes with a padding field.
func pad(body []byte, size int) ([]byte, error) {
	if !gjson.ParseBytes(body).IsObject() {
		return nil, errors.New("the large-body rule needs a JSON object body")
	}
	n := size - len(body) - len(`,"":""`) - len(paddingKey)
	if n <= 0 {
		return body, nil
	}
	return sjson.SetBytes(body, paddingKey, strings.Repeat("a", n))
}
