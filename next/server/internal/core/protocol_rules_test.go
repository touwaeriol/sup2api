package core

import (
	"fmt"
	"slices"
	"testing"
)

func TestNewProtocolRulesWithoutBinding(t *testing.T) {
	if r := NewProtocolRules(nil, nil); r != nil {
		t.Fatalf("no binding: %+v", r)
	}
	if r := NewProtocolRules(nil, []ModelProtocolRule{}); r != nil {
		t.Fatalf("empty model_protocols: %+v", r)
	}
	// An empty list is a binding: every protocol of the own platforms closed.
	if r := NewProtocolRules([]string{}, nil); r == nil {
		t.Fatal("protocols [] compiled to no binding")
	}
}

func TestProtocolRulesServes(t *testing.T) {
	const chat, responses, messages = "openai.chat", "openai.responses", "anthropic.messages"
	bound := []string{chat}
	rules := []ModelProtocolRule{
		{Models: []string{"claude-sonnet-4-5"}, Protocols: []string{messages}},
		{Models: []string{"gpt-5.6", "gpt-5.6-mini"}, Protocols: []string{chat, responses, messages}},
		{Models: []string{"gemini-3"}, Protocols: []string{"gemini.generate"}},
	}
	withOther := append(rules[:len(rules):len(rules)], ModelProtocolRule{Models: []string{OtherModels}, Protocols: []string{responses, messages}})
	for _, tc := range []struct {
		name      string
		protocols []string
		rules     []ModelProtocolRule
		model     string
		protocol  string
		cross     bool
		want      string // "" = served
	}{
		{name: "no binding, own protocol", model: "m", protocol: chat},
		{name: "no binding, conversion", model: "m", protocol: messages, cross: true, want: SchedConversionNotEnabled},
		{name: "no binding, converted count_tokens", model: "m", protocol: "anthropic.count_tokens", cross: true, want: SchedConversionNotEnabled},

		{name: "bound protocol", protocols: bound, model: "m", protocol: chat},
		{name: "protocol left out", protocols: bound, model: "m", protocol: responses, want: SchedProtocolClosed},
		{name: "every own protocol closed", protocols: []string{}, model: "m", protocol: chat, want: SchedProtocolClosed},
		{name: "bound, conversion", protocols: bound, model: "m", protocol: messages, cross: true, want: SchedConversionNotEnabled},
		{name: "count_tokens follows its generation protocol", protocols: []string{messages}, model: "m", protocol: "anthropic.count_tokens"},
		{name: "count_tokens of a closed generation protocol", protocols: []string{chat}, model: "m", protocol: "anthropic.count_tokens", want: SchedProtocolClosed},
		{name: "gemini count_tokens follows generate", protocols: []string{"gemini.generate"}, model: "m", protocol: "gemini.count_tokens"},
		{name: "gemini stream_generate is its own protocol", protocols: []string{"gemini.generate"}, model: "m", protocol: "gemini.stream_generate", want: SchedProtocolClosed},

		{name: "listed model, conversion enabled", rules: rules, model: "claude-sonnet-4-5", protocol: messages, cross: true},
		{name: "listed model, own protocol not listed", rules: rules, model: "claude-sonnet-4-5", protocol: chat, want: SchedModelProtocolClosed},
		{name: "listed model, own protocol listed", rules: rules, model: "gpt-5.6", protocol: responses},
		{name: "second model of a rule", rules: rules, model: "gpt-5.6-mini", protocol: messages, cross: true},
		{name: "listed model, another conversion", rules: rules, model: "gpt-5.6", protocol: "gemini.generate", cross: true, want: SchedModelProtocolClosed},
		{name: "listed model, converted count_tokens", rules: rules, model: "gpt-5.6", protocol: "anthropic.count_tokens", cross: true},
		{name: "listed model, stream_generate not listed", rules: rules, model: "gemini-3", protocol: "gemini.stream_generate", cross: true, want: SchedModelProtocolClosed},
		{name: "unlisted model, own protocol", rules: rules, model: "gpt-4o", protocol: chat},
		{name: "unlisted model, conversion", rules: rules, model: "gpt-4o", protocol: messages, cross: true, want: SchedConversionNotEnabled},

		{name: "listed protocol the account closed", protocols: bound, rules: rules, model: "gpt-5.6", protocol: responses, want: SchedProtocolClosed},
		{name: "listed protocol the account serves", protocols: bound, rules: rules, model: "gpt-5.6", protocol: chat},
		{name: "conversion ignores the own protocols", protocols: []string{}, rules: rules, model: "gpt-5.6", protocol: messages, cross: true},
		{name: "unlisted model, protocol left out", protocols: bound, rules: rules, model: "gpt-4o", protocol: responses, want: SchedProtocolClosed},

		{name: "other models, listed protocol", rules: withOther, model: "gpt-4o", protocol: responses},
		{name: "other models, conversion", rules: withOther, model: "gpt-4o", protocol: messages, cross: true},
		{name: "other models, protocol not listed", rules: withOther, model: "gpt-4o", protocol: chat, want: SchedModelProtocolClosed},
		{name: "a named rule wins over other models", rules: withOther, model: "claude-sonnet-4-5", protocol: responses, want: SchedModelProtocolClosed},
		{name: "other models, protocol the account closed", protocols: bound, rules: withOther, model: "gpt-4o", protocol: responses, want: SchedProtocolClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled := NewProtocolRules(tc.protocols, tc.rules)
			reason, ok := compiled.Serves(tc.model, tc.protocol, tc.cross)
			if ok != (tc.want == "") || reason != tc.want {
				t.Fatalf("Serves(%q, %q, cross=%v) = %q, %v; want %q", tc.model, tc.protocol, tc.cross, reason, ok, tc.want)
			}
			// A model no rule names is judged as the other models are.
			if slices.ContainsFunc(tc.rules, func(r ModelProtocolRule) bool { return slices.Contains(r.Models, tc.model) }) {
				return
			}
			if others, served := compiled.ServesOthers(tc.protocol, tc.cross); others != reason || served != ok {
				t.Fatalf("ServesOthers(%q, cross=%v) = %q, %v; Serves gives %q, %v", tc.protocol, tc.cross, others, served, reason, ok)
			}
		})
	}
}

