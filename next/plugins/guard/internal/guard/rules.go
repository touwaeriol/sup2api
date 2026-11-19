package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"

	"github.com/Sub2API-Devs/sup2api/next/plugins/guard/internal/model"
)

// Rule kinds.
const (
	KindKeyword = "keyword"
	KindRegex   = "regex"
)

const (
	maxRules      = 500
	maxPatternLen = 1000
	maxNameLen    = 100
)

// Rule is one blocking rule as stored in plg_guard.rules.
type Rule struct {
	ID        int64     `json:"id,omitempty"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Pattern   string    `json:"pattern"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// compiledRule is a rule ready for matching.
type compiledRule struct {
	Rule
	lower string         // keyword, lower-cased
	re    *regexp.Regexp // regex
}

// match returns the byte offset of the first match or -1.
func (r *compiledRule) match(text, lowerText string) int {
	if r.re != nil {
		if loc := r.re.FindStringIndex(text); loc != nil {
			return loc[0]
		}
		return -1
	}
	return strings.Index(lowerText, r.lower)
}

// ruleSet is an immutable snapshot of the enabled rules.
type ruleSet struct {
	rules []*compiledRule
}

func compile(rules []Rule) (*ruleSet, error) {
	rs := &ruleSet{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		cr := &compiledRule{Rule: r}
		switch r.Kind {
		case KindRegex:
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %d: %w", r.ID, err)
			}
			cr.re = re
		default:
			cr.lower = strings.ToLower(r.Pattern)
		}
		rs.rules = append(rs.rules, cr)
	}
	return rs, nil
}

