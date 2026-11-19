package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// Client is a thin JSON client for the console API (/api/v1) and the
// gateway. It carries an optional bearer token, and the password that
// answers a step-up (CONTRACTS §80.2) when the session has one
// (WithoutStepUp leaves the refusal to the caller).
type Client struct {
	Base     string
	HTTP     *http.Client
	Token    string
	Password string
}

// NewClient returns an anonymous client for base.
func NewClient(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Resp is a completed HTTP exchange.
type Resp struct {
	Method string
	Path   string
	Status int
	Header http.Header
	Body   []byte
}

// JSON parses the whole body.
func (r *Resp) JSON() gjson.Result { return gjson.ParseBytes(r.Body) }

// Data returns the "data" member of the envelope.
func (r *Resp) Data() gjson.Result { return gjson.GetBytes(r.Body, "data") }

// ErrCode returns error.code of a console error response.
func (r *Resp) ErrCode() string { return gjson.GetBytes(r.Body, "error.code").String() }

func (r *Resp) String() string {
	b := string(r.Body)
	if len(b) > 800 {
		b = b[:800] + "..."
	}
	return fmt.Sprintf("%s %s -> %d %s", r.Method, r.Path, r.Status, b)
}

// ReqOpt customises a request.
type ReqOpt func(*http.Request)

// Header sets a request header.
func Header(k, v string) ReqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// Query adds query parameters.
func Query(kv ...string) ReqOpt {
	return func(r *http.Request) {
		q := r.URL.Query()
		for i := 0; i+1 < len(kv); i += 2 {
			q.Set(kv[i], kv[i+1])
		}
		r.URL.RawQuery = q.Encode()
	}
}

// Do performs a request. body may be nil, []byte, io.Reader or any JSON value.
// path is relative to Base; "/api/v1" is NOT added automatically.
// A step-up refusal is answered with the client's password and the request
// sent again with the proof, unless body is an io.Reader.
func (c *Client) Do(t testing.TB, method, path string, body any, opts ...ReqOpt) *Resp {
	t.Helper()
	var payload []byte
	ctype := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		payload = b
	case io.Reader:
		return c.send(t, method, path, b, ctype, opts)
	default:
		payload, ctype = mustJSON(t, b), "application/json"
	}
	r := c.send(t, method, path, bytes.NewReader(payload), ctype, opts)
	if r.Status != http.StatusForbidden || r.ErrCode() != "verification_required" || c.Password == "" {
		return r
	}
	d := r.JSON().Get("error.details")
	v := c.send(t, http.MethodPost, "/api/v1/me/verify", bytes.NewReader(mustJSON(t, map[string]string{
		"scope": d.Get("scope").String(), "context": d.Get("context").String(), "method": "password", "password": c.Password,
	})), "application/json", nil)
	if v.Status != http.StatusOK {
		t.Fatalf("step-up for %s %s: %s", method, path, v)
	}
	// The session gets a new token of the same session (CONTRACTS §100.4):
	// the old one stops working seconds later.
	if fresh := v.Data().Get("access_token").String(); fresh != "" {
		c.Token = fresh
	}
	opts = append(opts, Header("X-Verify-Proof", v.Data().Get("proof").String()))
	return c.send(t, method, path, bytes.NewReader(payload), ctype, opts)
}

// WithoutStepUp returns a copy of c that answers no step-up, for a case
// that observes the verification_required refusal itself.
func (c *Client) WithoutStepUp() *Client {
	cp := *c
	cp.Password = ""
	return &cp
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// send performs one request.
func (c *Client) send(t testing.TB, method, path string, rd io.Reader, ctype string, opts []ReqOpt) *Resp {
	t.Helper()
	r, err := c.roundTrip(method, path, rd, ctype, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Try performs one request with a JSON body (no step-up) and returns the
// failure instead of failing the test: for a goroutine, which must not
// call t.Fatal.
func (c *Client) Try(method, path string, body any, opts ...ReqOpt) (*Resp, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.roundTrip(method, path, bytes.NewReader(payload), "application/json", opts)
}

func (c *Client) roundTrip(method, path string, rd io.Reader, ctype string, opts []ReqOpt) (*Resp, error) {
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read body: %w", method, path, err)
	}
	return &Resp{Method: method, Path: req.URL.RequestURI(), Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// API performs a console request against /api/v1 + path.
func (c *Client) API(t testing.TB, method, path string, body any, opts ...ReqOpt) *Resp {
	t.Helper()
	return c.Do(t, method, "/api/v1"+path, body, opts...)
}

// OK performs a console request, requires a 2xx status and returns "data".
func (c *Client) OK(t testing.TB, method, path string, body any, opts ...ReqOpt) gjson.Result {
	t.Helper()
	r := c.API(t, method, path, body, opts...)
	if r.Status < 200 || r.Status > 299 {
		t.Fatalf("expected 2xx: %s", r)
	}
	return r.Data()
}

// Expect performs a console request and requires the given status (and, when
// code != "", the error code).
func (c *Client) Expect(t testing.TB, status int, code, method, path string, body any, opts ...ReqOpt) *Resp {
	t.Helper()
	r := c.API(t, method, path, body, opts...)
	if r.Status != status {
		t.Fatalf("expected HTTP %d: %s", status, r)
	}
	if code != "" && r.ErrCode() != code {
		t.Fatalf("expected error code %q: %s", code, r)
	}
	return r
}

// Upload posts a multipart form with a single file field.
func (c *Client) Upload(t testing.TB, path, field, filename string, data []byte, opts ...ReqOpt) *Resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	opts = append([]ReqOpt{Header("Content-Type", mw.FormDataContentType())}, opts...)
	return c.API(t, http.MethodPost, path, buf.Bytes(), opts...)
}

// ListAll fetches every page of a list endpoint (page_size 200).
func (c *Client) ListAll(t testing.TB, path string, kv ...string) []gjson.Result {
	t.Helper()
	var out []gjson.Result
	for page := 1; ; page++ {
		q := append([]string{"page", fmt.Sprint(page), "page_size", "200"}, kv...)
		r := c.API(t, http.MethodGet, path, nil, Query(q...))
		if r.Status != 200 {
			t.Fatalf("list: %s", r)
		}
		items := r.Data().Array()
		out = append(out, items...)
		total := r.JSON().Get("page.total")
		if !total.Exists() || len(items) == 0 || int64(len(out)) >= total.Int() {
			return out
		}
	}
}

// Find returns the first element whose field equals value.
func Find(items []gjson.Result, field string, value any) (gjson.Result, bool) {
	want := fmt.Sprint(value)
	for _, it := range items {
		if it.Get(field).String() == want {
			return it, true
		}
	}
	return gjson.Result{}, false
}

// Strings returns the elements of a JSON array as strings.
func Strings(r gjson.Result) []string {
	out := []string{}
	for _, v := range r.Array() {
		out = append(out, v.String())
	}
	return out
}

// Set returns the elements of a JSON array as a set of strings.
func Set(r gjson.Result) map[string]bool {
	out := map[string]bool{}
	for _, v := range Strings(r) {
		out[v] = true
	}
	return out
}

// PathEscape escapes one path segment.
func PathEscape(s string) string { return url.PathEscape(s) }
