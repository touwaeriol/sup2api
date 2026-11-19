// Package standardtest connects a standard.Platform to the frozen golden
// cases (sdk/pluginsdk/standard/goldentest): a plugin built on the standard
// library replays them with
//
//	standardtest.Run(t, "openai", "../../manifest.json", myplugin.New())
package standardtest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
)

// Run replays every golden case of plugin against p (goldentest.Run with
// Golden; opts may select cases, goldentest.Select). The cases are
// replayed on the frozen file's passHeaders, so Run first requires the
// plugin's manifest.json (at manifestPath) to hand the file's account type
// exactly those client headers on the platform of the file's protocols: a
// manifest that changes them fails here instead of passing the replay
// unnoticed. Every request built must also go to a host the core relays
// to for the manifest (hostChecked).
func Run(t *testing.T, plugin, manifestPath string, p *standard.Platform, opts ...goldentest.Option) {
	t.Helper()
	f, err := goldentest.Load(plugin)
	if err != nil {
		t.Fatal(err)
	}
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if got := manifestPassHeaders(t, &m, f); !slices.Equal(got, f.PassHeaders) {
		t.Fatalf("the manifest hands account type %q the client headers %v, the golden cases were recorded with %v", f.AccountType, got, f.PassHeaders)
	}
	goldentest.Run(t, plugin, hostChecked(&m, f.AccountType, Golden(p, f.PassHeaders)), opts...)
}

// hostChecked is s whose Execute fails a request the core would refuse to
// relay: one to a host m allows neither as an external service nor as an
// upstream address of the case's account, of type accountType unless the
// case names one (manifest.UpstreamHostAllowed, CONTRACTS §112.17).
func hostChecked(m *manifest.Manifest, accountType string, s goldentest.Subject) goldentest.Subject {
	execute := s.Execute
	s.Execute = func(ctx context.Context, in *goldentest.ExecuteInput) (*goldentest.UpstreamRequest, error) {
		out, err := execute(ctx, in)
		if err != nil {
			return out, err
		}
		a := in.Account.Proto()
		var host string
		if u, err := url.Parse(out.URL); err == nil {
			host = strings.ToLower(u.Hostname())
		}
		if !m.UpstreamHostAllowed(host, cmp.Or(a.GetType(), accountType), a.GetSettingsJson(), a.GetCredentialsJson()) {
			return nil, fmt.Errorf("the core would refuse to relay to %q: neither in the manifest's externalServices nor an upstream address of the account", host)
		}
		return out, nil
	}
	return s
}

// manifestPassHeaders are the client headers the core hands the file's
// account type of the manifest on the platform (built-in or the
// manifest's own) of the file's protocols
// (manifest.AccountPlatform.ClientHeaders).
func manifestPassHeaders(t *testing.T, m *manifest.Manifest, f *goldentest.File) []string {
	t.Helper()
	p := filePlatform(f, append(platforms.Builtin(), m.Platforms...))
	if p == nil {
		t.Fatalf("no platform has an endpoint of the protocols of the %s cases", f.Plugin)
	}
	for _, at := range m.AccountTypes {
		if at.ID != f.AccountType {
			continue
		}
		for _, ap := range at.Platforms {
			if ap.Platform == p.ID {
				return ap.ClientHeaders(p)
			}
		}
		t.Fatalf("account type %q of the manifest does not serve platform %s", f.AccountType, p.ID)
	}
	t.Fatalf("the manifest declares no account type %q", f.AccountType)
	return nil
}

// filePlatform is the platform with an endpoint of the protocol of one of
// f's execute cases (a file records one platform; its unknown protocols
// belong to none).
func filePlatform(f *goldentest.File, known []manifest.Platform) *manifest.Platform {
	for i := range f.Cases {
		in, _, err := f.Cases[i].Execute()
		if err != nil {
			continue
		}
		for j := range known {
			if slices.Contains(known[j].Protocols(), in.Meta.Protocol) {
				return &known[j]
			}
		}
	}
	return nil
}

// Golden is the goldentest subject of p: Execute is p's BuildUpstream (what
// ExecuteDefault relays) on the client headers the core hands over, those
// of a case named in passHeaders (the file's PassHeaders; a case holds more
// on purpose); the other four kinds are p's own methods, classification at
// the case's clock.
func Golden(p *standard.Platform, passHeaders []string) goldentest.Subject {
	return goldentest.Subject{
		Execute: func(ctx context.Context, in *goldentest.ExecuteInput) (*goldentest.UpstreamRequest, error) {
			built, err := p.BuildUpstream(ctx, &pluginv1.ExecuteRequest{
				Account: in.Account.Proto(), Meta: in.Meta.Proto(),
				InboundHeaders: passed(in.ClientHeaders, passHeaders), Body: []byte(in.Body),
			})
			if err != nil {
				return nil, err
			}
			return &goldentest.UpstreamRequest{
				Method: built.GetMethod(), URL: built.GetUrl(),
				Headers: built.GetHeaders(), DefaultHeaders: built.GetDefaultHeaders(),
				Body: string(built.GetBody()), UpstreamModel: built.GetUpstreamModel(),
			}, nil
		},
		Test:     p.BuildTestRequest,
		Models:   p.BuildModelsRequest,
		Validate: p.ValidateCredentials,
		Classify: func(_ context.Context, now time.Time, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
			return p.ClassifyAt(now, in), nil
		},
	}
}

// passed is the core's filter: the client headers named in names.
func passed(client map[string]string, names []string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		if value, ok := client[name]; ok {
			out[name] = value
		}
	}
	return out
}
