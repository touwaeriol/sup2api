package check

import (
	"fmt"

	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
)

func (v *validator) observations() {
	o := v.m.Observations
	permission := v.perms[observation.Permission]
	if permission != nil {
		types, _ := permission.Scope["types"].(string)
		if types != "own" && types != "all" || len(permission.Scope) != 1 {
			v.add("hostPermissions", "invalid", `gateway.observe scope must be {"types":"own"} or {"types":"all"}`)
		}
	}
	if o == nil {
		if v.hasCap(observation.Capability) {
			v.add("observations", "required", "gateway.observe.v1 requires observations")
		}
		return
	}
	v.needCap("observations", observation.Capability)
	v.needPerm("observations", observation.Permission, "request observations")
	if len(o.Events) == 0 || len(o.Events) > 6 {
		v.add("observations.events", "invalid", "choose between one and six observation phases")
	}
	seen := map[string]bool{}
	for i, name := range o.Events {
		if !observation.ValidPhase(name) || seen[name] {
			v.add(fmt.Sprintf("observations.events[%d]", i), "invalid", "unknown or repeated observation phase")
		}
		seen[name] = true
	}
	if len(o.Metrics) > observation.MaxMetrics {
		v.add("observations.metrics", "limit", "too many metrics")
	}
	seen = map[string]bool{}
	for i, name := range o.Metrics {
		if !observation.ValidMetric(name) || seen[name] {
			v.add(fmt.Sprintf("observations.metrics[%d]", i), "invalid", "invalid or repeated metric name")
		}
		seen[name] = true
	}
}
