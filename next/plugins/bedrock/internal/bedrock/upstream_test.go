package bedrock

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

const (
	useast1 = "https://bedrock-runtime.us-east-1.amazonaws.com"
	// The model ids of the tests as one path segment.
	haikuSegment = "anthropic.claude-haiku-4-5-20251001-v1%3A0"
	arnSegment   = "arn%3Aaws%3Abedrock%3Aus-east-1%3A123456789012%3Aapplication-inference-profile%2Fabc123def456"
)

// TestInvokeModelURLs covers every shape of model id
// (DESIGN-2026-11-17-CLOUD-PROVIDERS §9.2).
func TestInvokeModelURLs(t *testing.T) {
	for _, tc := range []struct {
		name           string
		region, prefix string
		model          string
		stream         bool
		wantURL        string
		wantModel      string
	}{
		{"base id with a colon", "us-east-1", NoProfilePrefix, testHaiku, false, useast1 + "/model/" + haikuSegment + "/invoke", testHaiku},
		{"base id, streamed", "us-east-1", NoProfilePrefix, testHaiku, true, useast1 + "/model/" + haikuSegment + "/invoke-with-response-stream", testHaiku},
		{"base id without a colon", "us-east-1", NoProfilePrefix, "anthropic.claude-sonnet-4-6", false, useast1 + "/model/anthropic.claude-sonnet-4-6/invoke", "anthropic.claude-sonnet-4-6"},
		{"no region means us-east-1", "", NoProfilePrefix, testHaiku, false, useast1 + "/model/" + haikuSegment + "/invoke", testHaiku},
		{"another region", "ap-northeast-1", NoProfilePrefix, testHaiku, true,
			"https://bedrock-runtime.ap-northeast-1.amazonaws.com/model/" + haikuSegment + "/invoke-with-response-stream", testHaiku},
		{"govcloud region", "us-gov-west-1", NoProfilePrefix, testHaiku, false,
			"https://bedrock-runtime.us-gov-west-1.amazonaws.com/model/" + haikuSegment + "/invoke", testHaiku},
		{"inference profile id", "us-east-1", NoProfilePrefix, "us." + testHaiku, false, useast1 + "/model/us." + haikuSegment + "/invoke", "us." + testHaiku},
		{"no prefix choice means global", "us-east-1", "", testHaiku, false, useast1 + "/model/global." + haikuSegment + "/invoke", "global." + testHaiku},
		{"no prefix choice keeps an id that has one", "us-east-1", "", "us.anthropic.claude-opus-4-1-20250805-v1:0", false,
			useast1 + "/model/us.anthropic.claude-opus-4-1-20250805-v1%3A0/invoke", "us.anthropic.claude-opus-4-1-20250805-v1:0"},
		{"prefix none keeps a global id", "us-east-1", NoProfilePrefix, "global." + testHaiku, false, useast1 + "/model/global." + haikuSegment + "/invoke", "global." + testHaiku},
		{"prefix setting us", "us-east-1", "us", testHaiku, false, useast1 + "/model/us." + haikuSegment + "/invoke", "us." + testHaiku},
		{"prefix setting eu, streamed", "eu-central-1", "eu", testHaiku, true,
			"https://bedrock-runtime.eu-central-1.amazonaws.com/model/eu." + haikuSegment + "/invoke-with-response-stream", "eu." + testHaiku},
		{"prefix setting global", "us-east-1", "global", "anthropic.claude-sonnet-4-6", false,
			useast1 + "/model/global.anthropic.claude-sonnet-4-6/invoke", "global.anthropic.claude-sonnet-4-6"},
		{"prefix setting keeps an id that has one", "us-east-1", "eu", "us." + testHaiku, false, useast1 + "/model/us." + haikuSegment + "/invoke", "us." + testHaiku},
		{"prefix setting in", "ap-south-1", "in", "anthropic.claude-sonnet-5", false,
			"https://bedrock-runtime.ap-south-1.amazonaws.com/model/in.anthropic.claude-sonnet-5/invoke", "in.anthropic.claude-sonnet-5"},
		{"prefix setting keeps an in. id", "us-east-1", "us", "in.anthropic.claude-sonnet-5", false,
			useast1 + "/model/in.anthropic.claude-sonnet-5/invoke", "in.anthropic.claude-sonnet-5"},
		{"prefix setting keeps a us-gov id", "us-gov-west-1", "us", "us-gov." + testHaiku, false,
			"https://bedrock-runtime.us-gov-west-1.amazonaws.com/model/us-gov." + haikuSegment + "/invoke", "us-gov." + testHaiku},
		{"ARN with a slash", "us-east-1", NoProfilePrefix, testARN, false, useast1 + "/model/" + arnSegment + "/invoke", testARN},
		{"ARN, streamed", "us-east-1", NoProfilePrefix, testARN, true, useast1 + "/model/" + arnSegment + "/invoke-with-response-stream", testARN},
		{"prefix setting keeps an ARN", "us-east-1", "us", testARN, false, useast1 + "/model/" + arnSegment + "/invoke", testARN},
		{"id is one segment whatever it holds", "us-east-1", NoProfilePrefix, "my model/v1?x#y", false, useast1 + "/model/my%20model%2Fv1%3Fx%23y/invoke", "my model/v1?x#y"},
		{"surrounding space trimmed", "us-east-1", "apac", "  " + testHaiku + " ", false, useast1 + "/model/apac." + haikuSegment + "/invoke", "apac." + testHaiku},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := build(t, executeRequest(stored(TypeAPIKey, apiKeyCreds, settingsOf(tc.region, tc.prefix)), tc.model, tc.stream, claudeBody(tc.model, tc.stream)))
			if up.GetMethod() != http.MethodPost || up.GetUrl() != tc.wantURL || up.GetUpstreamModel() != tc.wantModel {
				t.Fatalf("request = %s %s (model %s)\nwant POST %s (model %s)", up.GetMethod(), up.GetUrl(), up.GetUpstreamModel(), tc.wantURL, tc.wantModel)
			}
			wantStream := ""
			if tc.stream {
				wantStream = "aws-eventstream-chunk"
			}
			if up.GetResponseStream() != wantStream {
				t.Fatalf("response_stream = %q, want %q", up.GetResponseStream(), wantStream)
			}
			// The model is exactly one path segment: /model/<id>/<action>.
			path := strings.TrimPrefix(up.GetUrl(), "https://")
			if n := strings.Count(path[strings.Index(path, "/"):], "/"); n != 3 || strings.Contains(path, ":") {
				t.Fatalf("path of %s has %d slashes or a raw colon", up.GetUrl(), n)
			}
			// The core parses the URL again before it sends: it must come
			// out byte for byte, or the signature no longer matches.
			if u, err := url.Parse(up.GetUrl()); err != nil || u.String() != up.GetUrl() {
				t.Fatalf("the URL does not survive a parse: %s -> %v (%v)", up.GetUrl(), u, err)
			}
		})
	}
}

