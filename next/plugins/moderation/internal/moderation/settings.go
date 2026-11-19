package moderation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Modes (settings.mode, CONTRACTS §72.5): the master switch and the default
// level in one (new-api enabled + default_level).
const (
	ModeOff     = "off"
	ModeObserve = "observe" // on, default level observe: post unless the user is at the enforce level
	ModeEnforce = "enforce" // on, default level enforce: pre unless the user is at the observe level
)

// Verdicts and actions of the 0.1 columns events.verdict / events.action, and
// the LLM verdicts of submit_verdict.
const (
	VerdictPass  = "pass"
	VerdictFlag  = "flag"
	VerdictBlock = "block"
	VerdictError = "error"

	ActionAllow = "allow"
	ActionDeny  = "deny"
)

// Record results (events.result).
const (
	ResultBlock      = "block"
	ResultHit        = "hit"
	ResultOverturned = "overturned"
	ResultError      = "error"
	ResultPass       = "pass"
)

// Category is one moderation category offered to the LLM.
type Category struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Settings is the plugin configuration (CONTRACTS §72.10; the keys and
// limits CheckSettings accepts). Of the keys retired in 0.11.0 only those of
// retiredKeys are read, from settings not saved since.
type Settings struct {
	Mode string `json:"mode"`

	InputMaxChars int `json:"input_max_chars"`
	MinChars      int `json:"min_chars"`
	// VerdictCacheDays is how long a cached verdict lives; 0 = no cache.
	VerdictCacheDays int `json:"verdict_cache_days"`
	CacheEntries     int `json:"cache_entries"`
	MaxConcurrency   int `json:"max_concurrency"`
	QueueSize        int `json:"queue_size"`
	TTFTTimeoutMs    int `json:"ttft_timeout_ms"`

	// Chunking and admission (CONTRACTS §72.4, §72.5; 0.6.0).
	InputMaxChunks    int    `json:"input_max_chunks"`
	InputOverflow     string `json:"input_overflow"`
	InputMaxImages    int    `json:"input_max_images"`
	MaxInflightMB     int    `json:"max_inflight_mb"`
	ErrorTextMaxChars int    `json:"error_text_max_chars"`

	BlockStatus  int    `json:"block_status"`
	BlockMessage string `json:"block_message"`
	// HitReplay and FailPolicy (CONTRACTS §72.5, 0.10.0): what is beside a
	// replayed hit, and what a request whose moderation failed gets.
	HitReplay  string `json:"hit_replay"`
	FailPolicy string `json:"fail_policy"`

	RecordPass       bool `json:"record_pass"`
	RecordOverturned bool `json:"record_overturned"`
	RecordErrors     bool `json:"record_errors"`
	StoreText        bool `json:"store_text"`
	// RecordPassInputMessage: with store_text, records without a stage hit
	// (passes) keep the raw messages too (new-api record_pass_input_message).
	RecordPassInputMessage bool `json:"record_pass_input_message"`

	HitRetentionDays        int `json:"hit_retention_days"`
	OverturnedRetentionDays int `json:"overturned_retention_days"`
	ErrorRetentionDays      int `json:"error_retention_days"`
	PassRetentionDays       int `json:"pass_retention_days"`

	AutoWatch bool `json:"auto_watch"`
	WatchDays int  `json:"watch_days"`
	// EscalateViolations violations within EscalateWindowSeconds put a user
	// at the enforce level (0 = off; CONTRACTS §72.8 0.10.0).
	EscalateViolations    int `json:"escalate_violations"`
	EscalateWindowSeconds int `json:"escalate_window_seconds"`

	BanThreshold     int `json:"ban_threshold"`
	BanWindowSeconds int `json:"ban_window_seconds"`
	BanDurationHours int `json:"ban_duration_hours"`

	JevDefaultSet *JevQuestions `json:"jev_default_set"`

	// Statistics, alerts and notifications (CONTRACTS §72.14).
	ErrorRateAlertPercent int    `json:"error_rate_alert_percent"`
	StatsRetentionDays    int    `json:"stats_retention_days"`
	NotifyWebhookURL      string `json:"notify_webhook_url"`
	NotifyWebhookSecret   string `json:"notify_webhook_secret"`

	// lists: the retired user lists, read only by the one-time import into
	// locked watch entries (upgrade.go).
	lists retiredLists
	// setup01: the settings hold a 0.1 single-model setup (base_url and
	// model), which 0.11.0 no longer converts (upgradeOnce logs it).
	setup01 bool
}

// retiredLists are the user lists of the settings before 0.11.0: the
// allowlist (allowlist_user_ids, or the older exempt_user_ids while that key
// is absent) and the blocklist, which wins. invalid counts the entries that
// are no user id.
type retiredLists struct {
	allow, block []int64
	invalid      int
}

// DefaultBlockMessage is the default deny message of a hit.
const DefaultBlockMessage = "Your request was blocked by content moderation. Please revise your input and try again."

// input_overflow: what a request with more chunks than input_max_chunks
// gets (CONTRACTS §72.4).
const (
	OverflowSample = "sample" // the first, the last and evenly spread chunks run
	OverflowBlock  = "block"  // pre: refused; post: sampled like OverflowSample
)

// hit_replay: what a request holding a cached hit set gets.
const (
	// HitReplayNewContent: the hit is replayed (a pre request is refused,
	// replayMessage) and, after the request, what is beside the hit set is
	// moderated.
	HitReplayNewContent = "new_content"
	// HitReplayWhole: the replay decides the whole request (0.9.3).
	HitReplayWhole = "whole"
)

// fail_policy: what a request whose moderation failed gets (failResponse).
const (
	FailOpen     = "open"      // it passes, recorded as an error
	FailClosePre = "close_pre" // a pre request is refused with 503
)

// MsgHistoryHit follows the deny message of a replayed hit under hit_replay
// = new_content: the hit is in content the request sent again - the history
// of a conversation, mostly - so rewording the new message does not help.
const MsgHistoryHit = "The conversation history contains content that was already flagged; please clear the context and try again."

// DefaultSettings returns the defaults of every setting.
func DefaultSettings() Settings {
	return Settings{
		Mode:          ModeOff,
		InputMaxChars: 12000, MinChars: 2,
		VerdictCacheDays: 7, CacheEntries: 100000,
		MaxConcurrency: 16, QueueSize: 1000, TTFTTimeoutMs: 10000,
		InputMaxChunks: 8, InputOverflow: OverflowSample, InputMaxImages: 8, MaxInflightMB: 128, ErrorTextMaxChars: 2000,
		BlockStatus: 403, BlockMessage: DefaultBlockMessage, HitReplay: HitReplayNewContent, FailPolicy: FailOpen,
		RecordOverturned: true, RecordErrors: true, StoreText: true, RecordPassInputMessage: true,
		HitRetentionDays: 180, OverturnedRetentionDays: 30, ErrorRetentionDays: 7, PassRetentionDays: 3,
		AutoWatch: true, WatchDays: 30, EscalateWindowSeconds: 3600,
		BanWindowSeconds: 86400, BanDurationHours: 24,
		ErrorRateAlertPercent: 5, StatsRetentionDays: 90,
	}
}

var (
	categoryIDRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	baseURLRe    = regexp.MustCompile(`^https?://\S+$`)
)

// DecodeSettings is lenient: unknown keys and values of the wrong type are
// ignored, numbers are clamped into range. Only a non-object is an error.
func DecodeSettings(raw []byte) (Settings, error) {
	s := DefaultSettings()
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return s, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return s, err
	}
	d := decoder{m: m}
	d.general(&s)
	d.storage(&s)
	d.watch(&s)
	d.notify(&s)
	d.retired(&s)
	return s, nil
}

