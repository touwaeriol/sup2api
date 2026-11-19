// Package conformance is the conformance suite every platform plugin runs
// from its own tests (DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 §14.1): one
// call, Run, with a Plugin and its sample Cases. Each rule is a named
// subtest; a failure names the case, what was observed and the rule with
// the section of the design that states it. See README.md.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// Plugin is the plugin under test and what the suite needs to know about it.
type Plugin struct {
	// Platform is the implementation under test. The SDK finds TaskMonitor,
	// UsageExtractor and the rest on it by type assertion, as in production.
	Platform pluginsdk.Platform
	// Manifest is the plugin's manifest.json. The suite serves the plugin
	// with it (pluginsdk.WithManifest): the declared capabilities decide
	// whether the WebSocket rule applies. It also reads from it the client
	// headers the core hands over (rule 4) and the account types that
	// declare a balance (rule 12).
	Manifest []byte
	// Platforms are platforms another plugin declares that the manifest's
	// account types serve (providers/typesafe.Platform() for a relay
	// forwarding to TypeSafe): the suite reads their endpoints and client
	// headers like the built-in platforms'.
	Platforms []manifest.Platform
	// SDK are further options of pluginsdk.Register.
	SDK []pluginsdk.Option
	// Config is the plugin settings sent with Configure (nil = "{}").
	Config any
	// Protocols are the HTTP upstream protocols the plugin declares. Each
	// needs an HTTP case of every form the core sends it in, which the
	// suite reads from the protocol's endpoint (a built-in platform's or
	// the manifest's): stream and non-stream with request.streamPath,
	// stream only with request.stream, otherwise non-stream only. A
	// WebSocket protocol is not listed: its turns are rule 11's.
	Protocols []string
	// Credentials are the credential and signed header names (lower-case):
	// they must be authoritative, never default headers.
	Credentials []string
	// Authoritative are further headers the plugin keeps authoritative, each
	// with the reason (e.g. today's code-plugin behaviour the conversion must
	// keep). Every other header must be a default header.
	Authoritative map[string]string
	// Fetch answers the plugin's auxiliary fetches (token exchange, upload).
	// nil answers UNIMPLEMENTED.
	Fetch func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)
	// FixClock sets the plugin's clock to now; the suite calls it before
	// every Execute. Required when the request depends on the time (a
	// signature), or the determinism and failover rules fail.
	FixClock func(now time.Time)
	// Exceptions are the rule violations the plugin justifies.
	Exceptions []Exception
}

// Endpoint is how the host executes the endpoint of a case.
type Endpoint int

const (
	// Ordinary relays without waiting and returns host_usage.
	Ordinary Endpoint = iota
	// PluginUsage is an endpoint with usageSource "plugin" (extract_usage):
	// relay and wait, return usage.
	PluginUsage
	// TaskSubmission is an endpoint with task.action "submit"
	// (task_submission): relay and wait, return task.
	TaskSubmission
)

// Case is one sample request of the plugin.
type Case struct {
	// Name names the case in failures and exceptions; default
	// "<protocol> stream|non-stream" (" websocket" for a turn).
	Name string
	// Account is a sample account of the type, with credentials and
	// settings JSON. The first case's account of each type is also the one
	// rule 12 queries the balance of.
	Account *pluginv1.Account
	// Meta is the request meta: protocol, model, stream, endpoint ...
	Meta *pluginv1.RequestMeta
	// Body is the request body as the host hands it over (upstream
	// protocol, rewrites done); for a WebSocket turn the message.
	Body []byte
	// Endpoint is how the host executes the case.
	Endpoint Endpoint
	// WebSocket marks one turn of a WebSocket endpoint.
	WebSocket bool
	// UpstreamModel is the model the plugin is declared to map the incoming
	// one to; empty means unchanged.
	UpstreamModel string
	// Observed is the upstream answer of the waited relay (PluginUsage and
	// TaskSubmission cases), e.g. pluginsdktest.Observed(200, body).
	Observed *pluginv1.RelayUpstreamResponse
	// Failover is the account of the failover attempt; nil derives one from
	// Account (other id, every top-level credential string suffixed). Set it when
	// the credentials have structure a suffix breaks.
	Failover *pluginv1.Account

	// inbound are the client headers the core hands over with the case:
	// each the manifest passes for the account type and the protocol's
	// platform.
	inbound map[string]string
}

