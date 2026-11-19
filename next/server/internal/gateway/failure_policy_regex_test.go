package gateway

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func regexTestPolicy(patterns ...string) FailurePolicy {
	p := defaultFailurePolicy()
	p.RetryEnabled, p.AccountEnabled = true, true
	m := FailureMatch{Source: "upstream", Keywords: patterns, KeywordRegex: true, Mode: "all"}
	p.RetryRules = []FailureRule{{ID: "retry", Name: "Retry", Match: m}}
	p.AccountRules = []FailureRule{{ID: "disable", Name: "Disable", Match: m}}
	return p
}

func TestFailurePolicyGoRegex(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		text     string
		want     bool
	}{
		{"first line", []string{`^quota \d+$`, `(?i)permission\s+denied`}, "quota 42", true},
		{"second line", []string{`^quota \d+$`, `(?i)permission\s+denied`}, "PERMISSION denied", true},
		{"no line", []string{`^quota \d+$`, `(?i)permission\s+denied`}, "other", false},
		{"Go case", []string{`^QUOTA$`}, "quota", false},
		{"Go uppercase escape", []string{`^\D+$`}, "ABC", true},
		{"Go Unicode", []string{`^\p{Han}+$`}, "额度不足", true},
		{"Go named capture", []string{`(?P<error>quota)\s+\d+`}, "quota 42", true},
		{"spaces preserved", []string{` quota `}, "quota", false},
		{"prefix bounded", []string{`quota`}, strings.Repeat("x", core.ClassifyPrefixBytes) + "quota", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := regexTestPolicy(tc.patterns...)
			require.Empty(t, p.validate(t.Context()))
			d := p.decide(FailureFacts{Source: "upstream", Status: 503, Text: tc.text})
			require.Equal(t, tc.want, d.Action == failureRetry)
			require.Equal(t, tc.want, d.AccountAction == "disable")
			raw, err := json.Marshal(p)
			require.NoError(t, err)
			var loaded FailurePolicy
			require.NoError(t, json.Unmarshal(raw, &loaded))
			require.Empty(t, loaded.validate(t.Context()))
			require.Equal(t, d, loaded.decide(FailureFacts{Source: "upstream", Status: 503, Text: tc.text}))
		})
	}
}

func TestFailurePolicyRegexValidation(t *testing.T) {
	for _, pattern := range []string{`[`, `(?=quota)`, `(quota)\1`, strings.Repeat("a", 201)} {
		p := regexTestPolicy("quota", "", pattern)
		fields := p.validate(t.Context())
		require.NotEmpty(t, fields)
		require.Equal(t, "retry_rules.0.match.keywords", fields[0].Field)
		require.Contains(t, fields[0].Message, "3")
	}
	p := regexTestPolicy("quota\r\n(?i)Denied", "", "quota")
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, []string{"quota", "(?i)Denied"}, p.RetryRules[0].Match.Keywords)
	require.Len(t, p.RetryRules[0].Match.patterns, 2)
	require.Empty(t, p.validate(t.Context()))
	require.Len(t, p.RetryRules[0].Match.patterns, 2, "revalidation must replace compiled patterns")
	p.SchemaVersion = 3
	require.NotEmpty(t, p.validate(t.Context()), "old schemas cannot silently accept regex rules")
}

