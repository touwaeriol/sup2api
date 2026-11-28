package check

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

var (
	// quotaKeyRe is the shape of a quota window key ("5h", "7d_fable").
	quotaKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)
	// headerNameRe is an HTTP field name (RFC 9110 token), at most 100 bytes.
	headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,100}$")
)

// quota validates accountTypes[].quota (CONTRACTS §44): at least one header
// window, event or query, distinct well-formed window keys (or a window length
// header instead of a key), every window naming at least one header, valid
// header names and known units and formats.
func (v *validator) quota(f string, q *manifest.AccountQuota) {
	if q == nil {
		return
	}
	f += ".quota"
	if q.AutoRefresh && !q.Query {
		v.add(f+".autoRefresh", "required", "autoRefresh requires query")
	}
	v.quotaWindowDefinitions(f, q.Windows)
	if !q.Supported() {
		v.add(f, "required", "quota must declare headers, events or query")
		return
	}
	v.quotaEvents(f, q.Events)
	if len(q.Headers) > manifest.MaxQuotaHeaders {
		v.add(f+".headers", "too_many", "at most %d quota windows", manifest.MaxQuotaHeaders)
	}
	keys := map[string]bool{}
	for i, h := range q.Headers {
		v.quotaHeader(fmt.Sprintf("%s.headers[%d]", f, i), h, keys)
	}
}

// quotaWindowDefinitions validates quota.windows: distinct well-formed keys,
// each with an English label.
func (v *validator) quotaWindowDefinitions(f string, windows []manifest.QuotaWindowDefinition) {
	if len(windows) > manifest.MaxQuotaHeaders {
		v.add(f+".windows", "too_many", "at most %d quota windows", manifest.MaxQuotaHeaders)
	}
	definitions := map[string]bool{}
	for i, w := range windows {
		wf := fmt.Sprintf("%s.windows[%d]", f, i)
		v.quotaKey(wf+".key", w.Key, definitions)
		if strings.TrimSpace(w.Label["en"]) == "" {
			v.add(wf+".label", "required", "label.en is required")
		}
		if len(w.ShortLabel) > 0 && strings.TrimSpace(w.ShortLabel["en"]) == "" {
			v.add(wf+".shortLabel", "required", "shortLabel.en is required")
		}
	}
}

// quotaKey validates a window key: present, well formed and not in seen,
// which it is then added to.
func (v *validator) quotaKey(f, key string, seen map[string]bool) {
	switch {
	case key == "":
		v.add(f, "required", "key is required")
	case !quotaKeyRe.MatchString(key):
		v.add(f, "invalid_format", "key %q must match %s", key, quotaKeyRe.String())
	case seen[key]:
		v.add(f, "duplicate", "window %q declared twice", key)
	}
	seen[key] = true
}

// quotaHeader validates one quota.headers window: a key (distinct across
// keys) or a window length header, at least one value header, valid header
// names, a known unit and reset format.
func (v *validator) quotaHeader(hf string, h manifest.QuotaHeader, keys map[string]bool) {
	switch {
	case h.Key != "" && h.WindowMinutes != "":
		v.add(hf+".windowMinutes", "unexpected", "windowMinutes names the window; leave key out")
	case h.WindowMinutes != "":
		// Named by the length header on each response: no key to check.
	case h.Key == "":
		v.add(hf+".key", "required", "key or windowMinutes is required")
	default:
		v.quotaKey(hf+".key", h.Key, keys)
	}
	if h.Utilization == "" && h.Reset == "" && h.Status == "" {
		v.add(hf, "required", "at least one of utilization, reset and status is required")
	}
	for name, value := range map[string]string{"utilization": h.Utilization, "reset": h.Reset, "status": h.Status, "windowMinutes": h.WindowMinutes} {
		if value != "" && !headerNameRe.MatchString(value) {
			v.add(hf+"."+name, "invalid_format", "%q is not an HTTP header name", value)
		}
	}
	v.quotaUnit(hf, h.UtilizationUnit, h.Utilization)
	v.quotaResetFormat(hf, h.ResetFormat, h.Reset)
}

// quotaResetFormat validates a resetFormat: a known format, and only beside
// a reset.
func (v *validator) quotaResetFormat(f, format, reset string) {
	switch format {
	case "", manifest.QuotaResetUnix, manifest.QuotaResetRFC3339, manifest.QuotaResetDelta:
		if format != "" && reset == "" {
			v.add(f+".resetFormat", "unexpected", "resetFormat needs reset")
		}
	default:
		v.add(f+".resetFormat", "invalid", "resetFormat must be %q, %q or %q",
			manifest.QuotaResetUnix, manifest.QuotaResetRFC3339, manifest.QuotaResetDelta)
	}
}

// credKeyRe is the shape of a top-level credentials key.
var credKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// refresh validates accountTypes[].refresh (CONTRACTS §48): a credentials
// key for the expiry (not a settings field: the host reads it from the
// encrypted credentials) and a renewal lead time within bounds.
func (v *validator) refresh(f string, at manifest.AccountType) {
	r := at.Refresh
	if r == nil {
		return
	}
	f += ".refresh"
	if r.ExpiresAtField != "" && !credKeyRe.MatchString(r.ExpiresAtField) {
		v.add(f+".expiresAtField", "invalid_format", "expiresAtField %q must match %s", r.ExpiresAtField, credKeyRe.String())
	}
	for _, s := range at.SettingsFields {
		if s == r.Field() {
			v.add(f+".expiresAtField", "invalid", "%q is a settings field; the expiry must be a credentials field", s)
		}
	}
	if r.BeforeExpirySec != 0 && (r.BeforeExpirySec < manifest.MinRefreshBeforeExpiry || r.BeforeExpirySec > manifest.MaxRefreshBeforeExpiry) {
		v.add(f+".beforeExpirySec", "out_of_range", "beforeExpirySec must be %d-%d", manifest.MinRefreshBeforeExpiry, manifest.MaxRefreshBeforeExpiry)
	}
}
