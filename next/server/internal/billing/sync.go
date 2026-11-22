package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Price sync source kinds (CONTRACTS §17).
const (
	KindLiteLLM   = "litellm"    // LiteLLM model_prices_and_context_window.json
	KindModelsDev = "models_dev" // models.dev api.json
	KindSup2API   = "sup2api"    // an upstream sup2api, read with one of its API keys
)

// Default URLs and provider filters of the public price catalogs.
const (
	DefaultLiteLLMURL   = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	DefaultModelsDevURL = "https://models.dev/api.json"
)

var (
	defaultLiteLLMProviders   = []string{"anthropic", "openai", "gemini"}
	defaultModelsDevProviders = []string{"anthropic", "openai", "google"}
)

const (
	maxSourceBytes  = 64 << 20
	sourceAPIKeyAAD = "price-source:api-key"
	// longContextLen is the prompt length above which the catalogs' long
	// context prices apply (len counts input plus cache tokens).
	longContextLen = 200000
)

// SyncDeps enables price sync sources and the API-key price export.
type SyncDeps struct {
	Cipher *secret.Cipher           // encrypts upstream sup2api API keys
	Keys   core.APIKeyAuthenticator // authenticates GET /key/prices
	HTTP   *http.Client             // fetches sources (default: 60s timeout)
}

// SetSyncDeps wires the sync dependencies (app assembly).
func (s *Service) SetSyncDeps(d SyncDeps) {
	if d.HTTP == nil {
		d.HTTP = netguard.NewClient(netguard.ClientOptions{Timeout: 60 * time.Second})
	}
	s.sync = d
}

// PriceSource is the API view of a price_sync_sources row.
type PriceSource struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	URL          string          `json:"url"`
	HasAPIKey    bool            `json:"has_api_key"`
	Options      json.RawMessage `json:"options"`
	Enabled      bool            `json:"enabled"`
	LastSyncedAt *time.Time      `json:"last_synced_at"`
	LastError    string          `json:"last_error"`
	PriceCount   int64           `json:"price_count"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`

	apiKeyEnc []byte
}

// sourceOptions is the union of the per-kind options.
type sourceOptions struct {
	Providers       []string `json:"providers,omitempty"`        // litellm, models_dev
	ApplyMultiplier *bool    `json:"apply_multiplier,omitempty"` // sup2api (default true)
}

const sourceColumns = `ps.id, ps.name, ps.kind, ps.url, ps.api_key_enc, ps.options, ps.enabled,
	ps.last_synced_at, ps.last_error, ps.created_at, ps.updated_at,
	(SELECT count(*) FROM model_prices mp WHERE mp.sync_source_id = ps.id) AS price_count`

// sources selects sourceColumns of the price sources (alias ps).
func sources(db *gorm.DB) *gorm.DB {
	return db.Table("price_sync_sources ps").Select(sourceColumns)
}

// sourceRow is a row of sources.
type sourceRow struct {
	model.PriceSyncSource
	PriceCount int64
}

func (r *sourceRow) source() *PriceSource {
	return &PriceSource{ID: r.ID, Name: r.Name, Kind: r.Kind, URL: r.URL, HasAPIKey: len(r.APIKeyEnc) > 0,
		Options: r.Options, Enabled: core.ValueOr(r.Enabled, false), LastSyncedAt: r.LastSyncedAt, LastError: r.LastError,
		PriceCount: r.PriceCount, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, apiKeyEnc: r.APIKeyEnc}
}

func (s *Service) getSource(ctx context.Context, q *gorm.DB, id int64) (*PriceSource, error) {
	var row sourceRow
	err := sources(store.G(ctx, q)).Where("ps.id = ?", id).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("price source not found")
	}
	if err != nil {
		return nil, err
	}
	return row.source(), nil
}

func (s *Service) registerSyncRoutes(r *httpapi.Router) {
	r.Perm("GET", "/price-sources", "price:read", s.listSources)
	r.Perm("POST", "/price-sources", "price:manage", s.createSource)
	r.Perm("PATCH", "/price-sources/:id", "price:manage", s.updateSource)
	r.Perm("DELETE", "/price-sources/:id", "price:manage", s.deleteSource)
	r.Perm("POST", "/price-sources/:id/preview", "price:manage", s.previewSync)
	r.Perm("POST", "/price-sources/:id/apply", "price:manage", s.applySync)
	// Downstream sup2api instances read our prices with one of our API keys.
	r.Public("GET", "/key/prices", s.keyPrices)
	r.Perm("GET", "/me/prices", "apikey:self:manage", s.myPrices)
}

func (s *Service) listSources(c *gin.Context) {
	ctx := c.Request.Context()
	var rows []sourceRow
	if err := sources(store.G(ctx, s.db.Gorm)).Order("ps.id").Scan(&rows).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]*PriceSource, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].source())
	}
	httpapi.OK(c, out)
}

// sourceInput is the body of POST/PATCH /price-sources. api_key: omitted or
// null keeps the stored key, "" clears it.
type sourceInput struct {
	Name    *string         `json:"name"`
	Kind    *string         `json:"kind"`
	URL     *string         `json:"url"`
	APIKey  *string         `json:"api_key"`
	Options json.RawMessage `json:"options"`
	Enabled *bool           `json:"enabled"`
}

// validateSource checks a complete source definition and returns its
// normalized options.
func validateSource(name, kind, rawURL string, hasKey bool, options json.RawMessage) (json.RawMessage, error) {
	fields := sourceFields(name, kind, rawURL, hasKey)
	o, more := normalizeOptions(kind, options)
	if fields = append(fields, more...); len(fields) > 0 {
		return nil, core.InvalidFields(fields...)
	}
	b, _ := json.Marshal(o)
	return b, nil
}

