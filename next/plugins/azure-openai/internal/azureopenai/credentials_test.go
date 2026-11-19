package azureopenai

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
)

// TestCredentialsRefused: the account forms' rules (base_url and api_key
// required, any non-empty key; base_url an http(s) URL without whitespace,
// query or fragment that matches EndpointPattern once normalized;
// azure_deployment also api_version, required, matching APIVersionPattern;
// no other key) hold in ValidateCredentials, which is final: the core
// checks nothing else. The shared api_key + base_url cases of both types,
// then each type's own.
func TestCredentialsRefused(t *testing.T) {
	const key, endpoint = "0123456789abcdef", "https://res.openai.azure.com"
	v1 := apikeytest.Form{AccountType: TypeV1, Key: key, BaseURL: endpoint, RequireBaseURL: true}
	deployment := apikeytest.Form{AccountType: TypeDeployment, Key: key, BaseURL: endpoint, RequireBaseURL: true,
		Fill: map[string]any{"api_version": "2024-10-21"}}
	// The shared cases' /v1 and /p/a%2Fb are no resource endpoint;
	// endpointCases has azure's own (/openai/v1, /openai and a project name
	// with %2F saved as written).
	cases := slices.DeleteFunc(append(v1.Cases(), deployment.Cases()...), func(c apikeytest.Case) bool {
		return c.Name == "base_url with /v1 saved as written" || c.Name == "base_url with an escaped slash saved as written"
	})
	cases = append(cases, endpointCases(v1)...)
	cases = append(cases, endpointCases(deployment)...)
	deploymentCase := func(name, apiVersion, field, code string) apikeytest.Case {
		return apikeytest.Case{Name: name, AccountType: TypeDeployment, Creds: `{"api_key": "` + key + `", "base_url": "` + endpoint + `"` + apiVersion + `}`, Field: field, Code: code}
	}
	apikeytest.Run(t, New().ValidateCredentials, append(cases,
		apikeytest.Case{Name: "api_version, a key of azure_deployment only", AccountType: TypeV1, Creds: `{"api_key": "` + key + `", "base_url": "` + endpoint + `", "api_version": "2024-10-21"}`, Field: "api_version", Code: "unknown"},
		deploymentCase("api_version missing", ``, "api_version", "required"),
		deploymentCase("api_version empty", `, "api_version": ""`, "api_version", "required"),
		deploymentCase("api_version not a string", `, "api_version": 20241021`, "api_version", ""),
		deploymentCase("api_version not a date", `, "api_version": "latest"`, "api_version", "pattern"),
		deploymentCase("api_version with another suffix", `, "api_version": "2024-10-21-beta"`, "api_version", ""),
		deploymentCase("a preview api_version", `, "api_version": "2025-04-01-preview"`, "", ""),
	))
}

// endpointCases are the base_url cases EndpointPattern adds on the form f's
// type: a pasted request URL refused, the endpoint forms accepted and saved
// as written once normalized (surrounding spaces trimmed, the scheme
// lower-cased, no trailing "/", /openai/v1 and /openai kept, %2F kept), the
// pattern matched on that saved value.
func endpointCases(f apikeytest.Form) []apikeytest.Case {
	creds := func(base string) string {
		m := maps.Clone(f.Fill)
		if m == nil {
			m = map[string]any{}
		}
		m["api_key"], m["base_url"] = f.Key, base
		b, _ := json.Marshal(m)
		return string(b)
	}
	refused := func(name, base string) apikeytest.Case {
		return apikeytest.Case{Name: name, AccountType: f.AccountType, Creds: creds(base), Field: "base_url"}
	}
	accepted := func(name, base, saved string) apikeytest.Case {
		return apikeytest.Case{Name: name, AccountType: f.AccountType, Creds: creds(base), Want: creds(saved)}
	}
	return []apikeytest.Case{
		refused("base_url with a deployment", "https://res.openai.azure.com/openai/deployments/gpt-4o"),
		refused("base_url with an operation", "https://res.openai.azure.com/openai/v1/chat/completions"),
		refused("base_url with another path", "https://res.openai.azure.com/other"),
		accepted("a Foundry project endpoint", "https://res.services.ai.azure.com/api/projects/p1", "https://res.services.ai.azure.com/api/projects/p1"),
		// The path keeps its escaped form, so %2F stays inside the one
		// project segment EndpointPattern allows.
		accepted("a project name with an escaped slash", "https://res.services.ai.azure.com/api/projects/a%2Fb", "https://res.services.ai.azure.com/api/projects/a%2Fb"),
		accepted("base_url with /openai saved as written", "https://res.openai.azure.com/openai/", "https://res.openai.azure.com/openai"),
		accepted("base_url normalized, then matched", " HTTPS://res.openai.azure.com/openai/v1/ ", "https://res.openai.azure.com/openai/v1"),
	}
}

// Credentials that are not a JSON object reach the wrapper's early return:
// the standard check's one error, nothing added.
func TestCredentialsNotAnObject(t *testing.T) {
	for _, typ := range []string{TypeV1, TypeDeployment} {
		resp, err := New().ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{AccountType: typ, CredentialsJson: "[1]"})
		if errs := resp.GetErrors(); err != nil || len(errs) != 1 || errs[0].GetCode() != "invalid_json" {
			t.Errorf("%s: %v %v, want one invalid_json error", typ, err, errs)
		}
	}
}