// decoder reads one raw settings document into a Settings.
type decoder struct {
	m map[string]json.RawMessage
}

func (d decoder) str(k string, dst *string) {
	if v, ok := d.m[k]; ok {
		var x string
		if json.Unmarshal(v, &x) == nil {
			*dst = x
		}
	}
}

func (d decoder) num(k string, dst *float64) bool {
	if v, ok := d.m[k]; ok {
		var x float64
		if json.Unmarshal(v, &x) == nil && !math.IsNaN(x) && !math.IsInf(x, 0) {
			*dst = x
			return true
		}
	}
	return false
}

func (d decoder) integer(k string, dst *int, lo, hi int) {
	var f float64
	if d.num(k, &f) {
		*dst = clampInt(int(math.Round(math.Max(math.Min(f, 1e12), -1e12))), lo, hi)
	} else {
		*dst = clampInt(*dst, lo, hi)
	}
}

func (d decoder) boolean(k string, dst *bool) {
	if v, ok := d.m[k]; ok {
		var x bool
		if json.Unmarshal(v, &x) == nil {
			*dst = x
		}
	}
}

func (d decoder) strList(k string) []string {
	var out []string
	if v, ok := d.m[k]; ok {
		var xs []any
		if json.Unmarshal(v, &xs) == nil {
			for _, x := range xs {
				switch t := x.(type) {
				case string:
					out = append(out, t)
				case float64:
					out = append(out, strconv.FormatFloat(t, 'f', -1, 64))
				}
			}
		}
	}
	return out
}

