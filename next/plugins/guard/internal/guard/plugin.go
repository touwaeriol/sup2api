// Package guard implements the guard demo plugin: a gateway.request hook that
// blocks prompts matching keyword/regex rules, usage statistics from
// usage.recorded events, rollup/cleanup jobs, admin routes for rules and
// stats, and webhook alerts.
package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/batch"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// Settings is the plugin configuration (settingsRules).
type Settings struct {
	WebhookURL     string `json:"webhook_url"`
	RecordSnippets bool   `json:"record_snippets"`
	// AlertCooldownSec: per rule, at most one webhook alert per this many
	// seconds across the cluster (throttle.go). 0 = an alert per block.
	AlertCooldownSec int `json:"alert_cooldown_sec"`
}

// alertCooldown is AlertCooldownSec as a duration.
func (s *Settings) alertCooldown() time.Duration {
	return time.Duration(s.AlertCooldownSec) * time.Second
}

// TopicRulesChanged is broadcast to the guard instances on the other nodes
// after PUT /rules succeeded; they reload the rules at once. Broadcasts are
// best effort, so the periodic reload (reloadEvery) stays as the fallback.
const TopicRulesChanged = "rules.changed"

// Plugin is the guard plugin. It implements pluginsdk.Hook, pluginsdk.App,
// pluginsdk.HTTP, pluginsdk.BroadcastHandler, pluginsdk.SettingsValidator
// and the lifecycle interfaces.
type Plugin struct {
	*pluginsdk.Router
	*pluginsdk.BroadcastMux

	host          pluginsdk.Host
	log           *slog.Logger
	settings      atomic.Pointer[Settings]
	rules         atomic.Pointer[ruleSet]
	ruleReloadMu  sync.Mutex
	ruleReloadSeq uint64
	now           func() time.Time

	// reloadEvery is the rule refresh period (other nodes may change rules).
	reloadEvery time.Duration

	blockWriter *batch.Writer[blockEvent]
	alerts      chan alert
	throttle    alertThrottle

	stats struct {
		checked, blocked, droppedBlocks, droppedAlerts, alertErrors, alertsThrottled atomic.Int64
	}

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// debugRoutes is set by the guardtest build (debug_guardtest.go).
var debugRoutes func(p *Plugin)

// New returns the plugin.
func New() *Plugin {
	p := &Plugin{
		Router:       pluginsdk.NewRouter(),
		BroadcastMux: pluginsdk.NewBroadcastMux(),
		now:          time.Now,
		reloadEvery:  5 * time.Second,
		alerts:       make(chan alert, 256),
		log:          slog.Default(),
	}
	p.settings.Store(&Settings{AlertCooldownSec: defaultAlertCooldownSec})
	p.rules.Store(&ruleSet{})
	p.Handle("GET", "/rules", p.getRules)
	p.Handle("PUT", "/rules", p.putRules)
	p.Handle("GET", "/stats", p.getStats)
	p.OnTopic(TopicRulesChanged, p.onRulesChanged)
	if debugRoutes != nil {
		debugRoutes(p)
	}
	return p
}

// Init implements pluginsdk.Initializer: loads rules and starts the
// background workers.
func (p *Plugin) Init(ctx context.Context, h pluginsdk.Host) error {
	p.host = h
	p.log = h.Logger()
	if err := p.reloadRules(ctx); err != nil {
		// Keep running with no rules; the refresher retries.
		p.log.Warn("guard: initial rule load failed", "error", err.Error())
	}
	bg, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	// Start batch writer for block events
	p.blockWriter = batch.New(bg, 1024, func(ctx context.Context, events []blockEvent) error {
		if err := p.writeBlocks(ctx, events); err != nil {
			p.log.Warn("guard: write block log failed", "error", err.Error(), "events", len(events))
			return err
		}
		return nil
	}, func(n int) {
		p.stats.droppedBlocks.Add(int64(n))
	})

	p.wg.Add(2)
	go p.refreshLoop(bg)
	go p.alertSender(bg)
	return nil
}

// Configure implements pluginsdk.Configurer.
func (p *Plugin) Configure(_ context.Context, cfg pluginsdk.Config) error {
	s, err := parseSettings(cfg)
	if err != nil {
		return err
	}
	p.settings.Store(s)
	return nil
}

// ValidateSettings implements pluginsdk.SettingsValidator: the console's
// settings are refused before they are stored when Configure would refuse
// them.
func (p *Plugin) ValidateSettings(_ context.Context, cfg pluginsdk.Config) (json.RawMessage, error) {
	_, err := parseSettings(cfg)
	return nil, err
}

// settingsRules are the settings the console form edits (ui/native
// GuardSettings shows the same bounds); every other key is refused.
var settingsRules = fieldcheck.Fields{
	"webhook_url":        fieldcheck.String().Pattern(`^(https?://\S+)?$`),
	"record_snippets":    fieldcheck.Bool(),
	"alert_cooldown_sec": fieldcheck.Integer().Range(0, float64(maxAlertCooldownSec)),
}

// parseSettings decodes and checks the settings, an absent cooldown taking
// its default: the one check of Configure and ValidateSettings.
func parseSettings(cfg pluginsdk.Config) (*Settings, error) {
	s := Settings{AlertCooldownSec: defaultAlertCooldownSec} // kept when the key is absent
	if err := fieldcheck.DecodeSettings(cfg, settingsRules, &s); err != nil {
		return nil, err
	}
	s.WebhookURL = strings.TrimSpace(s.WebhookURL)
	if s.WebhookURL != "" {
		u, err := url.Parse(s.WebhookURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, pluginsdk.FieldErrors{}.Add("webhook_url", "format", "webhook_url must be an http(s) URL / webhook 地址必须是 http(s) URL").Err()
		}
	}
	return &s, nil
}

// Health implements pluginsdk.HealthChecker.
func (p *Plugin) Health(context.Context) (*pluginv1.HealthResponse, error) {
	rs := p.rules.Load()
	return &pluginv1.HealthResponse{Healthy: true, Metrics: map[string]float64{
		"rules":            float64(len(rs.rules)),
		"checked":          float64(p.stats.checked.Load()),
		"blocked":          float64(p.stats.blocked.Load()),
		"dropped_blocks":   float64(p.stats.droppedBlocks.Load()),
		"dropped_alerts":   float64(p.stats.droppedAlerts.Load()),
		"alert_errors":     float64(p.stats.alertErrors.Load()),
		"alerts_throttled": float64(p.stats.alertsThrottled.Load()),
	}}, nil
}

// Shutdown implements pluginsdk.Shutdowner: flushes pending block records.
func (p *Plugin) Shutdown(ctx context.Context) error {
	if p.cancel == nil {
		return nil
	}
	p.cancel()

	// Stop the batch writer (has its own timeout)
	if err := p.blockWriter.Stop(); err != nil {
		return err
	}

	// Wait for other workers (refreshLoop, alertSender)
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("guard: workers did not stop in time")
	}
}