// The client's stream choice (meta.stream) decides the action, not a stream
// flag an operator's param_override wrote into a non-stream body.
func TestStreamFollowsMeta(t *testing.T) {
	up := build(t, executeRequest(stored(TypeAPIKey, apiKeyCreds, settingsOf("us-east-1", NoProfilePrefix)), testHaiku, false, claudeBody(testHaiku, true)))
	if up.GetUrl() != useast1+"/model/"+haikuSegment+"/invoke" || up.GetResponseStream() != "" {
		t.Fatalf("a non-stream request with stream:true in its body = %s %q", up.GetUrl(), up.GetResponseStream())
	}
}

func TestInvokeModelWithoutModel(t *testing.T) {
	_, err := newTestPlugin(Endpoints{}).BuildUpstream(context.Background(),
		executeRequest(stored(TypeAPIKey, apiKeyCreds, nil), "  ", false, `{"model":"  ","max_tokens":1,"messages":[]}`))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no model: %v", err)
	}
}

// Only the messages endpoint is served. The OpenAI protocols are not: no
// type declares the openai platform, so the core converts such a request
// into a Messages one instead of asking for an OpenAI route.
func TestUnsupportedProtocol(t *testing.T) {
	for _, protocol := range []string{"", "anthropic.count_tokens", "openai.chat", "openai.responses", "openai.embeddings", "openai.responses_ws", "gemini.generate"} {
		in := executeRequest(stored(TypeAPIKey, apiKeyCreds, nil), testHaiku, false, claudeBody(testHaiku, false))
		in.Meta.Protocol = protocol
		_, err := newTestPlugin(Endpoints{}).BuildUpstream(context.Background(), in)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%q: %v", protocol, err)
		}
	}
}

// Every beta feature InvokeModel accepts passes, in the list's own order,
// from the header as from the body.
func TestSupportedBetasPass(t *testing.T) {
	if got := bedrockBetas(headerBetas(strings.Join(supportedBetas, ","))...); !slices.Equal(got, supportedBetas) {
		t.Fatalf("betas = %v", got)
	}
	if got := bedrockBetas(bodyBetas(gjson.Parse(mustJSON(supportedBetas)))...); !slices.Equal(got, supportedBetas) {
		t.Fatalf("betas = %v", got)
	}
	if len(supportedBetas) != 15 || !slices.Contains(supportedBetas, betaAliases[betaAdvancedToolUse]) || !slices.Contains(supportedBetas, betaContextManagement) {
		t.Fatalf("supported = %v, aliases = %v", supportedBetas, betaAliases)
	}
}