// has: the key holds a value (null counts as absent).
func (d decoder) has(k string) bool {
	v, ok := d.m[k]
	return ok && string(v) != "null"
}

// userIDs reads a list of user ids (strings or numbers); invalid counts the
// entries that are no positive integer.
func (d decoder) userIDs(k string) (ids []int64, invalid int) {
	for _, u := range d.strList(k) {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		if id, err := strconv.ParseInt(u, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		} else {
			invalid++
		}
	}
	return ids, invalid
}

// retiredKey is a key 0.11.0 removed from the settings that is still read
// while its successor is absent ("" = none: always). The core keeps the
// stored values until the settings are saved again, and the console drops
// keys the plugin no longer accepts when it saves, so only settings not saved
// since 0.11.0 hold them (CONTRACTS §72 0.11.0).
type retiredKey struct {
	key, successor string
	read           func(d decoder, s *Settings)
}

// retiredKeys are the only retired keys read; nothing else of them is.
var retiredKeys = []retiredKey{
	// 0 keeps the verdict cache off, anything else is rounded up to whole
	// days (at most 7, the old maximum of 604800 seconds).
	{"cache_ttl_seconds", "verdict_cache_days", func(d decoder, s *Settings) {
		var ttl float64
		if d.num("cache_ttl_seconds", &ttl) {
			s.VerdictCacheDays = clampInt(int(math.Ceil(min(ttl, 604800)/86400)), 0, 7)
		}
	}},
	{"ban_window_hours", "ban_window_seconds", func(d decoder, s *Settings) {
		hours := 24
		d.integer("ban_window_hours", &hours, 1, 8760)
		s.BanWindowSeconds = hours * 3600
	}},
	// 0.1 kept every record retention_days: settings without
	// hit_retention_days keep the hits at least as long (as 0.10.0 did).
	{"retention_days", "hit_retention_days", func(d decoder, s *Settings) {
		days := 0
		d.integer("retention_days", &days, 1, 3650)
		s.HitRetentionDays = max(s.HitRetentionDays, days)
	}},
	// The user lists, only for the one-time import (upgrade.go).
	{"allowlist_user_ids", "", func(d decoder, s *Settings) { s.lists.readAllow(d, "allowlist_user_ids") }},
	{"exempt_user_ids", "allowlist_user_ids", func(d decoder, s *Settings) { s.lists.readAllow(d, "exempt_user_ids") }},
	{"blocklist_user_ids", "", func(d decoder, s *Settings) {
		var invalid int
		s.lists.block, invalid = d.userIDs("blocklist_user_ids")
		s.lists.invalid += invalid
	}},
}

func (l *retiredLists) readAllow(d decoder, key string) {
	var invalid int
	l.allow, invalid = d.userIDs(key)
	l.invalid += invalid
}

// retired applies retiredKeys, and notes a 0.1 setup (setup01).
func (d decoder) retired(s *Settings) {
	for _, r := range retiredKeys {
		if d.has(r.key) && (r.successor == "" || !d.has(r.successor)) {
			r.read(d, s)
		}
	}
	var base, model string
	d.str("base_url", &base)
	d.str("model", &model)
	s.setup01 = strings.TrimSpace(base) != "" && strings.TrimSpace(model) != ""
}