// db returns the plugin database handle, bound to ctx.
func (p *Plugin) db(ctx context.Context) (*gorm.DB, error) {
	if p.host == nil {
		return nil, errors.New("guard: host not initialised")
	}
	db, err := p.host.DB(ctx)
	if err != nil {
		return nil, fmt.Errorf("guard: database unavailable: %w", err)
	}
	return db, nil
}

func (p *Plugin) refreshLoop(ctx context.Context) {
	defer p.wg.Done()
	t := time.NewTicker(p.reloadEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := p.reloadRules(rctx); err != nil && ctx.Err() == nil {
				p.log.Debug("guard: rule reload failed", "error", err.Error())
			}
			cancel()
		}
	}
}

// publishRulesChanged tells the other nodes to reload the rules. Failures
// are only logged: the periodic reload catches up within reloadEvery.
func (p *Plugin) publishRulesChanged(ctx context.Context) {
	if p.host == nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := p.host.Publish(pctx, TopicRulesChanged, nil); err != nil {
		p.log.Warn("guard: broadcast rules.changed failed; other nodes reload within the polling period", "error", err.Error())
	}
}

// onRulesChanged handles a rules.changed broadcast from another node.
func (p *Plugin) onRulesChanged(ctx context.Context, b pluginsdk.Broadcast) error {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.reloadRules(rctx); err != nil {
		p.log.Warn("guard: reload after rules.changed failed", "source_node", b.SourceNodeID, "error", err.Error())
		return err
	}
	return nil
}