// With status codes and keywords both given, a Schema 3/4 rule matches only
// when the status and a keyword both match, whatever mode was stored; one
// condition alone matches on that one (2026-11-01, was OR).
func TestFailurePolicyStatusAndKeywordBothMatch(t *testing.T) {
	p := regexTestPolicy(`quota`)
	p.RetryRules[0].Match.StatusCodes = "429"
	p.RetryRules[0].Match.Mode = "any"
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, "all", p.RetryRules[0].Match.Mode)
	require.Equal(t, failureRetry, p.decide(FailureFacts{Source: "upstream", Status: 429, Text: "quota"}).Action)
	for _, facts := range []FailureFacts{
		{Source: "upstream", Status: 429, Text: "unrelated"},
		{Source: "upstream", Status: 503, Text: "quota"},
	} {
		require.Equal(t, failureReturn, p.decide(facts).Action)
	}
	p.RetryRules[0].Match.KeywordRegex = false
	p.RetryRules[0].Match.StatusCodes = ""
	p.RetryRules[0].Match.Keywords = []string{`[QUOTA]`}
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, failureRetry, p.decide(FailureFacts{Source: "upstream", Status: 503, Text: "[quota]"}).Action)
	require.Equal(t, failureReturn, p.decide(FailureFacts{Source: "upstream", Status: 503, Text: "q"}).Action)
	p.SchemaVersion = 3
	p.AccountRules = nil
	p.RetryRules[0].Match.StatusCodes = "429"
	p.RetryRules[0].Match.Mode = "any"
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, failureReturn, p.decide(FailureFacts{Source: "upstream", Status: 429, Text: "unrelated"}).Action)
	p.RetryRules[0].Match.Keywords = nil
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, failureRetry, p.decide(FailureFacts{Source: "upstream", Status: 429, Text: "unrelated"}).Action)
}

// An account rule disables only with a matched keyword when it has keywords,
// and the account's reason names the keyword.
func TestFailurePolicyDisableNamesTheMatchedKeyword(t *testing.T) {
	e := newEnv(t)
	p := simpleTestPolicy()
	p.RetryEnabled, p.AccountEnabled = false, true
	p.AccountRules = []FailureRule{{ID: "migrated-disable", Name: "Disable", Enabled: true, Action: "disable",
		Match: FailureMatch{Source: "upstream", StatusCodes: "401", Keywords: []string{"Permission Denied"}, Mode: "any"}}}
	e.setFailurePolicy(p)
	e.up.set("acc-1", &upstreamRule{status: 401, body: `{"error":{"type":"authentication_error","message":"invalid token"}}`})
	require.Equal(t, 401, e.messages(body(testModel, false)).status)
	require.NotContains(t, e.accounts.disabled, int64(1), "a 401 without a configured keyword must not disable")
	e.up.set("acc-1", &upstreamRule{status: 401, body: `{"error":{"type":"permission_error","message":"PERMISSION DENIED for this org"}}`})
	require.Equal(t, 401, e.messages(body(testModel, false)).status)
	require.Equal(t, `failure policy migrated-disable: upstream HTTP 401, keyword "permission denied"`, e.accounts.disabled[1])
}

func TestFailurePolicyRegexDrivesRetryAndDisable(t *testing.T) {
	e := newEnv(t)
	p := regexTestPolicy(`not present`, `(?i)quota\s+\d+`)
	p.BackoffMS = 0
	e.setFailurePolicy(p)
	e.up.set("acc-1", &upstreamRule{status: 403, body: `{"error":{"message":"QUOTA 42"}}`})
	require.Equal(t, 200, e.messages(body(testModel, false)).status)
	require.Equal(t, []string{"acc-1", "acc-2"}, e.up.keys())
	require.Contains(t, e.accounts.disabled, int64(1))
}

func TestFailurePolicyRegexPreviewAndPublishValidation(t *testing.T) {
	g := &Gateway{}
	p := regexTestPolicy(`not present`, `(?i)quota\s+\d+`)
	invoke := func(payload any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		return w
	}
	w := invoke(map[string]any{"policy": p, "facts": FailureFacts{Source: "upstream", Status: 403, Text: "QUOTA 42"}}, g.previewFailurePolicy)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"account_action":"disable"`)
	p.RetryRules[0].Match.Keywords = []string{"valid", "", "["}
	w = invoke(map[string]any{"policy": p}, g.previewFailurePolicy)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "line 3")
	w = invoke(publishFailurePolicy{Policy: p}, g.putFailurePolicy)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "retry_rules.0.match.keywords")
}
