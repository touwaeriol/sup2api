package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Quota declarations of an account type (CONTRACTS §44).
func TestAccountTypeQuota(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "oauth", Label: manifest.LocalizedText{"en": "OAuth"},
		Form:           keyForm(m),
		UpstreamFields: []string{},
		Platforms:      []manifest.AccountPlatform{{Platform: "video"}},
		Quota: &manifest.AccountQuota{Query: true, Headers: []manifest.QuotaHeader{
			{Key: "5h", Utilization: "anthropic-ratelimit-unified-5h-utilization",
				Reset: "anthropic-ratelimit-unified-5h-reset", Status: "anthropic-ratelimit-unified-5h-status"},
			{Key: "7d_fable", Utilization: "x-fable", UtilizationUnit: "percent", Reset: "x-fable-reset", ResetFormat: "rfc3339"},
		}},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("valid quota rejected: %v", codes(err))
	}
	// Query alone is enough.
	m.AccountTypes[0].Quota = &manifest.AccountQuota{Query: true}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("query-only quota rejected: %v", codes(err))
	}
	m.AccountTypes[0].Quota = &manifest.AccountQuota{}
	if got := codes(Validate(m, files, ValidateOptions{Tooling: true})); got["accountTypes[0].quota"] != "required" {
		t.Fatalf("empty quota: codes = %v", got)
	}
	m.AccountTypes[0].Quota = &manifest.AccountQuota{Headers: []manifest.QuotaHeader{
		{Key: "5H", Utilization: "x-u"},
		{Key: "7d", Utilization: "bad header", UtilizationUnit: "fraction"},
		{Key: "7d", Reset: "x-r", ResetFormat: "iso"},
		{Key: "1d"},
		{Key: "2d", Status: "x-s", UtilizationUnit: "ratio", ResetFormat: "unix"},
	}}
	got := codes(Validate(m, files, ValidateOptions{Tooling: true}))
	want := map[string]string{
		"accountTypes[0].quota.headers[0].key":             "invalid_format",
		"accountTypes[0].quota.headers[1].utilization":     "invalid_format",
		"accountTypes[0].quota.headers[1].utilizationUnit": "invalid",
		"accountTypes[0].quota.headers[2].key":             "duplicate",
		"accountTypes[0].quota.headers[2].resetFormat":     "invalid",
		"accountTypes[0].quota.headers[3]":                 "required",
		"accountTypes[0].quota.headers[4].utilizationUnit": "unexpected",
		"accountTypes[0].quota.headers[4].resetFormat":     "unexpected",
	}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	// A window length header names the window instead of a key.
	m.AccountTypes[0].Quota = &manifest.AccountQuota{Headers: []manifest.QuotaHeader{
		{WindowMinutes: "x-primary-window-minutes", Utilization: "x-primary-used", UtilizationUnit: "percent", Reset: "x-primary-reset"},
		{WindowMinutes: "x-secondary-window-minutes", Utilization: "x-secondary-used", UtilizationUnit: "percent"},
	}}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("windowMinutes quota rejected: %v", codes(err))
	}
	m.AccountTypes[0].Quota = &manifest.AccountQuota{Headers: []manifest.QuotaHeader{
		{Key: "5h", WindowMinutes: "x-minutes", Utilization: "x-u"},
		{WindowMinutes: "bad header", Utilization: "x-u"},
		{Utilization: "x-u"},
	}}
	got = codes(Validate(m, files, ValidateOptions{Tooling: true}))
	for f, c := range map[string]string{
		"accountTypes[0].quota.headers[0].windowMinutes": "unexpected",
		"accountTypes[0].quota.headers[1].windowMinutes": "invalid_format",
		"accountTypes[0].quota.headers[2].key":           "required",
	} {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	m.AccountTypes[0].Quota = &manifest.AccountQuota{Headers: make([]manifest.QuotaHeader, manifest.MaxQuotaHeaders+1)}
	for i := range m.AccountTypes[0].Quota.Headers {
		m.AccountTypes[0].Quota.Headers[i] = manifest.QuotaHeader{Key: "w" + string(rune('a'+i)), Status: "x-s"}
	}
	if got := codes(Validate(m, files, ValidateOptions{Tooling: true})); got["accountTypes[0].quota.headers"] != "too_many" {
		t.Fatalf("too many: codes = %v", got)
	}
}

// quotaEventWindow is a valid window of a quota event.
var quotaEventWindow = manifest.QuotaEventWindow{WindowMinutes: "rate_limits.primary.window_minutes",
	Utilization: "rate_limits.primary.used_percent", UtilizationUnit: "percent", Reset: "rate_limits.primary.reset_at"}

