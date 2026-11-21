package check

import (
	"fmt"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"strings"
)

func (v *validator) additionalUsageRules(field string, rules []manifest.AdditionalUsageRule) {
	if len(rules) > 16 {
		v.add(field, "limit", "at most 16 additional usage rules")
	}
	names := map[string]bool{}
	selectors := map[string]bool{}
	for i, r := range rules {
		f := fmt.Sprintf("%s[%d]", field, i)
		if !usageEventRe.MatchString(r.Name) || names[r.Name] {
			v.add(f+".name", "invalid", "additional usage name must be unique and bounded")
		}
		names[r.Name] = true
		if r.JSONPath == "" && r.SSEPath == "" {
			v.add(f, "required", "a JSON or SSE array path is required")
		}
		for key, path := range map[string]string{"jsonPath": r.JSONPath, "ssePath": r.SSEPath, "typePath": r.TypePath, "modelPath": r.ModelPath} {
			if key == "modelPath" && path == "" && r.UsePrimaryModel {
				continue
			}
			if path == "" && (key == "jsonPath" || key == "ssePath") {
				continue
			}
			v.usagePath(f+"."+key, path)
			if strings.Contains(path, "+") {
				v.add(f+"."+key, "invalid", "additional usage paths do not sum arrays or identities")
			}
		}
		if !usageEventRe.MatchString(r.TypeValue) {
			v.add(f+".typeValue", "invalid", "a bounded type value is required")
		}
		if r.SSEEvent != "" && !usageEventRe.MatchString(r.SSEEvent) {
			v.add(f+".sseEvent", "invalid", "invalid SSE event")
		}
		if r.Semantics != "inclusive" && r.Semantics != "exclusive" {
			v.add(f+".semantics", "invalid", "additional semantics must be explicit")
		}
		v.usageMap(f+".map", r.Map, nil)
		for key, path := range r.Map {
			if key == manifest.UsageModel || strings.Contains(path, "+") {
				v.add(f+".map."+key, "invalid", "use modelPath; token counters must be individual exact integer paths")
			}
		}
		for _, key := range []string{manifest.UsageInputTokens, manifest.UsageOutputTokens} {
			if r.Map[key] == "" {
				v.add(f+".map."+key, "required", "input and output token mappings are required")
			}
		}
		for protocol, path := range []string{r.JSONPath, r.SSEPath} {
			if path == "" {
				continue
			}
			key := fmt.Sprint(protocol) + "|" + path + "|" + r.TypePath + "|" + r.TypeValue
			if selectors[key] {
				v.add(f, "duplicate", "same usage snapshot/type may not be billed twice")
			}
			selectors[key] = true
		}
	}
}
