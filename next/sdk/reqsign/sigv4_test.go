package reqsign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/logging"
)

const (
	suiteAccessKey = "AKIDEXAMPLE"
	suiteSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

// signingLog collects what the SDK signer logs with LogSigning: the
// canonical request and the string to sign of the signature it made.
type signingLog struct{ canonical, stringToSign string }

func (l *signingLog) Logf(_ logging.Classification, format string, v ...any) {
	if len(v) >= 2 {
		l.canonical, l.stringToSign = fmt.Sprint(v[0]), fmt.Sprint(v[1])
	}
}

// signTraced signs with SignAWSSigV4 and returns what was signed, which the signer
// logs when sigV4SignerOptions asks it to.
func signTraced(t *testing.T, req *http.Request, payload []byte, c AWSCredentials, now time.Time) signingLog {
	t.Helper()
	var log signingLog
	sigV4SignerOptions = []func(*v4.SignerOptions){func(o *v4.SignerOptions) { o.Logger, o.LogSigning = &log, true }}
	defer func() { sigV4SignerOptions = nil }()
	if err := SignAWSSigV4(req, payload, c, now); err != nil {
		t.Fatal(err)
	}
	if log.canonical == "" || log.stringToSign == "" {
		t.Fatal("the signer logged nothing")
	}
	return log
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// suiteRequest parses a .req file of AWS's test suite: request line, headers
// up to an empty line, then the body.
func suiteRequest(t *testing.T, raw string) (req *http.Request, payload []byte, token string) {
	t.Helper()
	head, body, _ := strings.Cut(raw, "\n\n")
	lines := strings.Split(head, "\n")
	method, rest, _ := strings.Cut(lines[0], " ")
	target := strings.TrimSuffix(rest, " HTTP/1.1")
	req, err := http.NewRequest(method, "https://placeholder"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines[1:] {
		name, value, _ := strings.Cut(line, ":")
		switch strings.ToLower(name) {
		case "host":
			req.Host = value
		case "x-amz-date": // written by the signer from the clock
		case "x-amz-security-token":
			token = value
		default:
			req.Header.Add(name, value)
		}
	}
	return req, []byte(body), token
}

// TestOfficialSuite replays AWS's published SigV4 test suite: the canonical
// request, the string to sign and the Authorization header must all match.
func TestOfficialSuite(t *testing.T) {
	dirs, err := filepath.Glob("testdata/aws-sig-v4-test-suite/*")
	if err != nil || len(dirs) < 12 {
		t.Fatalf("suite cases: %d %v", len(dirs), err)
	}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	for _, dir := range dirs {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			file := func(ext string) string { return readFile(t, filepath.Join(dir, name+"."+ext)) }
			req, payload, token := suiteRequest(t, file("req"))
			d := AWSCredentials{Region: "us-east-1", Service: "service",
				AccessKeyID: suiteAccessKey, SecretAccessKey: suiteSecretKey, SessionToken: token}
			log := signTraced(t, req, payload, d, now)
			if want := file("creq"); log.canonical != want {
				t.Errorf("canonical request\n got: %q\nwant: %q", log.canonical, want)
			}
			if want := file("sts"); log.stringToSign != want {
				t.Errorf("string to sign\n got: %q\nwant: %q", log.stringToSign, want)
			}
			if got, want := req.Header.Get("Authorization"), file("authz"); got != want {
				t.Errorf("authorization\n got: %s\nwant: %s", got, want)
			}
			if req.Header.Get("X-Amz-Date") != "20150830T123600Z" || req.Header.Get("X-Amz-Security-Token") != token {
				t.Errorf("headers %v", req.Header)
			}
		})
	}
}

type sdkVector struct {
	Name          string            `json:"name"`
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	Region        string            `json:"region"`
	Service       string            `json:"service"`
	AccessKeyID   string            `json:"access_key_id"`
	SecretKey     string            `json:"secret_access_key"`
	SessionToken  string            `json:"session_token"`
	Time          time.Time         `json:"time"`
	Authorization string            `json:"authorization"`
	AmzDate       string            `json:"x_amz_date"`
}

func sdkVectors(t *testing.T) []sdkVector {
	t.Helper()
	var vs []sdkVector
	if err := json.Unmarshal([]byte(readFile(t, "testdata/aws-sdk-go-v2.json")), &vs); err != nil || len(vs) < 8 {
		t.Fatalf("vectors: %d %v", len(vs), err)
	}
	return vs
}

func (v sdkVector) request(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(v.Method, v.URL, strings.NewReader(v.Body))
	if err != nil {
		t.Fatal(err)
	}
	for k, val := range v.Headers {
		req.Header.Set(k, val)
	}
	return req
}

func (v sdkVector) credentials() AWSCredentials {
	return AWSCredentials{Region: v.Region, Service: v.Service,
		AccessKeyID: v.AccessKeyID, SecretAccessKey: v.SecretKey, SessionToken: v.SessionToken}
}

// TestSDKRecordings compares with what aws-sdk-go-v2 produced for
// Bedrock-shaped requests (testdata/README.md).
func TestSDKRecordings(t *testing.T) {
	for _, v := range sdkVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			req := v.request(t)
			log := signTraced(t, req, []byte(v.Body), v.credentials(), v.Time)
			if got := req.Header.Get("Authorization"); got != v.Authorization {
				t.Errorf("authorization\n got: %s\nwant: %s\ncanonical request:\n%s", got, v.Authorization, log.canonical)
			}
			if req.Header.Get("X-Amz-Date") != v.AmzDate || req.Header.Get("X-Amz-Security-Token") != v.SessionToken {
				t.Errorf("headers %v", req.Header)
			}
		})
	}
}

