package usagerules

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func additionalRules() manifest.UsageRules {
	return manifest.UsageRules{Additional: []manifest.AdditionalUsageRule{{Name: "advisor", JSONPath: "usage.iterations", SSEPath: "usage.iterations", SSEEvent: "message_delta", TypePath: "type", TypeValue: "advisor_message", ModelPath: "model", Semantics: "exclusive", Map: map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens", "cache_read_tokens": "cache_read_input_tokens", "cache_creation_tokens": "cache_creation_input_tokens", "cache_creation_1h_tokens": "cache_creation.ephemeral_1h_input_tokens"}}}}
}

func TestAdditionalUsageSnapshotsReplaceAndDoNotChargeExecutorTwice(t *testing.T) {
	u := New(additionalRules())
	for _, n := range []int{5, 9, 9} {
		u.ApplySSE("message_delta", []byte(fmt.Sprintf(`{"usage":{"iterations":[{"type":"message","model":"executor","input_tokens":999,"output_tokens":999},{"type":"advisor_message","model":"advisor-model","input_tokens":3,"output_tokens":%d}]}}`, n)))
	}
	got := u.Additional()
	if len(got) != 1 || got[0].Tokens.Input != 3 || got[0].Tokens.Output != 9 || u.AdditionalError != "" {
		t.Fatal(got, u.AdditionalError)
	}
	got[0].Model = "mutated"
	if u.Additional()[0].Model != "advisor-model" {
		t.Fatal("mutable extraction result")
	}
	u.ApplySSE("ping", []byte(`{"usage":{"iterations":[]}}`))
	if len(u.Additional()) != 1 {
		t.Fatal("unmatched event cleared usage")
	}
	u.ApplySSE("message_delta", []byte(`{"usage":{"iterations":null}}`))
	if len(u.Additional()) != 1 {
		t.Fatal("nullable absent snapshot discarded previous counters")
	}
	u.ApplyJSON([]byte(`{"usage":{"iterations":[]}}`))
	if len(u.Additional()) != 0 {
		t.Fatal("final empty snapshot did not replace")
	}
}

func TestAdditionalUsageRejectsMalformedMetering(t *testing.T) {
	for _, entry := range []string{
		`{"type":"advisor_message","model":"m","input_tokens":-1,"output_tokens":0}`,
		`{"type":"advisor_message","model":"m","input_tokens":1.5,"output_tokens":0}`,
		`{"type":"advisor_message","model":"m","input_tokens":"1","output_tokens":0}`,
		`{"type":"advisor_message","model":"m","input_tokens":1000000000001,"output_tokens":0}`,
		`{"type":"advisor_message","model":"m","input_tokens":1}`,
		`{"type":"advisor_message","model":"","input_tokens":1,"output_tokens":0}`,
		`{"type":"advisor_message","model":"m","input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":2}}`,
	} {
		u := New(additionalRules())
		u.ApplyJSON([]byte(`{"usage":{"iterations":[` + entry + `]}}`))
		if u.AdditionalError == "" {
			t.Fatal("invalid metering accepted", entry)
		}
	}
	u := New(additionalRules())
	u.ApplyJSON([]byte(`{"usage":{"iterations":[` + strings.Repeat(`{},`, MaxAdditionalItems) + `{}]}}`))
	if u.AdditionalError == "" {
		t.Fatal("unbounded array")
	}
}

func TestAdditionalCompactionUsesOnlyHostPrimaryAndSnapshots(t *testing.T) {
	rules := additionalRules()
	compact := rules.Additional[0]
	compact.Name = "compaction"
	compact.TypeValue = "compaction"
	compact.UsePrimaryModel = true
	compact.ModelPath = ""
	rules.Additional = append(rules.Additional, compact)
	for _, stream := range []bool{false, true} {
		u := New(rules).WithPrimaryModel("host-mapped-model")
		raw := []byte(`{"usage":{"input_tokens":23000,"output_tokens":1000,"iterations":[{"type":"compaction","input_tokens":180000,"output_tokens":3500},{"type":"message","input_tokens":23000,"output_tokens":1000},{"type":"advisor_message","model":"advisor","input_tokens":5,"output_tokens":6}]}}`)
		for range 2 {
			if stream {
				u.ApplySSE("message_delta", raw)
			} else {
				u.ApplyJSON(raw)
			}
		}
		items := u.Additional()
		if u.AdditionalError != "" || len(items) != 2 || items[1].Model != "host-mapped-model" || items[1].Tokens.Input != 180000 {
			t.Fatal(items, u.AdditionalError)
		}
	}
	for _, raw := range []string{
		`{"usage":{"iterations":[{"type":"compaction","model":"attacker-model","input_tokens":1,"output_tokens":2}]}}`,
		`{"usage":{"iterations":[{"type":"compaction","model":null,"input_tokens":1,"output_tokens":2}]}}`,
	} {
		// Whatever model the entry reports, it is priced at the primary model.
		u := New(rules).WithPrimaryModel("host-mapped-model")
		u.ApplyJSON([]byte(raw))
		if items := u.Additional(); u.AdditionalError != "" || len(items) != 1 || items[0].Model != "host-mapped-model" {
			t.Fatal(raw, items, u.AdditionalError)
		}
	}
	u := New(rules)
	u.ApplyJSON([]byte(`{"usage":{"iterations":[{"type":"compaction","input_tokens":1,"output_tokens":2}]}}`))
	if u.AdditionalError == "" {
		t.Fatal("missing trusted primary model accepted")
	}
}
