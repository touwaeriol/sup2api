package check

import (
	"fmt"
	"regexp"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// quotaEventTypeRe is the shape of a WebSocket message "type" a quota event
// matches ("codex.rate_limits").
var quotaEventTypeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// quotaEvents validates accountTypes[].quota.events (CONTRACTS §142): a
// bounded list of distinct message types, each with at least one window.
func (v *validator) quotaEvents(f string, events []manifest.QuotaEvent) {
	if len(events) > manifest.MaxQuotaHeaders {
		v.add(f+".events", "too_many", "at most %d quota events", manifest.MaxQuotaHeaders)
	}
	types := map[string]bool{}
	for i, ev := range events {
		ef := fmt.Sprintf("%s.events[%d]", f, i)
		switch {
		case ev.Type == "":
			v.add(ef+".type", "required", "type is required")
		case !quotaEventTypeRe.MatchString(ev.Type):
			v.add(ef+".type", "invalid_format", "type %q must match %s", ev.Type, quotaEventTypeRe.String())
		case types[ev.Type]:
			v.add(ef+".type", "duplicate", "event type %q declared twice", ev.Type)
		}
		types[ev.Type] = true
		v.quotaEventWindows(ef, ev.Windows)
	}
}

// quotaEventWindows validates the windows of one quota event: each is named
// by a window length path and reads at least one value, every path reads one
// value (ValidUsagePath, as the usage rules' paths) and the unit is known.
func (v *validator) quotaEventWindows(ef string, windows []manifest.QuotaEventWindow) {
	switch {
	case len(windows) == 0:
		v.add(ef+".windows", "required", "at least one window is required")
	case len(windows) > manifest.MaxQuotaHeaders:
		v.add(ef+".windows", "too_many", "at most %d windows", manifest.MaxQuotaHeaders)
	}
	for j, w := range windows {
		wf := fmt.Sprintf("%s.windows[%d]", ef, j)
		if w.WindowMinutes == "" {
			v.add(wf+".windowMinutes", "required", "windowMinutes names the window")
		}
		if w.Utilization == "" && w.Reset == "" {
			v.add(wf, "required", "at least one of utilization and reset is required")
		}
		for name, path := range map[string]string{"windowMinutes": w.WindowMinutes, "utilization": w.Utilization, "reset": w.Reset} {
			if path != "" && !ValidUsagePath(path) {
				v.add(wf+"."+name, "invalid_path", "%q is not a gjson path that reads one value", path)
			}
		}
		v.quotaUnit(wf, w.UtilizationUnit, w.Utilization)
	}
}

// quotaUnit validates a utilizationUnit: "ratio" or "percent", and only
// beside a utilization.
func (v *validator) quotaUnit(f, unit, utilization string) {
	switch unit {
	case "", manifest.QuotaUnitRatio, manifest.QuotaUnitPercent:
		if unit != "" && utilization == "" {
			v.add(f+".utilizationUnit", "unexpected", "utilizationUnit needs utilization")
		}
	default:
		v.add(f+".utilizationUnit", "invalid", "utilizationUnit must be %q or %q", manifest.QuotaUnitRatio, manifest.QuotaUnitPercent)
	}
}
