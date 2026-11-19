package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Automatic disabling (CONTRACTS §42), modelled on new-api's
// AutomaticDisableChannelEnabled / AutomaticDisableStatusCodeRanges /
// AutomaticDisableKeywords.

const (
	settingsKeyAutoDisable = protocol.SettingsAutoDisable

	maxAutoDisableRanges     = 50
	maxAutoDisableKeywords   = 100
	maxAutoDisableKeywordLen = 200
)

// AutoDisableSettings is the "auto_disable" row of the settings table.
type AutoDisableSettings struct {
	Enabled     bool     `json:"enabled"`
	StatusCodes string   `json:"status_codes"`
	Keywords    []string `json:"keywords"`
	// ranges is StatusCodes parsed by compiled.
	ranges []statusRange
}

func defaultAutoDisableSettings() AutoDisableSettings {
	return AutoDisableSettings{Enabled: true, StatusCodes: "401", Keywords: []string{
		"your credit balance is too low",
		"this organization has been disabled.",
		"you exceeded your current quota",
		"permission denied",
		"the security token included in the request is invalid",
		"operation not allowed",
		"your account is not authorized",
	}}.compiled()
}

// compiled parses StatusCodes; a stored value that no longer parses matches
// no status code.
func (s AutoDisableSettings) compiled() AutoDisableSettings {
	rs, err := parseStatusRanges(s.StatusCodes)
	if err != nil {
		slog.Warn("gateway: invalid auto-disable status codes", "value", s.StatusCodes, "err", err)
	}
	s.ranges = rs
	if s.Keywords == nil {
		s.Keywords = []string{}
	}
	return s
}

// rule reports which administrator rule, if any, asks to disable the account
// for this upstream failure; "" when none matches.
func (s AutoDisableSettings) rule(status int, body []byte, transportErr string) string {
	if status > 0 {
		for _, r := range s.ranges {
			if status >= r.lo && status <= r.hi {
				return "matched auto-disable rule: status " + strconv.Itoa(status)
			}
		}
	}
	if len(s.Keywords) == 0 {
		return ""
	}
	text := bytes.ToLower(append(append(append([]byte(nil), core.ClassifyPrefix(body)...), '\n'), transportErr...))
	for _, k := range s.Keywords {
		if bytes.Contains(text, []byte(k)) {
			return fmt.Sprintf("matched auto-disable rule: keyword %q", k)
		}
	}
	return ""
}

func loadAutoDisableSettings(ctx context.Context, q *gorm.DB) (AutoDisableSettings, error) {
	v := defaultAutoDisableSettings()
	err := loadSettingsRow(ctx, q, settingsKeyAutoDisable, &v)
	return v.compiled(), err
}

// ---------------------------------------------------------------- status codes

// statusRange is an inclusive range of HTTP status codes.
type statusRange struct{ lo, hi int }

// parseStatusRanges parses "401,403,500-503" into sorted, merged ranges.
func parseStatusRanges(s string) ([]statusRange, error) {
	var out []statusRange
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		r, err := parseStatusRange(part)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if len(out) > maxAutoDisableRanges {
		return nil, fmt.Errorf("at most %d ranges", maxAutoDisableRanges)
	}
	return mergeRanges(out), nil
}

func parseStatusRange(part string) (statusRange, error) {
	lo, hi, isRange := strings.Cut(part, "-")
	a, err := parseStatus(lo)
	if err != nil {
		return statusRange{}, err
	}
	if !isRange {
		return statusRange{a, a}, nil
	}
	b, err := parseStatus(hi)
	if err != nil {
		return statusRange{}, err
	}
	if b < a {
		return statusRange{}, fmt.Errorf("range %q is reversed", part)
	}
	return statusRange{a, b}, nil
}

func parseStatus(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 100 || n > 599 {
		return 0, fmt.Errorf("%q is not a status code (100-599)", strings.TrimSpace(s))
	}
	return n, nil
}

func mergeRanges(in []statusRange) []statusRange {
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	var out []statusRange
	for _, r := range in {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			out[n-1].hi = max(out[n-1].hi, r.hi)
			continue
		}
		out = append(out, r)
	}
	return out
}