// ruleInput accepts "type" as an alias of "kind" and an optional name.
type ruleInput struct {
	ID      int64  `json:"id,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Type    string `json:"type"`
	Pattern string `json:"pattern"`
	Enabled *bool  `json:"enabled"`
}

// validateRules normalizes and validates a PUT /rules payload.
func validateRules(in []ruleInput) ([]Rule, pluginsdk.FieldErrors) {
	var errs pluginsdk.FieldErrors
	if len(in) > maxRules {
		return nil, errs.Add("rules", "too_many", fmt.Sprintf("at most %d rules / 最多 %d 条规则", maxRules, maxRules))
	}
	out := make([]Rule, 0, len(in))
	seen := map[int64]bool{}
	for i, r := range in {
		f := func(name string) string { return fmt.Sprintf("rules[%d].%s", i, name) }
		kind := strings.ToLower(strings.TrimSpace(r.Kind))
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(r.Type))
		}
		if kind == "" {
			kind = KindKeyword
		}
		rule := Rule{ID: r.ID, Name: strings.TrimSpace(r.Name), Kind: kind, Pattern: r.Pattern, Enabled: r.Enabled == nil || *r.Enabled}
		if rule.ID < 0 || (rule.ID > 0 && seen[rule.ID]) {
			errs = errs.Add(f("id"), "invalid", "duplicate or invalid id / id 重复或无效")
		}
		seen[rule.ID] = true
		switch kind {
		case KindKeyword:
			rule.Pattern = strings.TrimSpace(rule.Pattern)
		case KindRegex:
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				errs = errs.Add(f("pattern"), "regex", "invalid regular expression: "+err.Error()+" / 正则表达式无效")
			}
		default:
			errs = errs.Add(f("kind"), "enum", "kind must be keyword or regex / 类型必须是 keyword 或 regex")
		}
		if rule.Pattern == "" {
			errs = errs.Add(f("pattern"), "required", "pattern is required / 请填写匹配内容")
		} else if len(rule.Pattern) > maxPatternLen {
			errs = errs.Add(f("pattern"), "too_long", fmt.Sprintf("pattern exceeds %d bytes / 匹配内容超过 %d 字节", maxPatternLen, maxPatternLen))
		}
		if rule.Name == "" {
			rule.Name = textutil.TruncateRunes(rule.Pattern, 40)
		}
		if utf8.RuneCountInString(rule.Name) > maxNameLen {
			errs = errs.Add(f("name"), "too_long", fmt.Sprintf("name exceeds %d characters / 名称超过 %d 个字符", maxNameLen, maxNameLen))
		}
		out = append(out, rule)
	}
	return out, errs
}

func (p *Plugin) loadRules(ctx context.Context) ([]Rule, error) {
	db, err := p.db(ctx)
	if err != nil {
		return nil, err
	}
	var rules []Rule // Find makes it [] when there is no rule
	err = db.Model(&model.Rule{}).Select("id, name, kind, pattern, enabled, updated_at").Order("id").Find(&rules).Error
	return rules, err
}

// reloadRules reads the rules table and swaps the matcher snapshot.
func (p *Plugin) reloadRules(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.ruleReloadMu.Lock()
	p.ruleReloadSeq++
	seq := p.ruleReloadSeq
	p.ruleReloadMu.Unlock()
	rules, err := p.loadRules(ctx)
	if err != nil {
		return err
	}
	rs, err := compile(rules)
	if err != nil {
		return err
	}
	// Newer reloads invalidate in-flight SELECTs before publishing. Polls,
	// broadcasts and PUT-triggered refreshes cannot restore an old matcher.
	p.ruleReloadMu.Lock()
	defer p.ruleReloadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if seq == p.ruleReloadSeq {
		p.rules.Store(rs)
	}
	return nil
}

// getRules serves GET /rules.
func (p *Plugin) getRules(ctx context.Context, _ *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	rules, err := p.loadRules(ctx)
	if err != nil {
		return pluginsdk.ErrorResponse(http.StatusServiceUnavailable, "unavailable", err.Error()), nil
	}
	return pluginsdk.DataResponse(rules), nil
}

// putRules serves PUT /rules: replaces the whole rule set. Body is
// {"rules": [...]} or a bare array. Rules without id are created; existing
// rules missing from the list are deleted. Concurrent PUTs (other admins,
// other nodes) are serialized: the last one replaces the set as a whole.
func (p *Plugin) putRules(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in []ruleInput
	body := strings.TrimSpace(string(req.GetBody()))
	if strings.HasPrefix(body, "[") {
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			return pluginsdk.ErrorResponse(http.StatusBadRequest, "invalid_argument", "invalid JSON body / 请求体不是合法 JSON"), nil
		}
	} else {
		var wrapper struct {
			Rules []ruleInput `json:"rules"`
		}
		if err := json.Unmarshal([]byte(body), &wrapper); err != nil {
			return pluginsdk.ErrorResponse(http.StatusBadRequest, "invalid_argument", "invalid JSON body / 请求体不是合法 JSON"), nil
		}
		in = wrapper.Rules
	}
	rules, errs := validateRules(in)
	if len(errs) > 0 {
		return pluginsdk.FieldErrorResponse("invalid rules / 规则无效", errs), nil
	}
	db, err := p.db(ctx)
	if err != nil {
		return pluginsdk.ErrorResponse(http.StatusServiceUnavailable, "unavailable", err.Error()), nil
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		return replaceRules(tx, rules)
	})
	if err != nil {
		return nil, err
	}
	// Committed: the other nodes reload now (best effort), this one below.
	p.publishRulesChanged(ctx)
	if err := p.reloadRules(ctx); err != nil {
		return nil, err
	}
	return p.getRules(ctx, req)
}

// replaceRules writes the whole rule set inside tx: rules with an id are
// updated (or created when that id is gone), and every rule missing from the
// list is deleted.
//
// The table lock serializes whole-set replacements so the last save wins as a
// whole. Without it two PUTs on two nodes interleave under READ COMMITTED:
// each DELETE misses the rows the other one inserts, so both sets' new rules
// survive (or the two DELETEs deadlock on each other's rows). SHARE ROW
// EXCLUSIVE conflicts with itself and with row writes, not with plain
// SELECTs, so rule reloads and GET /rules are not blocked. Taking it needs
// UPDATE/DELETE on rules, which this handler needs anyway; the core's
// database user, which the plugin's connections log in as, owns the table.
//
// A table lock rather than a cluster lock (host "lock" permission): those live
// in Redis, are not fenced and are not part of this transaction - a holder
// that stalls past its ttl would overlap the next one again. This one is held
// by the transaction itself and released on commit or rollback, exactly the
// span to protect. Not pg_advisory_xact_lock either: advisory keys are
// database-wide, shared with the core and every other plugin.
func replaceRules(tx *gorm.DB, rules []Rule) error {
	if err := pgdb.LockTable(tx, "rules"); err != nil {
		return err
	}
	// Never nil: a NULL array matches no row, and nothing would be deleted.
	keep := make(pgdb.Array[int64], 0, len(rules))
	for _, r := range rules {
		if r.ID > 0 {
			keep = append(keep, r.ID)
		}
	}
	if err := tx.Where("NOT (id = ANY(?))", keep).Delete(&model.Rule{}).Error; err != nil {
		return err
	}
	for _, r := range rules {
		if err := upsertRule(tx, r); err != nil {
			return err
		}
	}
	return nil
}

// columns are the stored fields of r as a map: created from a model.Rule,
// a disabled rule would get the column default (true) instead.
func (r Rule) columns() map[string]any {
	return map[string]any{"name": r.Name, "kind": r.Kind, "pattern": r.Pattern, "enabled": r.Enabled}
}

// upsertRule updates an existing rule, creating it when its id is unknown
// (deleted meanwhile) and skipping the write when nothing changed.
func upsertRule(tx *gorm.DB, r Rule) error {
	if r.ID > 0 {
		set := r.columns()
		set["updated_at"] = gorm.Expr("now()")
		res := tx.Model(&model.Rule{}).
			Where("id = ? AND (name, kind, pattern, enabled) IS DISTINCT FROM (?, ?, ?, ?)", r.ID, r.Name, r.Kind, r.Pattern, r.Enabled).
			Updates(set)
		if res.Error != nil || res.RowsAffected > 0 {
			return res.Error
		}
		exists, err := pgdb.Exists(tx, tx.Model(&model.Rule{}).Where("id = ?", r.ID))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		// Unknown id (deleted meanwhile): create it as a new rule.
	}
	return tx.Model(&model.Rule{}).Create(r.columns()).Error
}