// sourceFields are the field errors of a source's name, kind, URL and key.
func sourceFields(name, kind, rawURL string, hasKey bool) []core.FieldError {
	var fields []core.FieldError
	add := func(f, code, msg string) {
		fields = append(fields, core.FieldError{Field: f, Code: code, Message: msg})
	}
	if n := strings.TrimSpace(name); n == "" || len(n) > 100 {
		add("name", "invalid", "1-100 characters")
	}
	switch kind {
	case KindLiteLLM, KindModelsDev, KindSup2API:
	default:
		add("kind", "invalid", "litellm, models_dev or sup2api")
	}
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("url", "invalid", "an absolute http(s) URL")
	}
	if kind == KindSup2API && !hasKey {
		add("api_key", "required", "an API key of the upstream sup2api is required")
	}
	return fields
}

// normalizeOptions decodes the options of a source of kind, keeping only
// what the kind reads, with their field errors.
func normalizeOptions(kind string, options json.RawMessage) (sourceOptions, []core.FieldError) {
	var o sourceOptions
	var fields []core.FieldError
	if len(options) > 0 && string(options) != "null" {
		dec := json.NewDecoder(strings.NewReader(string(options)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&o); err != nil {
			fields = append(fields, core.FieldError{Field: "options", Code: "invalid", Message: err.Error()})
		}
	}
	switch kind {
	case KindLiteLLM, KindModelsDev:
		o.ApplyMultiplier = nil
		if slices.ContainsFunc(o.Providers, func(p string) bool { return strings.TrimSpace(p) == "" }) {
			fields = append(fields, core.FieldError{Field: "options.providers", Code: "invalid", Message: "provider ids must not be empty"})
		}
	case KindSup2API:
		o.Providers = nil
		if o.ApplyMultiplier == nil {
			t := true
			o.ApplyMultiplier = &t
		}
	}
	return o, fields
}

func (s *Service) encryptKey(key string) ([]byte, error) {
	if s.sync.Cipher == nil {
		return nil, core.ErrUnavailable.WithMessage("price source keys cannot be stored on this node")
	}
	return s.sync.Cipher.Encrypt([]byte(key), []byte(sourceAPIKeyAAD))
}

func (s *Service) createSource(c *gin.Context) {
	ctx := c.Request.Context()
	var in sourceInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	key := strings.TrimSpace(deref(in.APIKey))
	opts, err := validateSource(deref(in.Name), deref(in.Kind), strings.TrimSpace(deref(in.URL)), key != "", in.Options)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	enc, err := s.sourceKey(key)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	// Every column the INSERT names is written: a nil key is NULL, enabled
	// false is false.
	row := model.PriceSyncSource{Name: strings.TrimSpace(*in.Name), Kind: *in.Kind, URL: strings.TrimSpace(*in.URL),
		APIKeyEnc: enc, Options: opts, Enabled: &enabled}
	err = store.G(ctx, s.db.Gorm).Create(&row).Error
	s.replySource(c, row.ID, err, httpapi.Created)
}

// sourceKey is the stored form of an API key: nil for none.
func (s *Service) sourceKey(key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	return s.encryptKey(key)
}

// replySource answers a source write with the source id: a second source of
// the name is a conflict.
func (s *Service) replySource(c *gin.Context, id int64, err error, reply func(*gin.Context, any)) {
	if store.IsUniqueViolation(err, "") {
		err = core.ErrConflict.WithMessage("a price source with this name already exists")
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	p, err := s.getSource(c.Request.Context(), s.db.Gorm, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	reply(c, p)
}

func (s *Service) updateSource(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in sourceInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := s.patchedSource(ctx, id, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = store.G(ctx, s.db.Gorm).Model(&model.PriceSyncSource{}).Where("id = ?", id).Updates(map[string]any{
		"name": src.Name, "kind": src.Kind, "url": src.URL, "api_key_enc": src.apiKeyEnc, "options": src.Options, "enabled": src.Enabled,
		"updated_at": gorm.Expr("now()"),
	}).Error
	s.replySource(c, id, err, httpapi.OK)
}

// patchedSource is the source id after the PATCH body in, checked: a field
// left out keeps its value, api_key "" removes the key.
func (s *Service) patchedSource(ctx context.Context, id int64, in sourceInput) (*PriceSource, error) {
	src, err := s.getSource(ctx, s.db.Gorm, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		src.Name = strings.TrimSpace(*in.Name)
	}
	if in.URL != nil {
		src.URL = strings.TrimSpace(*in.URL)
	}
	if in.Options != nil {
		src.Options = in.Options
	}
	src.Kind, src.Enabled = core.ValueOr(in.Kind, src.Kind), core.ValueOr(in.Enabled, src.Enabled)
	if in.APIKey != nil {
		if src.apiKeyEnc, err = s.sourceKey(strings.TrimSpace(*in.APIKey)); err != nil {
			return nil, err
		}
	}
	src.Options, err = validateSource(src.Name, src.Kind, src.URL, len(src.apiKeyEnc) > 0, src.Options)
	return src, err
}

// deleteSource keeps the prices imported from the source (sync_source_id
// becomes NULL).
func (s *Service) deleteSource(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	res := store.G(ctx, s.db.Gorm).Where("id = ?", id).Delete(&model.PriceSyncSource{})
	if res.Error != nil {
		httpapi.Fail(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("price source not found"))
		return
	}
	s.changed(ctx, "prices")
	httpapi.NoContent(c)
}

// setSourceStatus records the outcome of a fetch or an import on the source
// id; its updated_at stays, an edit is not.
func setSourceStatus(db *gorm.DB, id int64, set map[string]any) *gorm.DB {
	return db.Model(&model.PriceSyncSource{}).Where("id = ?", id).Updates(set)
}