// The wire path keeps ":" as "%3A" and the canonical path escapes it again.
func TestCanonicalPathIsEscapedTwice(t *testing.T) {
	v := sdkVectors(t)[0]
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v1%3A0/invoke", nil)
	log := signTraced(t, req, nil, v.credentials(), v.Time)
	if got := strings.Split(log.canonical, "\n")[1]; got != "/model/anthropic.claude-v1%253A0/invoke" {
		t.Fatalf("canonical path %q", got)
	}
	if got := req.URL.EscapedPath(); got != "/model/anthropic.claude-v1%3A0/invoke" {
		t.Fatalf("wire path %q", got)
	}
}

// The query is signed sorted by name, then value, both as they are encoded,
// and sent exactly as signed.
func TestCanonicalQuery(t *testing.T) {
	v := sdkVectors(t)[0]
	for raw, want := range map[string]string{
		"":                         "",
		"b=2&a=1":                  "a=1&b=2",
		"p=b&p=a&p=B&a=z":          "a=z&p=B&p=a&p=b",
		"a-=1&a=2":                 "a=2&a-=1", // by key, not by "key=value"
		"q=a+b&r=a%20b":            "q=a%20b&r=a%20b",
		"flag&k=":                  "flag=&k=",
		"x=%E2%9C%93&y=a%2Fb%3Ac~": "x=%E2%9C%93&y=a%2Fb%3Ac~",
		"x=a/b:c*":                 "x=a%2Fb%3Ac%2A",
		"v=a-b&v=a/b":              "v=a%2Fb&v=a-b", // sorted after encoding: "%" < "-" < "/"
	} {
		req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/?"+raw, nil)
		log := signTraced(t, req, nil, v.credentials(), v.Time)
		if got := strings.Split(log.canonical, "\n")[2]; got != want || req.URL.RawQuery != want {
			t.Errorf("%q: signed %q, sent %q, want %q", raw, got, req.URL.RawQuery, want)
		}
		if !strings.HasSuffix(req.URL.RequestURI(), "?"+want) && want != "" {
			t.Errorf("%q: request URI %q", raw, req.URL.RequestURI())
		}
	}
	// A query that cannot be parsed whole is refused, not signed without the
	// parameters net/url drops; the request is left as it was.
	for _, raw := range []string{"a=1;b=2", "a=1&b=%zz", "%=1", "a=%2"} {
		req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
		req.URL.RawQuery = raw
		err := SignAWSSigV4(req, nil, v.credentials(), v.Time)
		if err == nil {
			t.Errorf("%q: signed", raw)
		}
		if req.URL.RawQuery != raw || req.Host != "example.amazonaws.com" || len(req.Header) != 0 {
			t.Errorf("%q: request touched: %q %q %v", raw, req.URL.RawQuery, req.Host, req.Header)
		}
	}
}

