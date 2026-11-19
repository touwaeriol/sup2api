package standard

import (
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
)

// Merge is one account type serving the routes of several types, usually
// provider presets (a relay site that speaks the OpenAI, Anthropic and
// Gemini protocols): each route keeps its part's auth, headers and error
// policy. The model listing, the balance and the default test are the
// first part's; every part's tests are among Tests, so the console tests a
// protocol the account serves. The type has no headers of its own and no
// default protocol (a gateway request always names its protocol); spec
// replaces the parts' credential rules.
//
// Two parts with one protocol, or a part with FilterFields (they would
// apply to every protocol), are programming errors: Merge panics.
func Merge(id string, spec apikey.Spec, parts ...Type) Type {
	if len(parts) == 0 {
		panic("standard.Merge: no part")
	}
	first := parts[0]
	t := Type{ID: id, Spec: spec, Auth: first.Auth, Classify: first.Classify, Routes: map[string]Route{},
		Test: first.Test, Models: first.Models, Balance: first.Balance}
	for _, part := range parts {
		if len(part.FilterFields) > 0 {
			panic("standard.Merge: part " + part.ID + " filters fields")
		}
		for protocol, r := range part.Routes {
			if _, dup := t.Routes[protocol]; dup {
				panic("standard.Merge: protocol " + protocol + " is in two parts")
			}
			r.Headers = append(slices.Clone(part.Headers), r.Headers...)
			if r.Auth == nil {
				r.Auth = &part.Auth
			}
			if r.Classify == nil {
				r.Classify = &part.Classify
			}
			t.Routes[protocol] = r
		}
		t.Tests = append(t.Tests, part.Tests...)
		if part.Test != nil && !slices.ContainsFunc(part.Tests, func(x Test) bool { return x.Protocol == part.Test.Protocol }) {
			t.Tests = append(t.Tests, *part.Test)
		}
	}
	return t
}
