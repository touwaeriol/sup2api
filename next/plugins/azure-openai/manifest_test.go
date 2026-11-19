package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"

	"github.com/Sub2API-Devs/sup2api/next/plugins/azure-openai/internal/azureopenai"
)

// endpoints are the openai endpoints each type serves; the type's routes
// must be exactly their protocols (TestManifest).
var endpoints = map[string][]string{
	typeV1:         {"chat_completions", "responses", "embeddings"},
	typeDeployment: {"chat_completions", "embeddings"},
}

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins it: a gRPC process on the forwarding v2
// core, account types only, one creation group whose first variant is the
// v1 API, an unguarded resource endpoint (as for relay, CONTRACTS §21.3.1),
// no prefilled models (a model is a deployment name), and the credentials
// of its own accounts, which the plugin needs to build their requests.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != "azure_openai" || m.Version != "0.1.0" || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("identity = %s %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime, m.HostCompat)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries != "runtimes/{os}-{arch}/plugin" {
		t.Errorf("entry = %+v", m.Entry)
	}
	if !slices.ContainsFunc(m.Capabilities, func(c manifest.Capability) bool { return c.ID == manifest.CapPlatformAdapter }) {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	if len(m.HostPermissions) != 2 || m.HostPermissions[0].ID != "platform.register" || m.HostPermissions[1].ID != "ui.native" {
		t.Errorf("hostPermissions = %+v, want platform.register and ui.native (the account forms)", m.HostPermissions)
	}
	if len(m.Platforms) != 0 || len(m.AccountTypes) != 2 || m.AccountTypes[0].ID != typeV1 || m.AccountTypes[1].ID != typeDeployment {
		t.Fatalf("platforms = %+v accountTypes = %+v, want %s then %s only", m.Platforms, m.AccountTypes, typeV1, typeDeployment)
	}
	// The client headers the core hands over are the openai platform's
	// minus openai-organization and openai-project, which name an
	// organization and a project at OpenAI itself and mean nothing to
	// Azure; derived from the platform, so a header it gains later is
	// noticed.
	notForwarded := []string{"openai-organization", "openai-project"}
	fromPlatform := slices.DeleteFunc(slices.Clone(builtinPlatform("openai").PassHeaders), func(h string) bool { return slices.Contains(notForwarded, h) })
	types := map[string]standard.Type{typeV1: azureopenai.V1(), typeDeployment: azureopenai.Deployment()}
	adapted := map[string]map[string]manifest.AdaptOptions{typeDeployment: {"responses": {}}}
	for _, at := range m.AccountTypes {
		if at.CreationGroup != "azure-openai" || at.AuthMethodLabel["en"] == "" || at.AuthMethodLabel["zh"] == "" {
			t.Errorf("%s: creationGroup %q authMethodLabel %v", at.ID, at.CreationGroup, at.AuthMethodLabel)
		}
		if len(at.Platforms) != 1 || at.Platforms[0].Platform != "openai" || !slices.Equal(at.Platforms[0].Endpoints, endpoints[at.ID]) {
			t.Fatalf("%s: platforms = %+v, want openai %v", at.ID, at.Platforms, endpoints[at.ID])
		}
		// The classic deployment API has no Responses: /v1/responses reaches
		// it converted to chat, which the type declares (CONTRACTS §83.3).
		if !reflect.DeepEqual(at.Platforms[0].Adapted, adapted[at.ID]) {
			t.Errorf("%s: adapted = %+v", at.ID, at.Platforms[0].Adapted)
		}
		if !slices.Equal(at.Platforms[0].PassHeaders, fromPlatform) {
			t.Errorf("%s: passHeaders = %v, want the openai platform's without %v: %v", at.ID, at.Platforms[0].PassHeaders, notForwarded, fromPlatform)
		}
		if !slices.Equal(at.SensitiveFields, []string{"api_key"}) || len(at.GuardedSettings) != 0 || len(at.DefaultModels) != 0 {
			t.Errorf("%s: sensitiveFields %v guardedSettings %v defaultModels %v", at.ID, at.SensitiveFields, at.GuardedSettings, at.DefaultModels)
		}
		if fields, known := at.AddressFields(); !known || !slices.Equal(fields, []string{"base_url"}) || !at.CustomUpstream() {
			t.Errorf("%s: upstreamFields %v (declared %v), custom upstream %v", at.ID, fields, known, at.CustomUpstream())
		}
		if got, want := slices.Sorted(maps.Keys(types[at.ID].Routes)), endpointProtocols(at.Platforms[0].Endpoints); !slices.Equal(got, want) {
			t.Errorf("%s: routes %v, the endpoints need %v", at.ID, got, want)
		}
	}
}

