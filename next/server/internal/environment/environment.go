// Package environment serves the environment variables (CONTRACTS §74.3): the
// global layer (the "environment" row of settings), the declared variables,
// and resolution outside a gateway request. The group and account layers are
// columns of their own tables, edited with the group and the account.
package environment

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// SettingsKey is the settings row of the global layer and its plugin-visible
// section (CONTRACTS §107); config:changed names it as configKey.
const (
	SettingsKey = protocol.SettingsEnvironment
	configKey   = "settings." + SettingsKey
)

// globalTTL bounds how stale the cached global layer gets on a node that
// missed the config:changed broadcast; past it the cached layer is still
// served while one background read refreshes it. errorTTL is how long after a
// failed read the database is left alone; loadTimeout bounds the read, which
// no caller can cancel.
const (
	globalTTL   = 10 * time.Second
	errorTTL    = 2 * time.Second
	loadTimeout = time.Second
)

// Service serves the routes and implements core.EnvironmentResolver.
type Service struct {
	db  *store.DB
	bus core.Bus
	// process is the process layer (config.Config.Environment), the
	// fallback of the global one.
	process core.EnvironmentValues
	// load reads the global layer; tests replace it.
	load func(context.Context) (core.EnvironmentValues, error)
	// Events records settings.changed with a saved global layer; nil
	// records nothing.
	Events core.EventPublisher

	unsubscribe func()

	mu sync.Mutex
	// last is the global layer over the process layer from the last read
	// that succeeded, nil before the first one. A failed read never
	// replaces it: a database hiccup must not put the timeouts
	// back to their defaults.
	last core.EnvironmentValues
	// lastAt is when last was read; zero once invalidate marked it outdated,
	// which makes the next caller wait for a read (a PUT is seen at once).
	lastAt time.Time
	// retryAt holds back reads after a failure until errorTTL passed.
	retryAt time.Time
	epoch   uint64 // bumped by invalidate, so a read in flight is not cached
	refill  singleflight.Group
}

// New wires the service; bus may be nil (single node, tests).
func New(db *store.DB, bus core.Bus, process core.EnvironmentValues) *Service {
	s := &Service{db: db, bus: bus, process: process}
	s.load = func(ctx context.Context) (core.EnvironmentValues, error) { return loadGlobal(ctx, s.db.Gorm) }
	if bus != nil {
		s.unsubscribe = bus.Subscribe(core.ChannelConfigChanged, func(payload []byte) {
			var msg struct{ Key string }
			if json.Unmarshal(payload, &msg) == nil && msg.Key == configKey {
				s.invalidate()
			}
		})
	}
	return s
}

// Close drops the config:changed subscription.
func (s *Service) Close(context.Context) {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
}

func (s *Service) RegisterRoutes(r *httpapi.Router) {
	// The editors of all three layers show the declarations and the process
	// layer they fall back to; the account editor is open to the holders of
	// account readers and creators of any scope too, who may edit the
	// account layer.
	r.PermScoped("GET", "/environment/variables", s.variables,
		perm.SettingsRead, perm.GroupRead, perm.AccountRead, perm.AccountCreate)
	r.Perm("GET", "/settings/environment", "settings:read", s.get)
	r.Perm("PUT", "/settings/environment", "settings:manage", s.put)
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.lastAt, s.retryAt = time.Time{}, time.Time{}
	s.epoch++
	s.mu.Unlock()
}

// GlobalEnvironment returns the cached global layer over the process layer.
//
//   - Fresh (read less than globalTTL ago): the cached layer.
//   - Stale: the cached layer at once, while one background read refreshes
//     it (stale-while-revalidate), so gateway requests never wait for the
//     database once a layer was read.
//   - Nothing read yet, or outdated by invalidate: callers wait for one read.
//
// A failed read keeps the last good layer (the process layer alone when
// there is none) and holds back the next read for errorTTL. One read serves
// all callers, detached from theirs: a client that went away must not
// fail it for everyone.
func (s *Service) GlobalEnvironment(ctx context.Context) core.EnvironmentValues {
	now := time.Now()
	s.mu.Lock()
	last, at, retryAt, epoch := s.last, s.lastAt, s.retryAt, s.epoch
	s.mu.Unlock()
	switch {
	case last != nil && !at.IsZero() && now.Sub(at) < globalTTL:
		return last
	case now.Before(retryAt):
		return s.fallback(last)
	case last != nil && !at.IsZero():
		s.refill.DoChan(strconv.FormatUint(epoch, 10), func() (any, error) {
			return s.read(context.WithoutCancel(ctx), epoch), nil
		})
		return last
	}
	// Keyed by epoch: a caller after invalidate does not join an older read.
	v, _, _ := s.refill.Do(strconv.FormatUint(epoch, 10), func() (any, error) {
		return s.read(context.WithoutCancel(ctx), epoch), nil
	})
	return v.(core.EnvironmentValues)
}

