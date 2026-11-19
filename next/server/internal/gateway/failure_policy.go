package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const failurePolicyKey = protocol.SettingsFailurePolicy

const (
	failureReturn = "return"
	failureRetry  = "retry"
	failureSwitch = "switch"
	failureReset  = "switch_reset"
)

// FailurePolicy is owned and executed by the core. No plugin RPC or manifest
// participates in evaluating these rules. Slice order is rule priority.
type FailurePolicy struct {
	Revision          int64         `json:"revision"`
	SchemaVersion     int           `json:"schema_version"`
	RetryEnabled      bool          `json:"retry_enabled"`
	MaxRetries        int           `json:"max_retries"`
	RetriesPerAccount int           `json:"retries_per_account"`
	SwitchAccount     bool          `json:"switch_account"`
	ResetSticky       bool          `json:"reset_sticky"`
	Egress            string        `json:"egress"`
	BackoffMS         int           `json:"backoff_ms"`
	MaxWaitMS         int           `json:"max_wait_ms"`
	RespectRetryAfter bool          `json:"respect_retry_after"`
	AccountEnabled    bool          `json:"account_enabled"`
	RetryRules        []FailureRule `json:"retry_rules"`
	AccountRules      []FailureRule `json:"account_rules"`
}

// JSON null is an empty form field, not an explicit zero retry/wait budget.
func (p *FailurePolicy) UnmarshalJSON(raw []byte) error {
	type plain FailurePolicy
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, key := range []string{"max_retries", "retries_per_account", "backoff_ms", "max_wait_ms"} {
		if value.SchemaVersion >= 3 && (key == "max_wait_ms" || key == "retries_per_account" && !value.SwitchAccount) {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return fmt.Errorf("%s must be an integer, not null", key)
		}
	}
	*p = FailurePolicy(value)
	return nil
}

type FailureRule struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Enabled            bool         `json:"enabled"`
	Match              FailureMatch `json:"match"`
	Action             string       `json:"action"`
	SameAccountRetries int          `json:"same_account_retries"`
	ExhaustedAction    string       `json:"exhausted_action"`
	Egress             string       `json:"egress"`
	CooldownSeconds    int          `json:"cooldown_seconds"`
	// NoSameAccountRetry turns "retry on the same account" off for a retry rule
	// of a unified plan: its errors skip retries_per_account and go straight to
	// the plan's next step. Off the default, so a stored rule keeps its behaviour.
	NoSameAccountRetry bool `json:"no_same_account_retry,omitempty"`
}

type FailureMatch struct {
	Source       string   `json:"source"` // upstream, transport, core
	StatusCodes  string   `json:"status_codes"`
	Keywords     []string `json:"keywords"`
	KeywordRegex bool     `json:"keyword_regex,omitempty"`
	Mode         string   `json:"mode"` // all or any of the nonempty error conditions
	Groups       []int64  `json:"groups"`
	Models       []string `json:"models"`
	Protocols    []string `json:"protocols"`
	ranges       []statusRange
	patterns     []*regexp.Regexp
}