// TestResourceEndpointForm is the base_url rule of both types
// (azureopenai.EndpointPattern, which ValidateCredentials applies and the
// forms show): the address of the resource and nothing else, optionally
// ending in /openai or /openai/v1, or a Foundry project path. A pasted
// request URL (a deployment, an operation, an api-version) is refused with a
// message that says what to enter, instead of being saved and answering 404
// on every request. The rule is matched on the URL as normalized (trimmed,
// the scheme lower-cased, trailing slashes removed), as every type's base
// URL is. The suffixes are matched in the case of the types' Versions (the
// root is found case-sensitively), so another casing is refused too rather
// than accepted and left in the address. What the form accepts is saved as
// written and gives working request URLs.
func TestResourceEndpointForm(t *testing.T) {
	rejected := []string{
		"",
		resource + "/openai/deployments/gpt-4o",
		resource + "/openai/deployments/gpt-4o/chat/completions",
		resource + "/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21",
		resource + "/openai/v1/chat/completions",
		resource + "/openai/v1/responses",
		resource + "/models",
		resource + "/OpenAI/V1",
		resource + "/Openai",
		resource + "/openai/v1/openai",
		resource + "/openai/openai",
		resource + "?api-version=2024-10-21",
		resource + "/?api-version=2024-10-21",
		resource + "/openai?api-version=2024-10-21",
		resource + "/openai/v1?x=1",
		resource + "#keys",
		resource + "/api/projects",
		resource + "/api/projects/",
		resource + "/api/projects/p/openai/v1",
		resource + "/api/Projects/p",
		"https://key@res.openai.azure.com",
		"https://",
		"res.openai.azure.com",
		"ftp://res.openai.azure.com",
	}
	ctx := context.Background()
	for _, accountType := range []string{typeV1, typeDeployment} {
		for _, in := range rejected {
			acc := account(accountType, in, formFields(accountType)...)
			res, err := platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{AccountType: accountType, CredentialsJson: acc.CredentialsJson, SettingsJson: acc.SettingsJson})
			if err != nil || len(res.GetErrors()) != 1 || res.Errors[0].Field != "base_url" {
				t.Errorf("%s %q: validate %v %v, want a base_url error", accountType, in, err, res.GetErrors())
				continue
			}
			// A refusal of the rule itself (not of a malformed URL) says
			// what to enter, in both languages.
			if msg := res.Errors[0].Message; strings.Contains(in, "/deployments/") && !strings.Contains(in, "?") && msg != "Enter the resource endpoint only, without deployments, chat/completions or api-version / 只填资源地址，不要带 deployments、chat/completions 或 api-version" {
				t.Errorf("%s %q: message %q", accountType, in, msg)
			}
		}
		for in, e := range acceptedEndpoints {
			assertEndpoint(t, accountType, in, e)
		}
	}
}

// endpointProtocols are the protocols of the openai endpoints ids, sorted.
func endpointProtocols(ids []string) []string {
	var out []string
	for _, e := range builtinPlatform("openai").Endpoints {
		if slices.Contains(ids, e.ID) {
			out = append(out, e.Protocol)
		}
	}
	slices.Sort(out)
	return out
}

func builtinPlatform(id string) *manifest.Platform {
	for _, p := range platforms.Builtin() {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// load decodes the embedded manifest strictly and checks it with the files
// a package carries, as the core does before it installs a package
// (tooling mode: the binaries come from the build).
func load(t *testing.T) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	files := map[string][]byte{"manifest.json": manifestJSON}
	for _, dir := range []string{"assets"} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := path.Join(dir, e.Name())
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			files[p] = raw
		}
	}
	if err := check.Validate(&m, files, check.ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
	return m
}