func (d decoder) enum(k string, dst *string, allowed ...string) {
	var x string
	d.str(k, &x)
	x = strings.ToLower(strings.TrimSpace(x))
	for _, a := range allowed {
		if x == a {
			*dst = x
			return
		}
	}
}

func (d decoder) general(s *Settings) {
	d.enum("mode", &s.Mode, ModeOff, ModeObserve, ModeEnforce)
	d.integer("input_max_chars", &s.InputMaxChars, 256, 100000)
	d.integer("min_chars", &s.MinChars, 0, 1000)
	d.integer("verdict_cache_days", &s.VerdictCacheDays, 0, 30)
	d.integer("cache_entries", &s.CacheEntries, 1000, 1000000)
	d.integer("max_concurrency", &s.MaxConcurrency, 1, 256)
	d.integer("ttft_timeout_ms", &s.TTFTTimeoutMs, 1000, 60000)
	d.integer("queue_size", &s.QueueSize, 1, 100000)
	d.integer("input_max_chunks", &s.InputMaxChunks, 1, 32)
	d.enum("input_overflow", &s.InputOverflow, OverflowSample, OverflowBlock)
	d.integer("input_max_images", &s.InputMaxImages, 0, 32)
	d.integer("max_inflight_mb", &s.MaxInflightMB, 16, 4096)
	d.integer("error_text_max_chars", &s.ErrorTextMaxChars, 0, 100000)
	d.integer("block_status", &s.BlockStatus, 400, 599)
	d.str("block_message", &s.BlockMessage)
	s.BlockMessage = textutil.TruncateRunes(strings.TrimSpace(s.BlockMessage), 500)
	if s.BlockMessage == "" {
		s.BlockMessage = DefaultBlockMessage
	}
	d.enum("hit_replay", &s.HitReplay, HitReplayNewContent, HitReplayWhole)
	d.enum("fail_policy", &s.FailPolicy, FailOpen, FailClosePre)
	if v, ok := d.m["jev_default_set"]; ok {
		var q JevQuestions
		if json.Unmarshal(v, &q) == nil && q.Validate() == nil {
			s.JevDefaultSet = &q
		}
	}
}

func (d decoder) storage(s *Settings) {
	d.boolean("record_pass", &s.RecordPass)
	d.boolean("record_overturned", &s.RecordOverturned)
	d.boolean("record_errors", &s.RecordErrors)
	d.boolean("store_text", &s.StoreText)
	d.boolean("record_pass_input_message", &s.RecordPassInputMessage)
	d.integer("hit_retention_days", &s.HitRetentionDays, 1, 3650)
	d.integer("overturned_retention_days", &s.OverturnedRetentionDays, 1, 3650)
	d.integer("error_retention_days", &s.ErrorRetentionDays, 1, 3650)
	d.integer("pass_retention_days", &s.PassRetentionDays, 1, 3)
}

func (d decoder) watch(s *Settings) {
	d.boolean("auto_watch", &s.AutoWatch)
	d.integer("watch_days", &s.WatchDays, 1, 365)
	d.integer("escalate_violations", &s.EscalateViolations, 0, 1000)
	d.integer("escalate_window_seconds", &s.EscalateWindowSeconds, 1, 31536000)
	d.integer("ban_threshold", &s.BanThreshold, 0, 1000)
	d.integer("ban_window_seconds", &s.BanWindowSeconds, 1, 31536000)
	d.integer("ban_duration_hours", &s.BanDurationHours, 0, 87600)
}

