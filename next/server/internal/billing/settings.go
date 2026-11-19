package billing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// settingsKey is the settings row and the plugin-visible section
// (CONTRACTS §107) of the billing settings.
const settingsKey = protocol.SettingsBilling

// Settings is the "billing" row of the settings table (CONTRACTS §8).
type Settings struct {
	PreConsumeTokens    int64           `json:"pre_consume_tokens"`
	MinBalance          decimal.Decimal `json:"min_balance"`
	BigCostWarningUSD   decimal.Decimal `json:"big_cost_warning_usd"`
	OutputReserveTokens int64           `json:"output_reserve_tokens"`
	MaxOverdraftPerUser decimal.Decimal `json:"max_overdraft_per_user"`
	// ClientDisconnectPolicy is what a request pays when its client hangs up
	// while the upstream is answering (CONTRACTS §25.8): the pre-charge
	// estimate, the real usage after reading the upstream to its end, or
	// nothing. The reading has no timeout of its own: it keeps the
	// upstream exchange's (environment variables, §74.3).
	ClientDisconnectPolicy string `json:"client_disconnect_policy"`
}

// normalizeClientDisconnect makes a stored document usable: a policy outside
// the accepted set becomes the default rather than one nobody chose.
func (v *Settings) normalizeClientDisconnect() {
	if !validDisconnectPolicy(v.ClientDisconnectPolicy) {
		v.ClientDisconnectPolicy = DefaultSettings().ClientDisconnectPolicy
	}
}

func validDisconnectPolicy(p string) bool {
	return p == core.DisconnectPrecharge || p == core.DisconnectContinue || p == core.DisconnectFree
}

// DefaultSettings are used when the row is absent.
func DefaultSettings() Settings {
	return Settings{
		PreConsumeTokens:       500,
		MinBalance:             decimal.Zero,
		BigCostWarningUSD:      decimal.NewFromInt(10),
		OutputReserveTokens:    4000,
		MaxOverdraftPerUser:    decimal.NewFromInt(5),
		ClientDisconnectPolicy: core.DisconnectContinue,
	}
}

type settingsSnapshot struct {
	at time.Time
	v  Settings
}

// Settings returns the cached billing settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return s.settingsIn(ctx, s.db.Gorm)
}

// settingsIn is Settings with a cache miss read through q: a caller in a
// transaction passes it, so that the read takes no second connection. The
// transaction does not write the settings, so it reads what the pool would.
func (s *Service) settingsIn(ctx context.Context, q *gorm.DB) (Settings, error) {
	s.mu.Lock()
	snap := s.settings
	epoch := s.epoch
	s.mu.Unlock()
	if snap != nil && time.Since(snap.at) < s.cacheTTL {
		return snap.v, nil
	}
	v, err := loadSettings(ctx, q)
	if err != nil {
		return DefaultSettings(), err
	}
	s.mu.Lock()
	if s.epoch == epoch {
		s.settings = &settingsSnapshot{at: time.Now(), v: v}
	}
	s.mu.Unlock()
	return v, nil
}

func loadSettings(ctx context.Context, q *gorm.DB) (Settings, error) {
	v := DefaultSettings()
	raw, err := store.SettingValue(store.G(ctx, q), settingsKey)
	if store.IsNoRows(err) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	// Fields absent from the stored document keep their defaults.
	var partial struct {
		PreConsumeTokens       *int64           `json:"pre_consume_tokens"`
		MinBalance             *decimal.Decimal `json:"min_balance"`
		BigCostWarningUSD      *decimal.Decimal `json:"big_cost_warning_usd"`
		OutputReserveTokens    *int64           `json:"output_reserve_tokens"`
		MaxOverdraftPerUser    *decimal.Decimal `json:"max_overdraft_per_user"`
		ClientDisconnectPolicy *string          `json:"client_disconnect_policy"`
	}
	if err := json.Unmarshal(raw, &partial); err != nil {
		return v, err
	}
	if partial.PreConsumeTokens != nil {
		v.PreConsumeTokens = *partial.PreConsumeTokens
	}
	if partial.MinBalance != nil {
		v.MinBalance = *partial.MinBalance
	}
	if partial.BigCostWarningUSD != nil {
		v.BigCostWarningUSD = *partial.BigCostWarningUSD
	}
	if partial.OutputReserveTokens != nil {
		v.OutputReserveTokens = *partial.OutputReserveTokens
	}
	if partial.MaxOverdraftPerUser != nil {
		v.MaxOverdraftPerUser = *partial.MaxOverdraftPerUser
	}
	if partial.ClientDisconnectPolicy != nil {
		v.ClientDisconnectPolicy = *partial.ClientDisconnectPolicy
	}
	v.normalizeClientDisconnect()
	return v, nil
}