// The scheme's default port is neither signed nor sent: Go would otherwise
// send "Host: h:443" under a signature made for "h".
func TestHostSentIsHostSigned(t *testing.T) {
	v := sdkVectors(t)[0]
	for _, tc := range []struct{ url, host, want string }{
		{"https://example.amazonaws.com:443/x", "", "example.amazonaws.com"},
		{"http://example.amazonaws.com:80/x", "", "example.amazonaws.com"},
		{"https://example.amazonaws.com/x", "", "example.amazonaws.com"},
		{"https://example.amazonaws.com:8443/x", "", "example.amazonaws.com:8443"},
		{"https://example.amazonaws.com:80/x", "", "example.amazonaws.com:80"},
		{"http://example.amazonaws.com:443/x", "", "example.amazonaws.com:443"},
		{"https://[::1]:8443/x", "", "[::1]:8443"},
		// The SDK signer takes the brackets off with the default port; the
		// host sent is still the host signed.
		{"https://[::1]:443/x", "", "::1"},
		{"https://10.0.0.1:443/x", "other.example.com:443", "other.example.com"},
		{"https://10.0.0.1/x", "other.example.com:8443", "other.example.com:8443"},
	} {
		req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
		req.Host = tc.host
		log := signTraced(t, req, nil, v.credentials(), v.Time)
		if !strings.Contains(log.canonical, "\nhost:"+tc.want+"\n") {
			t.Errorf("%s: signed\n%s", tc.url, log.canonical)
		}
		var wire bytes.Buffer
		if err := req.Write(&wire); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(wire.String(), "\r\nHost: "+tc.want+"\r\n") {
			t.Errorf("%s: sent\n%s", tc.url, wire.String())
		}
		// Where the request connects to is not changed.
		if u, _ := http.NewRequest(http.MethodGet, tc.url, nil); req.URL.Host != u.URL.Host {
			t.Errorf("%s: URL host %q", tc.url, req.URL.Host)
		}
	}
}

func signed(t *testing.T, v sdkVector, edit func(*http.Request, *[]byte, *AWSCredentials, *time.Time)) string {
	t.Helper()
	req, payload, d, now := v.request(t), []byte(v.Body), v.credentials(), v.Time
	if edit != nil {
		edit(req, &payload, &d, &now)
	}
	if err := SignAWSSigV4(req, payload, d, now); err != nil {
		t.Fatal(err)
	}
	return req.Header.Get("Authorization")
}

// What is signed changes the signature; what is not signed does not.
func TestSignatureCoverage(t *testing.T) {
	v := sdkVectors(t)[0]
	base := signed(t, v, nil)
	if base != v.Authorization {
		t.Fatalf("base %s", base)
	}
	changes := map[string]func(*http.Request, *[]byte, *AWSCredentials, *time.Time){
		"one body byte": func(_ *http.Request, p *[]byte, _ *AWSCredentials, _ *time.Time) { (*p)[len(*p)-1] ^= 1 },
		"empty body":    func(_ *http.Request, p *[]byte, _ *AWSCredentials, _ *time.Time) { *p = nil },
		"content-type": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) {
			r.Header.Set("Content-Type", "text/plain")
		},
		"host": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) { r.Host = "other.example.com" },
		"path": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) {
			r.URL.Path, r.URL.RawPath = "/model/x/invoke", ""
		},
		"query":            func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) { r.URL.RawQuery = "a=1" },
		"method":           func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) { r.Method = http.MethodPut },
		"time":             func(_ *http.Request, _ *[]byte, _ *AWSCredentials, n *time.Time) { *n = n.Add(time.Second) },
		"region":           func(_ *http.Request, _ *[]byte, d *AWSCredentials, _ *time.Time) { d.Region = "eu-west-1" },
		"service":          func(_ *http.Request, _ *[]byte, d *AWSCredentials, _ *time.Time) { d.Service = "s3" },
		"secret":           func(_ *http.Request, _ *[]byte, d *AWSCredentials, _ *time.Time) { d.SecretAccessKey += "x" },
		"session token":    func(_ *http.Request, _ *[]byte, d *AWSCredentials, _ *time.Time) { d.SessionToken = "tok" },
		"request.Host set": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) { r.Host = "h.example.com:8443" },
	}
	for name, edit := range changes {
		if got := signed(t, v, edit); got == base {
			t.Errorf("%s: signature unchanged", name)
		}
	}
	same := map[string]func(*http.Request, *[]byte, *AWSCredentials, *time.Time){
		// Headers the SDK signer would sign if it were handed them.
		"unsigned headers": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) {
			r.Header.Set("Accept", "application/json")
			r.Header.Set("User-Agent", "x")
			r.Header.Set("X-Amzn-Bedrock-Trace", "ENABLED")
			r.Header.Set("X-Amz-Target", "x")
			r.Header.Set("Content-Length", "7")
			r.Header.Set("Accept-Encoding", "gzip")
			r.ContentLength = 7
		},
		"stale credential headers": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) {
			r.Header.Set("Authorization", "Bearer stale")
			r.Header.Set("X-Amz-Date", "20000101T000000Z")
			r.Header.Set("X-Amz-Security-Token", "stale")
		},
		"header value spacing": func(r *http.Request, _ *[]byte, _ *AWSCredentials, _ *time.Time) {
			r.Header.Set("Content-Type", "  application/json ")
		},
		"local time zone": func(_ *http.Request, _ *[]byte, _ *AWSCredentials, n *time.Time) {
			*n = n.In(time.FixedZone("x", 8*3600))
		},
	}
	for name, edit := range same {
		if got := signed(t, v, edit); got != base {
			t.Errorf("%s: signature changed", name)
		}
	}
}