// benchRules is a binding of the largest shape the API accepts in practice:
// 50 rules of 20 models each, and a restricted list of own protocols.
func benchRules() *ProtocolRules {
	rules := make([]ModelProtocolRule, 50)
	for i := range rules {
		models := make([]string, 20)
		for j := range models {
			models[j] = fmt.Sprintf("model-%d-%d", i, j)
		}
		rules[i] = ModelProtocolRule{Models: models, Protocols: []string{"openai.chat", "openai.responses", "anthropic.messages"}}
	}
	return NewProtocolRules([]string{"openai.chat", "openai.responses", "openai.embeddings"}, rules)
}

// benchCandidates are n accounts as a group snapshot hands them over, all
// sharing rules (nil = no binding).
func benchCandidates(n int, rules *ProtocolRules) []AccountRef {
	refs := make([]AccountRef, n)
	for i := range refs {
		refs[i] = AccountRef{ID: int64(i + 1), ProtocolRules: rules}
	}
	return refs
}

// ModelProtocols is the covering entry's protocols as written: the model's
// own entry, else the other models' entry, else none.
func TestProtocolRulesModelProtocols(t *testing.T) {
	rules := []ModelProtocolRule{{Models: []string{"m"}, Protocols: []string{"b.x", "a.y"}}}
	if got := NewProtocolRules(nil, rules).ModelProtocols("m"); !slices.Equal(got, []string{"b.x", "a.y"}) {
		t.Errorf("named: %v", got)
	}
	if got := NewProtocolRules(nil, rules).ModelProtocols("n"); got != nil {
		t.Errorf("no entry: %v", got)
	}
	rules = append(rules, ModelProtocolRule{Models: []string{OtherModels}, Protocols: []string{"c.z"}})
	if got := NewProtocolRules(nil, rules).ModelProtocols("n"); !slices.Equal(got, []string{"c.z"}) {
		t.Errorf("other models: %v", got)
	}
	if got := (*ProtocolRules)(nil).ModelProtocols("m"); got != nil {
		t.Errorf("no binding: %v", got)
	}
}

// The decision runs for every candidate of every request: it must not
// allocate, with or without a binding, served or not.
func TestProtocolRulesServesDoesNotAllocate(t *testing.T) {
	for name, rules := range map[string]*ProtocolRules{"no binding": nil, "50 rules": benchRules()} {
		refs := benchCandidates(500, rules)
		for _, q := range []struct {
			model, protocol string
			cross           bool
		}{
			{"model-49-19", "openai.responses", false},
			{"model-49-19", "anthropic.count_tokens", true},
			{"model-49-19", "gemini.generate", true},
			{"unlisted", "openai.chat", false},
			{"unlisted", "openai.responses_ws", false},
			{"unlisted", "anthropic.messages", true},
		} {
			allocs := testing.AllocsPerRun(100, func() {
				for i := range refs {
					refs[i].ProtocolRules.Serves(q.model, q.protocol, q.cross)
				}
			})
			if allocs != 0 {
				t.Errorf("%s: Serves(%q, %q, cross=%v) allocates %v times per 500 candidates", name, q.model, q.protocol, q.cross, allocs)
			}
		}
	}
}

// BenchmarkProtocolRulesServes judges the 500 candidates of one request
// (DESIGN-2026-11-17-PROTOCOL-BINDING §3.5): 0 allocs/op in every case.
func BenchmarkProtocolRulesServes(b *testing.B) {
	for _, bc := range []struct {
		name            string
		rules           *ProtocolRules
		model, protocol string
		cross           bool
	}{
		{"no_binding/own", nil, "gpt-5.6", "openai.chat", false},
		{"no_binding/conversion", nil, "gpt-5.6", "anthropic.messages", true},
		{"50_rules/listed_model", benchRules(), "model-49-19", "openai.responses", false},
		{"50_rules/listed_model_conversion", benchRules(), "model-49-19", "anthropic.messages", true},
		{"50_rules/unlisted_model", benchRules(), "gpt-5.6", "openai.chat", false},
		{"50_rules/count_tokens", benchRules(), "model-0-0", "anthropic.count_tokens", true},
	} {
		refs := benchCandidates(500, bc.rules)
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for i := range refs {
					refs[i].ProtocolRules.Serves(bc.model, bc.protocol, bc.cross)
				}
			}
		})
	}
}
