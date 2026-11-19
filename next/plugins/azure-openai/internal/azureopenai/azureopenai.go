// Package azureopenai is the Azure OpenAI platform: two account types on
// the openai preset, the same resource endpoint and key served through the
// v1 API (azure_v1) or the classic per-deployment API (azure_deployment).
// They are two types because a route cannot depend on an account setting.
package azureopenai

import (
	"context"
	"net/url"
	"regexp"
	"slices"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// Account type ids (accountTypes[].id of manifest.json).
const (
	TypeV1         = "azure_v1"
	TypeDeployment = "azure_deployment"
)

// EndpointPattern is the base_url both types accept: the address of the
// resource and nothing else, optionally ending in /openai or /openai/v1 (the
// types' Spec.Versions, rebuilt per route on each request), or a Foundry
// project path. A pasted request URL (a deployment, an operation, an
// api-version) is refused instead of being saved and answering 404 on every
// request. It is matched against the value saved, apikey.NormalizeBaseURL's
// output (trimmed, the scheme lower-cased, no trailing "/", the path in the
// escaped form written, so a project name with %2F is one segment), as every
// type's base_url is checked; the suffixes in the case Versions has them in.
// The account form (ui/native/src/forms.ts) shows the same rule on the URL
// as typed.
var EndpointPattern = regexp.MustCompile(`^https?://[^\s/?#@]+(/|/openai/?|/openai/v1/?|/api/projects/[^\s/?#]+/?)?$`)

// APIVersionPattern is the api_version of azure_deployment: a date, with
// -preview for a preview version.
var APIVersionPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(-preview)?$`)

// Platform is the standard platform of both types, their credentials also
// checked against EndpointPattern and, for azure_deployment, the
// api_version (see ValidateCredentials). These rules are not Spec.Fields
// because both refusals carry their own message saying what to enter,
// which fieldcheck's generic ones would lose. Spec.Fields only names the
// keys each type accepts.
type Platform struct{ *standard.Platform }

// New returns the platform of both account types.
func New() *Platform { return &Platform{standard.New(V1(), Deployment())} }

// ValidateCredentials is the standard check (a key, a required base URL)
// plus the account forms' other rules: a base_url matching EndpointPattern
// once normalized (trimmed, the scheme lower-cased: apikey.NormalizeBaseURL)
// and, on azure_deployment, a required api_version matching
// APIVersionPattern.
func (p *Platform) ValidateCredentials(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	resp, err := p.Platform.ValidateCredentials(ctx, in)
	if err != nil {
		return resp, err
	}
	creds, errC := apikey.DecodeObject(in.GetCredentialsJson())
	settings, errS := apikey.DecodeObject(in.GetSettingsJson())
	if errC != nil || errS != nil {
		return resp, nil // the standard check refused it
	}
	var errs pluginsdk.FieldErrors
	v, _ := apikey.Lookup("base_url", settings, creds)
	base, _ := v.(string)
	// A base URL NormalizeBaseURL refuses the standard check refused.
	if n, err := apikey.NormalizeBaseURL(base); err == nil && !EndpointPattern.MatchString(n) {
		errs = errs.Add("base_url", "pattern", "Enter the resource endpoint only, without deployments, chat/completions or api-version / 只填资源地址，不要带 deployments、chat/completions 或 api-version")
	}
	if in.GetAccountType() == TypeDeployment {
		v, _ = apikey.Lookup("api_version", settings, creds)
		s, _ := v.(string)
		switch {
		case v == nil || v == "":
			errs = errs.Add("api_version", "required", "API version is required / 请填写 API 版本")
		case !APIVersionPattern.MatchString(s):
			errs = errs.Add("api_version", "pattern", "API version must be a date such as 2024-10-21 or 2025-04-01-preview / API 版本须为日期，例如 2024-10-21、2025-04-01-preview")
		}
	}
	if len(errs) == 0 {
		return resp, nil
	}
	return &pluginv1.ValidateCredentialsResponse{Errors: append(resp.GetErrors(), errs...)}, nil
}

// V1 is the azure_v1 type: the v1 API under <resource>/openai/v1.
func V1() standard.Type {
	return azure(TypeV1, nil, map[string]standard.Route{
		openai.ProtocolChat:       {Path: standard.Path("/openai/v1/chat/completions"), Edit: openai.IncludeStreamUsage},
		openai.ProtocolResponses:  {Path: standard.Path("/openai/v1/responses")},
		openai.ProtocolEmbeddings: {Path: standard.Path("/openai/v1/embeddings")},
	})
}

// Deployment is the azure_deployment type: the classic API, chat
// completions and embeddings of a deployment.
func Deployment() standard.Type {
	return azure(TypeDeployment, fieldcheck.Fields{"api_version": fieldcheck.Any()}, map[string]standard.Route{
		openai.ProtocolChat:       deployment("/chat/completions", openai.IncludeStreamUsage),
		openai.ProtocolEmbeddings: deployment("/embeddings", nil),
	})
}

// azure is what both types change in the openai preset: a required resource
// endpoint without a default, saved as written, its trailing /openai/v1 or
// /openai left out when each route's full path is appended, the credentials
// api_key, base_url and fields only (their rules in ValidateCredentials), the
// key in api-key, the routes given and the preset's tests on them, no model
// listing.
func azure(id string, fields fieldcheck.Fields, routes map[string]standard.Route) standard.Type {
	t := openai.APIKey(id)
	t.Spec.Fields = fields
	t.Spec.DefaultBaseURL, t.Spec.RequireBaseURL = "", true
	t.Spec.Versions = []string{"/openai/v1", "/openai"}
	t.Spec.Texts.BaseURLRequired = "Resource endpoint is required / 请填写资源地址"
	t.Auth = standard.Auth{Header: "api-key"}
	t.Routes = routes
	t.Tests = slices.DeleteFunc(t.Tests, func(x standard.Test) bool { _, ok := routes[x.Protocol]; return !ok })
	t.Models = nil
	// service_tier and safety_identifier belong to a client's own OpenAI
	// account. The plugin removes them after the operator's rewrites
	// (design §12.5), so an operator's param_override that sets either on
	// an Azure account is removed too.
	t.FilterFields = []string{"service_tier", "safety_identifier"}
	return t
}

// deployment is a route of the classic API: the deployment (the upstream
// model) as one path segment, the account's api_version in the query.
func deployment(operation string, edit func(*jsonedit.Doc, *standard.Request)) standard.Route {
	return standard.Route{
		Path: func(r *standard.Request) string {
			return "/openai/deployments/" + url.PathEscape(r.Model) + operation + "?api-version=" + url.QueryEscape(r.Field("api_version"))
		},
		Model: standard.Model{Required: true, Missing: "model is required (path /openai/deployments/{model}/...)"},
		Edit:  edit,
	}
}