// Exception excuses one case (or suite item) from one rule. It needs a
// reason, and fails once the rule passes without it.
type Exception struct {
	// Check is the subtest name of the rule, e.g. "03_body".
	Check string
	// Case is the case or item name as the failure reports it.
	Case string
	// Reason says why the plugin cannot follow the rule there.
	Reason string
}

const (
	// largeBodySize is the size the large-body rule pads a case's body to.
	// It only sets the size measured: the host bounds no body a plugin is
	// handed or relays.
	largeBodySize = 8 << 20
	// cancelWait is how long the cancellation rule waits for Execute to
	// return once its context is cancelled: the suite's bound, the host does
	// not time a plugin's own work.
	cancelWait = 2 * time.Second
	// executeTimeout bounds every Execute of the suite.
	executeTimeout = 30 * time.Second
)

// fixedTime is the clock FixClock sets.
var fixedTime = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// Run runs every rule against p with the sample cases, each as a subtest
// of t. Do not run it in parallel with tests that use the network: for its
// duration the process's default transport and resolver fail every dial.
func Run(t *testing.T, p Plugin, cases ...Case) {
	t.Helper()
	run(t, p, cases, func(name string, fn func(testing.TB)) {
		t.Run(name, func(t *testing.T) { fn(t) })
	})
}

// run is Run with the subtest runner supplied, so the self-test can record
// which rule failed instead of failing.
func run(t testing.TB, p Plugin, cases []Case, sub func(name string, fn func(testing.TB))) {
	t.Helper()
	s, err := newSuite(p, cases)
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	defer s.sockets.install()()
	for _, c := range checks {
		sub(c.name, func(t testing.TB) { s.check(t, c) })
	}
}

// suite is one Run: the input, completed with defaults and checked.
type suite struct {
	p          Plugin
	declared   *declared
	cases      []*Case
	exceptions map[itemKey]Exception
	sockets    *sockets
}

type itemKey struct{ check, item string }

func newSuite(p Plugin, cases []Case) (*suite, error) {
	s := &suite{p: p, exceptions: map[itemKey]Exception{}, sockets: &sockets{}}
	d, err := readManifest(p.Manifest, p.Platforms)
	if err != nil {
		return nil, err
	}
	s.declared = d
	errs := []error{s.checkPlugin()}
	names := map[string]bool{}
	for i := range cases {
		c := cases[i]
		if c.Name == "" {
			c.Name = defaultName(&c)
		}
		if names[c.Name] {
			errs = append(errs, fmt.Errorf("two cases are named %q", c.Name))
		}
		names[c.Name] = true
		errs = append(errs, checkCase(&c), s.passHeaders(&c))
		s.cases = append(s.cases, &c)
	}
	errs = append(errs, s.indexExceptions())
	return s, errors.Join(errs...)
}

func (s *suite) checkPlugin() error {
	var errs []error
	if s.p.Platform == nil {
		errs = append(errs, errors.New("Plugin.Platform is required"))
	}
	if len(s.p.Protocols) == 0 {
		errs = append(errs, errors.New("Plugin.Protocols must list the declared upstream protocols"))
	}
	for _, protocol := range s.p.Protocols {
		switch f := s.declared.forms(protocol); {
		case !f.known:
			errs = append(errs, fmt.Errorf("Plugin.Protocols: %q belongs to no endpoint of a built-in platform or of the manifest", protocol))
		case !f.stream && !f.nonStream:
			errs = append(errs, fmt.Errorf("Plugin.Protocols: %q is a WebSocket endpoint's protocol; its turns are rule 11's, leave it out", protocol))
		}
	}
	for name, reason := range s.p.Authoritative {
		if reason == "" {
			errs = append(errs, fmt.Errorf("authoritative header %q needs a reason", name))
		}
	}
	return errors.Join(errs...)
}

