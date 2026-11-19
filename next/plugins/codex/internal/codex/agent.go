package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// The client headers (User-Agent, originator) every request of an account
// sends to OpenAI: the upstream (HTTP and WebSocket), the account test, the
// token refresh, the usage resets and the authorizations. Each is the
// account's own value (credentials user_agent / originator), else the
// plugin settings' value, else the built-in one (UserAgent, Originator). An
// empty value sets nothing and the next level applies. An authorization
// runs before the account is saved: its account values are the form's, which
// each route call carries (readCall).

// agentFields are the credential fields of the account's own values.
var agentFields = []string{"user_agent", "originator"}

// agent is the User-Agent and originator one account sends.
type agent struct {
	UserAgent  string
	Originator string
}

// agent resolves the client headers of the account with credentials c (for
// an authorization, the form's values).
func (p *Plugin) agent(c credentials) agent {
	s := p.settings.Load()
	return agent{
		UserAgent:  cmp.Or(c.UserAgent, s.UserAgent, UserAgent),
		Originator: cmp.Or(c.Originator, s.Originator, Originator),
	}
}

// Settings are the plugin settings: the client headers of the accounts that
// set none, "" for the built-in ones.
type Settings struct {
	UserAgent  string `json:"user_agent"`
	Originator string `json:"originator"`
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

// settingsRules are the keys of the settings (the console form,
// ui/native CodexSettings); every other key is refused. Their values are
// checked as an account's own (agentError).
var settingsRules = fieldcheck.Fields{
	"user_agent": fieldcheck.String(),
	"originator": fieldcheck.String(),
}

// parseSettings decodes and checks the settings: the one check of Configure
// and ValidateSettings.
func parseSettings(cfg pluginsdk.Config) (*Settings, error) {
	var m credMap
	if err := fieldcheck.DecodeSettings(cfg, settingsRules, &m); err != nil {
		return nil, err
	}
	if k, code, msg := m.agentError(); k != "" {
		return nil, pluginsdk.FieldErrors{}.Add(k, code, msg).Err()
	}
	ua, _ := m.str("user_agent")
	originator, _ := m.str("originator")
	return &Settings{UserAgent: strings.TrimSpace(ua), Originator: strings.TrimSpace(originator)}, nil
}