type FailureFacts struct {
	Source   string `json:"source"`
	Status   int    `json:"status"`
	Text     string `json:"text"`
	GroupID  int64  `json:"group_id"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

type FailureDecision struct {
	RetryRule          string `json:"retry_rule,omitempty"`
	AccountRule        string `json:"account_rule,omitempty"`
	AccountKeyword     string `json:"account_keyword,omitempty"` // the account rule's matched keyword, "" when status alone matched
	Action             string `json:"action"`
	AccountAction      string `json:"account_action"`
	SameAccountRetries int    `json:"same_account_retries,omitempty"`
	ExhaustedAction    string `json:"exhausted_action,omitempty"`
	Egress             string `json:"egress,omitempty"`
	CooldownSeconds    int    `json:"cooldown_seconds,omitempty"`
}

func defaultFailurePolicy() FailurePolicy {
	return FailurePolicy{SchemaVersion: 4, MaxRetries: 2, SwitchAccount: true, ResetSticky: true, Egress: "account_configured", BackoffMS: 200,
		RetryRules: []FailureRule{}, AccountRules: []FailureRule{}}
}

// loginAccountRules disable an account on a login failure the provider
// names (401) and on an account the provider holds (403). Migrations 0072
// and 0074 wrote these rules into a published policy in place of the
// migrated rule, with keywords only CCGateway produced besides (its Worker's
// codes and Claude Code's own messages), which 0075 extended and 0102 took
// out again.
func loginAccountRules() []FailureRule {
	rule := func(id, name, status string, keywords ...string) FailureRule {
		return FailureRule{ID: id, Name: name, Enabled: true, Action: "disable",
			Match: FailureMatch{Source: "upstream", StatusCodes: status, Keywords: keywords, Mode: "all"}}
	}
	return []FailureRule{
		rule("login-invalid", "Login invalid (401)", "401",
			"oauth access token has been revoked",
			"oauth token has been revoked",
			"invalid x-api-key",
			"invalid bearer token",
			"the security token included in the request is invalid",
			"your account is not authorized"),
		rule("account-on-hold", "Account on hold (403)", "403",
			"oauth authentication is currently not allowed for this organization"),
	}
}

// defaultRetryRules are the retry rules of a new policy. A 429 is retried
// only when another account can help: an account out of quota. Any other 429,
// such as a request that needs usage credits, would get the same answer from
// every account, so it goes back to the client.
func defaultRetryRules() []FailureRule {
	return []FailureRule{
		{ID: "http-transient", Name: "HTTP transient errors", Enabled: true,
			Match:  FailureMatch{Source: "upstream", StatusCodes: "408,500-599", Mode: "all"},
			Action: failureRetry},
		{ID: "rate-limit", Name: "Account quota (429)", Enabled: true,
			Match:  FailureMatch{Source: "upstream", StatusCodes: "429", Keywords: []string{"exceed your account's rate limit"}, Mode: "all"},
			Action: failureRetry},
	}
}

// migrationDraft is a visible starting point, not an executable replacement
// until published. Legacy plugin-specific decisions are intentionally not copied.
// The old settings disabled on a status code or a keyword; a rule needs both
// now, so the draft names login failures precisely, keeps the old keywords
// at any status and keeps status codes other than the default 401 alone.
func migrationDraft(gw GatewaySettings, ad AutoDisableSettings) FailurePolicy {
	p := defaultFailurePolicy()
	p.MaxRetries = gw.normalized().MaxAttempts - 1
	p.RetryEnabled = p.MaxRetries > 0
	p.AccountEnabled = ad.Enabled
	p.RetryRules = defaultRetryRules()
	p.AccountRules = loginAccountRules()
	if ad.StatusCodes != "" && ad.StatusCodes != "401" {
		p.AccountRules = append(p.AccountRules, FailureRule{ID: "migrated-status", Name: "Existing automatic disable (status)", Enabled: true,
			Match: FailureMatch{Source: "upstream", StatusCodes: ad.StatusCodes, Mode: "all"}, Action: "disable"})
	}
	if len(ad.Keywords) > 0 {
		p.AccountRules = append(p.AccountRules, FailureRule{ID: "migrated-disable", Name: "Existing automatic disable", Enabled: true,
			Match: FailureMatch{Source: "upstream", Keywords: ad.Keywords, Mode: "all"}, Action: "disable"})
	}
	return p
}

// matches reports whether the failure matches, and the keyword (the
// expression, for regex keywords) that matched, "" when none did.
func (m FailureMatch) matches(f FailureFacts) (bool, string) {
	if m.Source != f.Source || len(m.Groups) > 0 && !slices.Contains(m.Groups, f.GroupID) ||
		!anyGlob(m.Models, f.Model) || !matchList(m.Protocols, f.Protocol) {
		return false, ""
	}
	statusMatch := false
	for _, r := range m.ranges {
		if f.Status >= r.lo && f.Status <= r.hi {
			statusMatch = true
			break
		}
	}
	text := core.ClassifyPrefix([]byte(f.Text))
	keyword := m.matchedKeyword(text)
	keywordMatch := keyword != ""
	if len(m.ranges) == 0 && len(m.Keywords) == 0 {
		return m.Source == "transport", ""
	}
	if m.Mode == "any" {
		return statusMatch || keywordMatch, keyword
	}
	return (len(m.ranges) == 0 || statusMatch) && (len(m.Keywords) == 0 || keywordMatch), keyword
}

// matchedKeyword is the first keyword (expression) found in text.
func (m FailureMatch) matchedKeyword(text []byte) string {
	if m.KeywordRegex {
		for i, pattern := range m.patterns {
			if pattern.Match(text) {
				return m.Keywords[i]
			}
		}
		return ""
	}
	text = bytes.ToLower(text)
	for _, keyword := range m.Keywords {
		if bytes.Contains(text, []byte(keyword)) {
			return keyword
		}
	}
	return ""
}

func (p *FailurePolicy) decide(f FailureFacts) FailureDecision {
	d := FailureDecision{Action: failureReturn, AccountAction: "none"}
	if p.RetryEnabled && p.MaxRetries > 0 {
		for _, r := range p.RetryRules {
			if !r.Enabled {
				continue
			}
			if ok, _ := r.Match.matches(f); ok {
				d = p.retryDecision(r)
				break
			}
		}
	}
	if p.AccountEnabled {
		for _, r := range p.AccountRules {
			if !r.Enabled {
				continue
			}
			if ok, keyword := r.Match.matches(f); ok {
				d.AccountRule, d.AccountKeyword, d.AccountAction, d.CooldownSeconds = r.ID, keyword, r.Action, r.CooldownSeconds
				break
			}
		}
	}
	return d
}

func (p *FailurePolicy) retryDecision(r FailureRule) FailureDecision {
	d := FailureDecision{RetryRule: r.ID, Action: r.Action, AccountAction: "none",
		SameAccountRetries: r.SameAccountRetries, ExhaustedAction: r.ExhaustedAction, Egress: r.Egress}
	if p.SchemaVersion == 1 {
		return d
	}
	d.SameAccountRetries, d.ExhaustedAction, d.Egress = p.RetriesPerAccount, failureReturn, p.Egress
	if !p.SwitchAccount {
		d.SameAccountRetries = p.MaxRetries
	} else if p.ResetSticky {
		d.ExhaustedAction = failureReset
	} else {
		d.ExhaustedAction = failureSwitch
	}
	if r.NoSameAccountRetry {
		d.SameAccountRetries = 0
	}
	return d
}