// SignAWSSigV4 replaces credentials an earlier layer wrote and removes a session
// token header the credentials do not carry.
func TestSignReplacesCredentialHeaders(t *testing.T) {
	v := sdkVectors(t)[0]
	req := v.request(t)
	req.Header.Set("Authorization", "Bearer stale")
	req.Header.Set("X-Amz-Security-Token", "stale")
	if err := SignAWSSigV4(req, []byte(v.Body), v.credentials(), v.Time); err != nil {
		t.Fatal(err)
	}
	if len(req.Header.Values("Authorization")) != 1 || !strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") ||
		req.Header.Get("X-Amz-Security-Token") != "" {
		t.Fatalf("headers %v", req.Header)
	}
}

// AWSSigV4Headers returns what the signature wrote or covers, nothing else
// the request carries.
func TestAWSSigV4Headers(t *testing.T) {
	v := sdkVectors(t)[0]
	d := v.credentials()
	d.SessionToken = "session"
	req := v.request(t)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Client", "c")
	if err := SignAWSSigV4(req, []byte(v.Body), d, v.Time); err != nil {
		t.Fatal(err)
	}
	got := AWSSigV4Headers(req)
	want := http.Header{"Content-Type": {"application/json"}, "Authorization": req.Header.Values("Authorization"),
		"X-Amz-Date": req.Header.Values("X-Amz-Date"), "X-Amz-Security-Token": {"session"}}
	if fmt.Sprint(got) != fmt.Sprint(want) || len(got["Authorization"]) != 1 {
		t.Fatalf("AWSSigV4Headers = %v\nwant %v", got, want)
	}
	req.Header.Del("Content-Type")
	req.Header.Del("X-Amz-Security-Token")
	if got := AWSSigV4Headers(req); len(got) != 2 {
		t.Fatalf("without content type and token: %v", got)
	}
}

func TestSignErrors(t *testing.T) {
	v := sdkVectors(t)[0]
	const secret, token = "s3cr3t-value", "t0ken-value"
	for name, d := range map[string]AWSCredentials{
		"no region":     {Service: "s", AccessKeyID: "AK", SecretAccessKey: secret, SessionToken: token},
		"no service":    {Region: "r", AccessKeyID: "AK", SecretAccessKey: secret, SessionToken: token},
		"no access key": {Region: "r", Service: "s", SecretAccessKey: secret, SessionToken: token},
		"no secret":     {Region: "r", Service: "s", AccessKeyID: "AK", SessionToken: token},
	} {
		req := v.request(t)
		req.Header.Set("Authorization", "Bearer kept")
		err := SignAWSSigV4(req, []byte(v.Body), d, v.Time)
		if err == nil {
			t.Errorf("%s: signed", name)
			continue
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), token) {
			t.Errorf("%s: error leaks a secret: %v", name, err)
		}
		if req.Header.Get("Authorization") != "Bearer kept" || req.Header.Get("X-Amz-Date") != "" {
			t.Errorf("%s: request touched: %v", name, req.Header)
		}
	}
	if err := SignAWSSigV4(nil, nil, v.credentials(), v.Time); err == nil {
		t.Error("nil request signed")
	}
}

// Credentials that end up in a log line or an error by accident show
// neither the secret nor the session token.
func TestCredentialsAreRedacted(t *testing.T) {
	d := AWSCredentials{Region: "us-east-1", Service: "bedrock", AccessKeyID: "AKIDEXAMPLE",
		SecretAccessKey: "s3cr3t-value", SessionToken: "t0ken-value"}
	var buf strings.Builder
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("x", "credentials", d, "pointer", &d)
	out := buf.String() + fmt.Sprintf("%v %+v %#v %s %v", d, d, d, d, &d) + fmt.Errorf("wrap: %v", d).Error()
	if strings.Contains(out, "s3cr3t-value") || strings.Contains(out, "t0ken-value") {
		t.Fatalf("leak: %s", out)
	}
	if !strings.Contains(out, "AKIDEXAMPLE") || !strings.Contains(out, "bedrock") {
		t.Fatalf("not descriptive: %s", out)
	}
}