func formatStatusRanges(rs []statusRange) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = strconv.Itoa(r.lo)
		if r.hi != r.lo {
			parts[i] += "-" + strconv.Itoa(r.hi)
		}
	}
	return strings.Join(parts, ",")
}

// normalizeKeywords trims, lower-cases and de-duplicates the keywords,
// dropping blank ones.
func normalizeKeywords(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, k := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// ---------------------------------------------------------------- API

// autoDisableInput is the PUT /settings/auto-disable body; nil fields keep
// their current value.
type autoDisableInput struct {
	Enabled     *bool     `json:"enabled"`
	StatusCodes *string   `json:"status_codes"`
	Keywords    *[]string `json:"keywords"`
}

// validate checks the fields (CONTRACTS §42.2) and normalises them in place.
func (in *autoDisableInput) validate(ctx context.Context) []core.FieldError {
	zh := core.Locale(ctx) == "zh"
	msg := func(en, cn string) string {
		if zh {
			return cn
		}
		return en
	}
	var fe []core.FieldError
	if in.StatusCodes != nil {
		rs, err := parseStatusRanges(*in.StatusCodes)
		if err != nil {
			fe = append(fe, core.FieldError{Field: "status_codes", Code: "invalid",
				Message: msg("use status codes 100-599 or ranges such as 401,403,500-503: "+err.Error(),
					"请填写 100-599 的状态码或区间，如 401,403,500-503："+err.Error())})
		}
		s := formatStatusRanges(rs)
		in.StatusCodes = &s
	}
	if in.Keywords == nil {
		return fe
	}
	for i, k := range *in.Keywords {
		if utf8.RuneCountInString(strings.TrimSpace(k)) > maxAutoDisableKeywordLen {
			fe = append(fe, core.FieldError{Field: "keywords[" + strconv.Itoa(i) + "]", Code: "invalid",
				Message: msg("at most 200 characters", "最多 200 个字符")})
		}
	}
	kws := normalizeKeywords(*in.Keywords)
	if len(kws) > maxAutoDisableKeywords {
		fe = append(fe, core.FieldError{Field: "keywords", Code: "too_many",
			Message: msg("at most 100 keywords", "最多 100 个关键词")})
	}
	in.Keywords = &kws
	return fe
}

func (g *Gateway) getAutoDisableHandler(c *gin.Context) {
	db, err := g.db()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	v, err := loadAutoDisableSettings(c.Request.Context(), db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}

// putAutoDisableHandler updates the provided fields, then invalidates the
// local settings cache and broadcasts config:changed.
func (g *Gateway) putAutoDisableHandler(c *gin.Context) {
	ctx := c.Request.Context()
	db, err := g.db()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var in autoDisableInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if fe := in.validate(ctx); len(fe) > 0 {
		httpapi.Fail(c, core.InvalidFields(fe...))
		return
	}
	uid, _ := core.UserID(ctx)
	cur := defaultAutoDisableSettings()
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		if err := legacyFailureSettingGuard(ctx, tx); err != nil {
			return err
		}
		if _, err := store.UpdateSettingJSONTx(ctx, tx, settingsKeyAutoDisable, nullID(uid), func(raw json.RawMessage) (json.RawMessage, error) {
			if err := json.Unmarshal(raw, &cur); err != nil {
				return nil, err
			}
			if in.Enabled != nil {
				cur.Enabled = *in.Enabled
			}
			if in.StatusCodes != nil {
				cur.StatusCodes = *in.StatusCodes
			}
			if in.Keywords != nil {
				cur.Keywords = *in.Keywords
			}
			return json.Marshal(cur)
		}); err != nil {
			return err
		}
		return core.EmitSettingsChanged(ctx, g.d.Events, tx, settingsKeyAutoDisable)
	}); err != nil {
		httpapi.Fail(c, err)
		return
	}
	g.changed(ctx, "settings.auto_disable")
	httpapi.OK(c, cur.compiled())
}
