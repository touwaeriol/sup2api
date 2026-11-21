package gateway

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/tidwall/gjson"
)

// attemptRulesOf are the attempt usage rules platform pf and its endpoint ep
// declare.
func attemptRulesOf(pf manifest.Platform, ep manifest.Endpoint) []*manifest.AttemptUsageRule {
	rules := []*manifest.AttemptUsageRule{pf.Usage.Attempts}
	if ep.Usage != nil {
		rules = append(rules, ep.Usage.Attempts)
	}
	return rules
}

func attemptUsageActive(rule *manifest.AttemptUsageRule, body []byte) bool {
	if rule == nil {
		return false
	}
	for _, path := range rule.RequiredBy {
		value := gjson.GetBytes(body, path)
		if value.Exists() && value.Type != gjson.Null {
			return true
		}
	}
	return false
}

// hasRequestedAttemptUsage reports whether body asks for the attempt usage
// a rule of the route declares, which turns attempt accounting on.
func hasRequestedAttemptUsage(rt *typeRoute, body []byte) bool {
	for _, rule := range rt.attemptRules {
		if attemptUsageActive(rule, body) {
			return true
		}
	}
	return false
}