// An id that already names its geography keeps it whatever the account's
// prefix: the prefixes an account can choose, and us-gov.
func TestRegionalPrefixes(t *testing.T) {
	want := []string{"us-gov."}
	for _, prefix := range ProfilePrefixes[1:] {
		want = append(want, prefix+".")
	}
	if ProfilePrefixes[0] != NoProfilePrefix || !slices.Equal(regionalPrefixes, want) || len(regionalPrefixes) != len(ProfilePrefixes) {
		t.Fatalf("regional prefixes = %v, profile prefixes = %v", regionalPrefixes, ProfilePrefixes)
	}
	for _, prefix := range regionalPrefixes {
		if !hasRegionalPrefix(prefix+testHaiku) || hasRegionalPrefix(strings.TrimSuffix(prefix, ".")+testHaiku) {
			t.Errorf("prefix %q is not recognized as one", prefix)
		}
	}
	if hasRegionalPrefix(testHaiku) || hasRegionalPrefix(NoProfilePrefix+"."+testHaiku) {
		t.Error("a plain id counts as an inference profile id")
	}
}

func TestBuildFromBrokenAccount(t *testing.T) {
	p := newTestPlugin(Endpoints{})
	for name, tc := range map[string]struct {
		acc  *pluginv1.Account
		want codes.Code
	}{
		"credentials not JSON": {&pluginv1.Account{Type: TypeAPIKey, CredentialsJson: "{"}, codes.InvalidArgument},
		"settings not JSON":    {&pluginv1.Account{Type: TypeAPIKey, CredentialsJson: mustJSON(apiKeyCreds), SettingsJson: "[1]"}, codes.InvalidArgument},
		"no credentials":       {&pluginv1.Account{Type: TypeAccessKey}, codes.FailedPrecondition},
		"another plugin's":     {stored("apikey", apiKeyCreds, nil), codes.FailedPrecondition},
	} {
		if _, err := p.BuildUpstream(context.Background(), executeRequest(tc.acc, testHaiku, false, claudeBody(testHaiku, false))); status.Code(err) != tc.want {
			t.Errorf("%s: BuildUpstream: %v", name, err)
		}
		if _, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: tc.acc}); status.Code(err) != tc.want {
			t.Errorf("%s: BuildTestRequest: %v", name, err)
		}
	}
}

// received is the test request as an upstream receives it from the host,
// which sends the described request unchanged (account/testreq.go).
func received(t *testing.T, tr *pluginv1.BuildTestRequestResponse) *http.Request {
	t.Helper()
	req, err := http.NewRequest(tr.GetMethod(), tr.GetUrl(), strings.NewReader(tr.GetBodyJson()))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range tr.GetHeaders() {
		req.Header.Set(k, v)
	}
	req.Host = req.URL.Host
	return req
}