// quotaEventsManifest is a platform plugin whose account type declares
// events as its quota.
func quotaEventsManifest(events ...manifest.QuotaEvent) *manifest.Manifest {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{{ID: "gateway.endpoint"}, {ID: "platform.register"}}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "oauth", Label: manifest.LocalizedText{"en": "OAuth"}, Form: keyForm(m), UpstreamFields: []string{},
		Platforms: []manifest.AccountPlatform{{Platform: "video"}},
		Quota:     &manifest.AccountQuota{Events: events},
	}}
	return m
}

// A quota of events only is valid (CONTRACTS §142).
func TestAccountTypeQuotaEvents(t *testing.T) {
	m := quotaEventsManifest(manifest.QuotaEvent{Type: "codex.rate_limits", Windows: []manifest.QuotaEventWindow{quotaEventWindow}})
	if err := Validate(m, map[string][]byte{}, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("events-only quota rejected: %v", codes(err))
	}
}

// Every malformed event and window is reported at its field.
func TestAccountTypeQuotaEventsInvalid(t *testing.T) {
	m := quotaEventsManifest(
		manifest.QuotaEvent{Type: "", Windows: []manifest.QuotaEventWindow{quotaEventWindow}},
		manifest.QuotaEvent{Type: "bad type", Windows: []manifest.QuotaEventWindow{
			{Utilization: "a..b", UtilizationUnit: "fraction"},
			{WindowMinutes: "w", Reset: "r", UtilizationUnit: "ratio"},
			{WindowMinutes: "w"},
		}},
		manifest.QuotaEvent{Type: "x.limits"},
		manifest.QuotaEvent{Type: "x.limits", Windows: []manifest.QuotaEventWindow{quotaEventWindow}},
	)
	got := codes(Validate(m, map[string][]byte{}, ValidateOptions{Tooling: true}))
	for f, c := range map[string]string{
		"accountTypes[0].quota.events[0].type":                       "required",
		"accountTypes[0].quota.events[1].type":                       "invalid_format",
		"accountTypes[0].quota.events[1].windows[0].windowMinutes":   "required",
		"accountTypes[0].quota.events[1].windows[0].utilization":     "invalid_path",
		"accountTypes[0].quota.events[1].windows[0].utilizationUnit": "invalid",
		"accountTypes[0].quota.events[1].windows[1].utilizationUnit": "unexpected",
		"accountTypes[0].quota.events[1].windows[2]":                 "required",
		"accountTypes[0].quota.events[2].windows":                    "required",
		"accountTypes[0].quota.events[3].type":                       "duplicate",
	} {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
}

// At most MaxQuotaHeaders events.
func TestAccountTypeQuotaEventsTooMany(t *testing.T) {
	events := make([]manifest.QuotaEvent, manifest.MaxQuotaHeaders+1)
	for i := range events {
		events[i] = manifest.QuotaEvent{Type: "e" + string(rune('a'+i)), Windows: []manifest.QuotaEventWindow{quotaEventWindow}}
	}
	got := codes(Validate(quotaEventsManifest(events...), map[string][]byte{}, ValidateOptions{Tooling: true}))
	if got["accountTypes[0].quota.events"] != "too_many" {
		t.Fatalf("too many: codes = %v", got)
	}
}

// Credential refresh declarations of an account type (CONTRACTS §48).
func TestAccountTypeRefresh(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "oauth", Label: manifest.LocalizedText{"en": "OAuth"},
		Form:           keyForm(m),
		UpstreamFields: []string{},
		Platforms:      []manifest.AccountPlatform{{Platform: "video"}},
		SettingsFields: []string{"region"},
		Refresh:        &manifest.AccountRefresh{},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("default refresh rejected: %v", codes(err))
	}
	m.AccountTypes[0].Refresh = &manifest.AccountRefresh{ExpiresAtField: "expiry", BeforeExpirySec: 600}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("custom refresh rejected: %v", codes(err))
	}
	for _, c := range []struct {
		r     manifest.AccountRefresh
		field string
		code  string
	}{
		{manifest.AccountRefresh{ExpiresAtField: "a.b"}, "expiresAtField", "invalid_format"},
		{manifest.AccountRefresh{ExpiresAtField: "region"}, "expiresAtField", "invalid"},
		{manifest.AccountRefresh{BeforeExpirySec: 59}, "beforeExpirySec", "out_of_range"},
		{manifest.AccountRefresh{BeforeExpirySec: 86401}, "beforeExpirySec", "out_of_range"},
	} {
		r := c.r
		m.AccountTypes[0].Refresh = &r
		if got := codes(Validate(m, files, ValidateOptions{Tooling: true})); got["accountTypes[0].refresh."+c.field] != c.code {
			t.Errorf("%+v: codes = %v, want %s %s", c.r, got, c.field, c.code)
		}
	}
}