// read loads the global layer and caches the merge unless invalidate ran
// meanwhile. It returns what callers should use: the new layer, or after a
// failure the last good one.
func (s *Service) read(ctx context.Context, epoch uint64) core.EnvironmentValues {
	lctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	global, err := s.load(lctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		slog.WarnContext(ctx, "environment: read global layer; keeping the last one read", "err", err, "have_last", s.last != nil)
		if s.epoch == epoch {
			s.retryAt = time.Now().Add(errorTTL)
		}
		return s.fallback(s.last)
	}
	values := s.overProcess(global)
	if s.epoch == epoch {
		s.last, s.lastAt, s.retryAt = values, time.Now(), time.Time{}
	}
	return values
}

// overProcess is the global layer over the process layer.
func (s *Service) overProcess(global core.EnvironmentValues) core.EnvironmentValues {
	values := core.EnvironmentValues{}
	maps.Copy(values, s.process)
	maps.Copy(values, global)
	return values
}

// SettingsReaders reads the environment section as plugins see it
// (CONTRACTS §107): the global layer read now over the process layer,
// resolved like the layers of a request.
func (s *Service) SettingsReaders() map[string]core.SettingsReader {
	return map[string]core.SettingsReader{SettingsKey: func(ctx context.Context) (protocol.SettingsValues, error) {
		global, err := s.load(ctx)
		if err != nil {
			return nil, err
		}
		return protocol.EnvironmentSettings{Values: core.ResolveEnvironment(s.overProcess(global), nil, nil)}, nil
	}}
}

// fallback is last, or the process layer alone before any read succeeded.
func (s *Service) fallback(last core.EnvironmentValues) core.EnvironmentValues {
	if last != nil {
		return last
	}
	return maps.Clone(s.process)
}

// ResolveEnvironment reads the group and account layers and merges them with
// the global one. accountID 0 resolves the group alone.
func (s *Service) ResolveEnvironment(ctx context.Context, groupID, accountID int64) (core.Environment, error) {
	var row layerRows
	if err := layersQuery(store.G(ctx, s.db.Gorm), groupID, accountID, &row).Error; err != nil {
		return nil, err
	}
	return core.ResolveEnvironment(s.GlobalEnvironment(ctx), row.GroupEnv.V, row.AccountEnv.V), nil
}

// layerRows are the group and account layers.
type layerRows struct {
	GroupEnv, AccountEnv store.JSON[core.EnvironmentValues]
}

// layersQuery reads into dst the layers of a group and a live account, {}
// for a row that is not there.
func layersQuery(db *gorm.DB, groupID, accountID int64, dst *layerRows) *gorm.DB {
	group := db.Model(&model.Group{}).Select("environment").Where("id = ?", groupID)
	account := db.Model(&model.Account{}).Select("environment").Where("id = ? AND deleted_at IS NULL", accountID)
	return store.OneRow(db).Select("COALESCE((?), '{}') AS group_env, COALESCE((?), '{}') AS account_env", group, account).Scan(dst)
}

func loadGlobal(ctx context.Context, q *gorm.DB) (core.EnvironmentValues, error) {
	values := core.EnvironmentValues{}
	raw, err := store.SettingValue(store.G(ctx, q), SettingsKey)
	if store.IsNoRows(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

// variablesView is GET /environment/variables: the declared variables and
// the process layer, so editors can show what "follow" falls back to.
type variablesView struct {
	Variables []core.EnvironmentVariable `json:"variables"`
	Process   core.EnvironmentValues     `json:"process"`
}

func (s *Service) variables(c *gin.Context) {
	httpapi.OK(c, variablesView{Variables: core.EnvironmentVariables, Process: s.process})
}

func (s *Service) get(c *gin.Context) {
	values, err := loadGlobal(c.Request.Context(), s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"values": values})
}

// put replaces the global layer: {"values": {key: value}}; 0 clears a key.
func (s *Service) put(c *gin.Context) {
	ctx := c.Request.Context()
	var in struct {
		Values core.EnvironmentValues `json:"values"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	values, fe := core.ValidateEnvironmentValues(ctx, core.EnvLayerGlobal, "values", in.Values)
	if len(fe) > 0 {
		httpapi.Fail(c, core.InvalidFields(fe...))
		return
	}
	uid, _ := core.UserID(ctx)
	var by *int64
	if uid > 0 {
		by = &uid
	}
	if err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if _, err := store.UpdateSettingJSONTx(ctx, tx, SettingsKey, by, func(json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(values)
		}); err != nil {
			return err
		}
		return core.EmitSettingsChanged(ctx, s.Events, tx, SettingsKey)
	}); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.invalidate()
	if s.bus != nil {
		payload, _ := json.Marshal(map[string]string{"key": configKey})
		if err := s.bus.Publish(context.WithoutCancel(ctx), core.ChannelConfigChanged, payload); err != nil {
			slog.WarnContext(ctx, "environment: publish config:changed", "err", err)
		}
	}
	httpapi.OK(c, gin.H{"values": values})
}