// SettingsReaders reads the billing section as plugins see it
// (CONTRACTS §107), with the read and defaults of Settings.
func (s *Service) SettingsReaders() map[string]core.SettingsReader {
	return map[string]core.SettingsReader{settingsKey: func(ctx context.Context) (protocol.SettingsValues, error) {
		v, err := loadSettings(ctx, s.db.Gorm)
		if err != nil {
			return nil, err
		}
		return protocol.BillingSettings{
			MinBalance: v.MinBalance.String(), BigCostWarningUSD: v.BigCostWarningUSD.String(),
			PreConsumeTokens: v.PreConsumeTokens, OutputReserveTokens: v.OutputReserveTokens, ClientDisconnectPolicy: v.ClientDisconnectPolicy,
		}, nil
	}}
}

func (s *Service) registerSettingsRoutes(r *httpapi.Router) {
	r.Perm("GET", "/settings/billing", "settings:read", s.getSettings)
	r.Perm("PUT", "/settings/billing", "settings:manage", s.putSettings)
}

func (s *Service) getSettings(c *gin.Context) {
	v, err := loadSettings(c.Request.Context(), s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}

func (s *Service) putSettings(c *gin.Context) {
	ctx := c.Request.Context()
	cur := DefaultSettings()
	var in struct {
		PreConsumeTokens       *int64           `json:"pre_consume_tokens"`
		MinBalance             *decimal.Decimal `json:"min_balance"`
		BigCostWarningUSD      *decimal.Decimal `json:"big_cost_warning_usd"`
		ClientDisconnectPolicy *string          `json:"client_disconnect_policy"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	var fields []core.FieldError
	if in.PreConsumeTokens != nil {
		if *in.PreConsumeTokens < 0 || *in.PreConsumeTokens > 100000000 {
			fields = append(fields, core.FieldError{Field: "pre_consume_tokens", Code: "invalid", Message: "must be an integer from 0 to 100000000"})
		}
		cur.PreConsumeTokens = *in.PreConsumeTokens
	}
	if in.MinBalance != nil {
		cur.MinBalance = *in.MinBalance
	}
	if in.BigCostWarningUSD != nil {
		if in.BigCostWarningUSD.Sign() <= 0 {
			fields = append(fields, core.FieldError{Field: "big_cost_warning_usd", Code: "invalid", Message: "must be positive"})
		}
		cur.BigCostWarningUSD = *in.BigCostWarningUSD
	}
	if in.ClientDisconnectPolicy != nil {
		if !validDisconnectPolicy(*in.ClientDisconnectPolicy) {
			fields = append(fields, core.FieldError{Field: "client_disconnect_policy", Code: "invalid", Message: "must be precharge, continue or free"})
		}
		cur.ClientDisconnectPolicy = *in.ClientDisconnectPolicy
	}
	if len(fields) > 0 {
		httpapi.Fail(c, core.InvalidFields(fields...))
		return
	}
	uid, _ := core.UserID(ctx)
	if err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.PatchSettingJSONTx(ctx, tx, settingsKey, nullID(uid), in, &cur); err != nil {
			return err
		}
		return core.EmitSettingsChanged(ctx, s.events, tx, settingsKey)
	}); err != nil {
		httpapi.Fail(c, err)
		return
	}
	cur.normalizeClientDisconnect()
	s.changed(ctx, "settings."+settingsKey)
	httpapi.OK(c, cur)
}

func nullID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}