func checkCase(c *Case) error {
	var errs []error
	if c.Account == nil || c.Meta.GetProtocol() == "" || len(c.Body) == 0 {
		errs = append(errs, fmt.Errorf("case %q needs Account, Meta with a protocol and Body", c.Name))
	}
	if c.Endpoint != Ordinary && c.Observed == nil {
		errs = append(errs, fmt.Errorf("case %q: a %s case needs Observed, the upstream answer of its waited relay", c.Name, c.Endpoint))
	}
	if c.WebSocket && c.Endpoint != Ordinary {
		errs = append(errs, fmt.Errorf("case %q: a WebSocket turn is an Ordinary endpoint", c.Name))
	}
	return errors.Join(errs...)
}

func defaultName(c *Case) string {
	name := c.Meta.GetProtocol() + " non-stream"
	if c.Meta.GetStream() {
		name = c.Meta.GetProtocol() + " stream"
	}
	if c.WebSocket {
		name += " websocket"
	}
	return name
}

// indexExceptions checks every exception names a rule and one of its items.
func (s *suite) indexExceptions() error {
	known := map[itemKey]bool{}
	for _, c := range checks {
		for _, it := range c.items(s) {
			known[itemKey{c.name, it.name}] = true
		}
	}
	var errs []error
	for _, e := range s.p.Exceptions {
		k := itemKey{e.Check, e.Case}
		switch {
		case e.Reason == "":
			errs = append(errs, fmt.Errorf("exception %s/%s has no reason", e.Check, e.Case))
		case !known[k]:
			errs = append(errs, fmt.Errorf("exception %s/%s names no rule and case of the suite", e.Check, e.Case))
		}
		s.exceptions[k] = e
	}
	return errors.Join(errs...)
}

func (e Endpoint) String() string {
	return [...]string{"ordinary", "plugin-usage", "task-submission"}[e]
}

// passHeaders sets the client headers of c: each header the manifest
// passes for its account type on its protocol, as the core filters them.
func (s *suite) passHeaders(c *Case) error {
	names, err := s.declared.clientHeaders(c.Account.GetType(), c.Meta.GetProtocol())
	if err != nil {
		return fmt.Errorf("case %q: %w", c.Name, err)
	}
	c.inbound = map[string]string{}
	for _, name := range names {
		c.inbound[name] = "conformance-client-" + name
	}
	return nil
}

// request is the ExecuteRequest of c as the host builds it.
func (s *suite) request(c *Case) *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		Meta:           proto.Clone(c.Meta).(*pluginv1.RequestMeta),
		Account:        c.Account,
		Body:           c.Body,
		InboundHeaders: c.inbound,
		ExtractUsage:   c.Endpoint == PluginUsage,
		TaskSubmission: c.Endpoint == TaskSubmission,
	}
}

// execute makes one Execute of c (edit, when set, changes the request) at
// the fixed time.
func (s *suite) execute(ctx context.Context, h *pluginsdktest.Harness, c *Case, edit func(*pluginv1.ExecuteRequest)) *pluginsdktest.Execution {
	in := s.request(c)
	if edit != nil {
		edit(in)
	}
	if s.p.FixClock != nil {
		s.p.FixClock(fixedTime)
	}
	return h.Execute(ctx, in)
}

// once starts a harness for c and executes it once.
func (s *suite) once(t testing.TB, c *Case) *pluginsdktest.Execution {
	return s.run(s.start(t, c, upstream{}), c, nil)
}

// run executes c on h within executeTimeout.
func (s *suite) run(h *pluginsdktest.Harness, c *Case, edit func(*pluginv1.ExecuteRequest)) *pluginsdktest.Execution {
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	defer cancel()
	return s.execute(ctx, h, c, edit)
}