func TestBuildTestRequest(t *testing.T) {
	for _, tc := range []struct {
		name           string
		typ            string
		region, prefix string
		model          string
		wantURL        string
		wantModel      string
	}{
		// The default model is a global inference profile id (Haiku 4.5 has
		// no bare id on bedrock-runtime): sent as it is whatever the prefix.
		{"api key, default model", TypeAPIKey, "", "", "", useast1 + "/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"api key, default model, prefix none", TypeAPIKey, "", NoProfilePrefix, "", useast1 + "/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"api key, default model, prefix global", TypeAPIKey, "", "global", "", useast1 + "/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"api key, default model, prefix us", TypeAPIKey, "", "us", "", useast1 + "/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"api key, prefix and a plain model", TypeAPIKey, "eu-west-1", "eu", testHaiku, "https://bedrock-runtime.eu-west-1.amazonaws.com/model/eu." + haikuSegment + "/invoke", "eu." + testHaiku},
		{"api key, prefix none and a plain model", TypeAPIKey, "eu-west-2", NoProfilePrefix, "anthropic.claude-sonnet-5", "https://bedrock-runtime.eu-west-2.amazonaws.com/model/anthropic.claude-sonnet-5/invoke", "anthropic.claude-sonnet-5"},
		{"access key, default model", TypeAccessKey, "", "", "", useast1 + "/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"access key, prefix and model", TypeAccessKey, "us-west-2", "us", "anthropic.claude-sonnet-4-5-20250929-v1:0",
			"https://bedrock-runtime.us-west-2.amazonaws.com/model/us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/invoke", "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{"access key, ARN", TypeAccessKey, "us-east-1", "us", testARN, useast1 + "/model/" + arnSegment + "/invoke", testARN},
		{"temporary credentials, default model", TypeSTS, "ap-southeast-2", "au", "",
			"https://bedrock-runtime.ap-southeast-2.amazonaws.com/model/global." + haikuSegment + "/invoke", DefaultTestModel},
		{"temporary credentials, ARN", TypeSTS, "us-east-1", "", testARN, useast1 + "/model/" + arnSegment + "/invoke", testARN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := newTestPlugin(Endpoints{}).BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
				Account: stored(tc.typ, typeCreds[tc.typ], settingsOf(tc.region, tc.prefix)), Model: tc.model,
			})
			if err != nil {
				t.Fatalf("BuildTestRequest: %v", err)
			}
			if tr.GetMethod() != http.MethodPost || tr.GetUrl() != tc.wantURL || tr.GetModel() != tc.wantModel || tr.GetUsageProtocol() != "anthropic.messages" {
				t.Fatalf("request = %s %s (model %s, usage %s)", tr.GetMethod(), tr.GetUrl(), tr.GetModel(), tr.GetUsageProtocol())
			}
			// The Messages ping without its model, with Bedrock's version.
			body := gjson.Parse(tr.GetBodyJson())
			if !gjson.Valid(tr.GetBodyJson()) || body.Get("model").Exists() || body.Get("stream").Exists() || body.Get("anthropic_version").String() != AnthropicVersion ||
				body.Get("max_tokens").Int() != 1 || body.Get("messages.0.role").String() != "user" || body.Get("messages.0.content").String() != "ping" {
				t.Fatalf("body = %s", tr.GetBodyJson())
			}
			h := tr.GetHeaders()
			if h["content-type"] != "application/json" || h["accept"] != "application/json" {
				t.Fatalf("headers lack content-type / accept")
			}
			if tc.typ == TypeAPIKey {
				if h["authorization"] != "Bearer "+testAPIKey || len(h) != 3 {
					t.Fatalf("an API key request has %d headers", len(h))
				}
				return
			}

			// The signature is recomputed from the request as received.
			req := received(t, tr)
			sig, err := verifySigV4(req, []byte(tr.GetBodyJson()), testNow)
			if err != nil {
				t.Fatalf("signature: %v", err)
			}
			region := tc.region
			if region == "" {
				region = DefaultRegion
			}
			wantSigned, wantKey := "content-type;host;x-amz-date", testAccessKeyID
			if tc.typ == TypeSTS {
				wantSigned, wantKey = "content-type;host;x-amz-date;x-amz-security-token", testSTSKeyID
			}
			if sig.AccessKeyID != wantKey || sig.Region != region || sig.Service != "bedrock" || sig.SignedHeaders != wantSigned {
				t.Fatalf("signature = %+v", sig)
			}
			if h["x-amz-date"] != testNow.UTC().Format("20060102T150405Z") || (h["x-amz-security-token"] == testSessionToken) != (tc.typ == TypeSTS) {
				t.Fatalf("x-amz-date = %s, session token header set = %v", h["x-amz-date"], h["x-amz-security-token"] != "")
			}

			// The signature covers the body, the path and the session token.
			tampered := received(t, tr)
			if _, err := verifySigV4(tampered, []byte(tr.GetBodyJson()+" "), testNow); err == nil {
				t.Fatal("a changed body still verifies")
			}
			tampered = received(t, tr)
			tampered.URL.RawPath = strings.Replace(tampered.URL.EscapedPath(), "/invoke", "/invoke-with-response-stream", 1)
			tampered.URL.Path = strings.Replace(tampered.URL.Path, "/invoke", "/invoke-with-response-stream", 1)
			if _, err := verifySigV4(tampered, []byte(tr.GetBodyJson()), testNow); err == nil {
				t.Fatal("a changed path still verifies")
			}
			if tc.typ == TypeSTS {
				tampered = received(t, tr)
				tampered.Header.Set("X-Amz-Security-Token", testSessionToken+"x")
				if _, err := verifySigV4(tampered, []byte(tr.GetBodyJson()), testNow); err == nil {
					t.Fatal("a changed session token still verifies")
				}
			}
		})
	}
}

// A secret that is not the access key's fails the independent check: the
// test above proves more than "some signature is present".
func TestBuildTestRequestWrongSecret(t *testing.T) {
	tr, err := newTestPlugin(Endpoints{}).BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
		Account: stored(TypeAccessKey, with(akskCreds, map[string]any{fieldSecretAccessKey: "another-secret"}), nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifySigV4(received(t, tr), []byte(tr.GetBodyJson()), testNow); err == nil {
		t.Fatal("a signature made with another secret verifies")
	}
}