func (d decoder) notify(s *Settings) {
	d.integer("error_rate_alert_percent", &s.ErrorRateAlertPercent, 0, 100)
	d.integer("stats_retention_days", &s.StatsRetentionDays, 7, 3650)
	d.str("notify_webhook_url", &s.NotifyWebhookURL)
	s.NotifyWebhookURL = strings.TrimSpace(s.NotifyWebhookURL)
	if s.NotifyWebhookURL != "" && (!baseURLRe.MatchString(s.NotifyWebhookURL) || utf8.RuneCountInString(s.NotifyWebhookURL) > 1000) {
		s.NotifyWebhookURL = "" // ValidateSettings rejects it on save (CheckSettings); a stored bad value turns notices off
	}
	d.str("notify_webhook_secret", &s.NotifyWebhookSecret)
	s.NotifyWebhookSecret = textutil.TruncateRunes(strings.TrimSpace(s.NotifyWebhookSecret), 200)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// config is the compiled, immutable runtime view of Settings.
type config struct {
	Settings

	enabled      bool   // mode != off
	defaultLevel string // level of a user without a watch entry (mode)
	settingsHash string
	cacheTTL     time.Duration
	ttft         time.Duration
	banWindow    time.Duration
	banDuration  time.Duration // 0 = until unblocked
	watchFor     time.Duration
	retention    map[string]time.Duration // by result
	jevDefault   JevQuestions
	limits       planLimits // chunking of the moderated content
	maxInflight  int64      // bytes of hook requests in progress (max_inflight_mb)

	// escalateWindow: escalate_window_seconds (escalates).
	escalateWindow time.Duration
}

// compile derives the runtime config from settings.
func compile(s Settings) *config {
	c := &config{Settings: s}
	c.enabled = s.Mode != ModeOff
	c.limits = planLimits{maxChars: s.InputMaxChars, maxChunks: s.InputMaxChunks, maxImages: s.InputMaxImages}
	c.maxInflight = int64(s.MaxInflightMB) << 20
	c.defaultLevel = LevelObserve
	if s.Mode == ModeEnforce {
		c.defaultLevel = LevelEnforce
	}
	c.cacheTTL = time.Duration(s.VerdictCacheDays) * 24 * time.Hour
	c.ttft = time.Duration(s.TTFTTimeoutMs) * time.Millisecond
	c.banWindow = time.Duration(s.BanWindowSeconds) * time.Second
	c.escalateWindow = time.Duration(s.EscalateWindowSeconds) * time.Second
	c.banDuration = time.Duration(s.BanDurationHours) * time.Hour
	c.watchFor = time.Duration(s.WatchDays) * 24 * time.Hour
	day := 24 * time.Hour
	c.retention = map[string]time.Duration{
		ResultBlock: time.Duration(s.HitRetentionDays) * day, ResultHit: time.Duration(s.HitRetentionDays) * day,
		ResultOverturned: time.Duration(s.OverturnedRetentionDays) * day,
		ResultError:      time.Duration(s.ErrorRetentionDays) * day, ResultPass: time.Duration(s.PassRetentionDays) * day,
	}
	c.jevDefault, _ = DefaultQuestions(s.JevDefaultSet)
	return c
}

// replayMessage is the deny message of a replayed hit.
func (c *config) replayMessage() string {
	if c.HitReplay == HitReplayWhole {
		return c.BlockMessage
	}
	return c.BlockMessage + " " + MsgHistoryHit
}

// retentionCut reports whether c keeps some records or statistics for a
// shorter time than old.
func (c *config) retentionCut(old *config) bool {
	for result, keep := range c.retention {
		if keep < old.retention[result] {
			return true
		}
	}
	return c.StatsRetentionDays < old.StatsRetentionDays
}

// settingsDigest is a short sha256 of the raw settings JSON (tells nodes
// apart while a change propagates).
func settingsDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// globMatch matches s against pattern with * (any run) and ? (one rune).
func globMatch(pattern, s string) bool {
	px, sx := 0, 0
	nextPx, nextSx := -1, -1
	for px < len(pattern) || sx < len(s) {
		if px < len(pattern) {
			switch c := pattern[px]; c {
			case '*':
				nextPx, nextSx = px, sx+1
				px++
				continue
			case '?':
				if sx < len(s) {
					_, n := utf8.DecodeRuneInString(s[sx:])
					px++
					sx += n
					continue
				}
			default:
				if sx < len(s) && s[sx] == c {
					px++
					sx++
					continue
				}
			}
		}
		if nextSx > 0 && nextSx <= len(s) {
			px, sx = nextPx, nextSx
			continue
		}
		return false
	}
	return true
}
