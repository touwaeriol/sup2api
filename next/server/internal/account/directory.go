package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Candidates returns active, schedulable accounts of the group whose account
// type is in types (all types when empty), highest priority first, excluding
// accounts that are cooling down.
func (s *Service) Candidates(ctx context.Context, groupID int64, types []core.AccountTypeKey) ([]core.AccountRef, error) {
	snap, err := s.groupSnapshot(ctx, groupID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]core.AccountRef, 0, len(snap.refs))
	for i := range snap.refs {
		// A timed automatic disable expires by itself (lazy expiry): the
		// account serves again once its release time passes, whether or not
		// the recovery sweep has written it back to active yet.
		if r := &snap.refs[i]; !snap.disabledAt(r.ID, now) && ofTypes(r, types) {
			out = append(out, *r)
		}
	}
	cooling, err := s.coolingDown(ctx, out)
	if err != nil {
		// Serving a cooling account is better than serving nothing; the
		// gateway will classify the upstream error again.
		slog.WarnContext(ctx, "account: read cooldowns", "err", err)
		return out, nil
	}
	if cooling == nil {
		return out, nil
	}
	kept := out[:0]
	for i, r := range out {
		if cooling[i].Val() == 0 {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// Resting returns the accounts of the group's snapshot, of types (all types
// when empty), that Candidates leaves out now: those under a timed automatic
// disable that has not run out, and those cooling down. Unlike Candidates it
// fails when the cooldowns cannot be read.
func (s *Service) Resting(ctx context.Context, groupID int64, types []core.AccountTypeKey) ([]core.AccountRef, error) {
	snap, err := s.groupSnapshot(ctx, groupID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out, live []core.AccountRef
	for i := range snap.refs {
		switch r := &snap.refs[i]; {
		case !ofTypes(r, types):
		case snap.disabledAt(r.ID, now):
			out = append(out, *r)
		default:
			live = append(live, *r)
		}
	}
	cooling, err := s.coolingDown(ctx, live)
	if err != nil {
		return nil, err
	}
	for i := range cooling {
		if cooling[i].Val() != 0 {
			out = append(out, live[i])
		}
	}
	return out, nil
}

// disabledAt reports an account of the snapshot under a timed automatic
// disable that has not run out at now.
func (g groupSnap) disabledAt(id int64, now time.Time) bool {
	until, ok := g.releases[id]
	return ok && now.Before(until)
}

// ofTypes reports whether the account type of r is in types; every type is
// in an empty types.
func ofTypes(r *core.AccountRef, types []core.AccountTypeKey) bool {
	return len(types) == 0 || slices.Contains(types, core.AccountTypeKey{PluginKey: r.PluginKey, Type: r.Type})
}

// coolingDown reads in one pipeline whether each of refs has a cooldown
// (Val() != 0); nil when there is nothing to read.
func (s *Service) coolingDown(ctx context.Context, refs []core.AccountRef) ([]*redis.IntCmd, error) {
	if len(refs) == 0 || s.d.Redis == nil {
		return nil, nil
	}
	pipe := s.d.Redis.Pipeline()
	cmds := make([]*redis.IntCmd, len(refs))
	for i := range refs {
		cmds[i] = pipe.Exists(ctx, cooldownKey(refs[i].ID))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return cmds, nil
}

// GroupHasType reports whether the group holds an account of one of types,
// disabled or not; only failed dispatches ask, so it is not cached.
func (s *Service) GroupHasType(ctx context.Context, groupID int64, types []core.AccountTypeKey) (bool, error) {
	if len(types) == 0 {
		return false, nil
	}
	pairs := make([]any, len(types))
	for i, t := range types {
		pairs[i] = []any{t.PluginKey, t.Type}
	}
	g := store.G(ctx, s.d.DB.Gorm)
	return gormpg.Exists(g, g.Table("accounts a").Joins("JOIN account_groups ag ON ag.account_id = a.id").
		Where("ag.group_id = ? AND a.deleted_at IS NULL", groupID).Where(clause.IN{Column: []clause.Column{{Table: "a", Name: "plugin_key"}, {Table: "a", Name: "type"}}, Values: pairs}))
}

// Unschedulable lists the accounts of the group Candidates does not offer now,
// with why. Not cached: only a dispatch that found no account asks, to record
// what the group holds (CONTRACTS §24.6).
func (s *Service) Unschedulable(ctx context.Context, groupID int64) ([]core.UnschedulableAccount, error) {
	var all []groupMember
	if err := store.G(ctx, s.d.DB.Gorm).Table("accounts a").Select("a.id, a.status, a.schedulable, a.auto_disabled_until").
		Joins("JOIN account_groups ag ON ag.account_id = a.id").Where("ag.group_id = ? AND a.deleted_at IS NULL", groupID).
		Order("a.id").Limit(200).Scan(&all).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	var out []core.UnschedulableAccount
	var offered []int64
	for _, a := range all {
		switch {
		case a.Status == "auto_disabled" && a.Schedulable && a.AutoDisabledUntil != nil && !now.Before(*a.AutoDisabledUntil):
			offered = append(offered, a.ID) // lazily expired: served again
		case a.Status == "auto_disabled":
			u := core.UnschedulableAccount{ID: a.ID, Reason: "auto_disabled"}
			if a.AutoDisabledUntil != nil {
				u.Until = *a.AutoDisabledUntil
			}
			out = append(out, u)
		case a.Status != "active":
			out = append(out, core.UnschedulableAccount{ID: a.ID, Reason: "disabled"})
		case !a.Schedulable:
			out = append(out, core.UnschedulableAccount{ID: a.ID, Reason: "unschedulable"})
		default:
			offered = append(offered, a.ID)
		}
	}
	if len(offered) == 0 || s.d.Redis == nil {
		return out, nil
	}
	pipe := s.d.Redis.Pipeline()
	ttls := make([]*redis.DurationCmd, len(offered))
	for i, id := range offered {
		ttls[i] = pipe.PTTL(ctx, cooldownKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return out, nil // what the database says still stands
	}
	for i, id := range offered {
		if ttl := ttls[i].Val(); ttl > 0 {
			out = append(out, core.UnschedulableAccount{ID: id, Reason: "cooldown", Until: now.Add(ttl)})
		}
	}
	return out, nil
}

// groupMember is an account of a group, as Unschedulable reads it.
type groupMember struct {
	ID                int64
	Status            string
	Schedulable       bool
	AutoDisabledUntil *time.Time
}

// groupSnapshot returns the group's schedulable accounts: active ones and
// those under a timed automatic disable, whose release times it lists.
func (s *Service) groupSnapshot(ctx context.Context, groupID int64) (groupSnap, error) {
	now := time.Now()
	s.cacheMu.Lock()
	snap, ok := s.groups[groupID]
	epoch := s.epoch
	s.cacheMu.Unlock()
	if ok && now.Sub(snap.at) < snapshotTTL {
		return snap, nil
	}
	v, err, _ := s.sf.Do("g:"+itoa(groupID), func() (any, error) {
		// A miss of the snapshot cache runs this read on a gateway request.
		snap, err := readGroupSnap(store.G(ctx, s.d.DB.Gorm), groupID, now)
		if err != nil {
			return nil, err
		}
		s.cacheMu.Lock()
		if s.epoch == epoch {
			s.groups[groupID] = snap
		}
		s.cacheMu.Unlock()
		return snap, nil
	})
	if err != nil {
		return groupSnap{}, err
	}
	return v.(groupSnap), nil
}

// readGroupSnap reads the group's snapshot taken at now: its schedulable
// accounts in scheduling order, the active ones and those under a timed
// automatic disable with its release time. A miss of the snapshot cache
// runs it on a gateway request: its rows are scanned by position.
func readGroupSnap(db *gorm.DB, groupID int64, now time.Time) (groupSnap, error) {
	snap := groupSnap{at: now, refs: []core.AccountRef{}}
	// The columns that are no field of an AccountRef. Every row replaces
	// them, so one set serves the whole read instead of one per row.
	var col struct {
		mapping, limits []byte
		env             store.JSON[core.EnvironmentValues]
		until           *time.Time
		// The protocol binding: NULL, NULL for an account without one, which
		// is then neither transferred nor parsed.
		protocols      []string
		modelProtocols []byte
	}
	err := store.EachRow(db.Table("accounts a").Select(`a.id, a.name, a.plugin_key, a.type, a.priority, a.weight,
			a.max_concurrency, a.proxy_id, a.models, a.model_mapping, a.rpm_limit, a.tpm_limit, a.spm_limit, a.spm_window_seconds, a.model_limits, a.environment,
			COALESCE(a.conversion_loss_policy, ''), CASE WHEN a.status = 'auto_disabled' THEN a.auto_disabled_until END,
			a.protocols, NULLIF(a.model_protocols, '[]'::jsonb)`).
		Joins("JOIN account_groups ag ON ag.account_id = a.id").
		Where(`ag.group_id = ? AND a.deleted_at IS NULL AND a.schedulable
			AND (a.status = 'active' OR a.status = 'auto_disabled' AND a.auto_disabled_until IS NOT NULL)`, groupID).
		Order("a.priority DESC, a.id"), func(rows *sql.Rows) error {
		var a core.AccountRef
		if err := rows.Scan(&a.ID, &a.Name, &a.PluginKey, &a.Type, &a.Priority, &a.Weight, &a.MaxConcurrency, &a.ProxyID,
			(*store.Strings)(&a.Models), &col.mapping, &a.RPMLimit, &a.TPMLimit, &a.SPMLimit, &a.SPMWindowSeconds, &col.limits, &col.env, &a.ConversionLossPolicy, &col.until,
			(*store.Strings)(&col.protocols), &col.modelProtocols); err != nil {
			return err
		}
		if len(col.mapping) > 0 {
			_ = json.Unmarshal(col.mapping, &a.ModelMapping)
		}
		if col.until != nil {
			if snap.releases == nil {
				snap.releases = map[int64]time.Time{}
			}
			snap.releases[a.ID] = *col.until
		}
		a.ModelLimits, a.Environment = parseModelLimits(col.limits), col.env.V
		a.ProtocolRules = core.NewProtocolRules(col.protocols, parseModelProtocols(col.modelProtocols))
		snap.refs = append(snap.refs, a)
		return nil
	})
	return snap, err
}

// Load returns one account with decrypted credentials (not deleted; any status).
func (s *Service) Load(ctx context.Context, id int64) (*core.Account, error) {
	now := time.Now()
	s.cacheMu.Lock()
	snap, ok := s.accounts[id]
	epoch := s.epoch
	s.cacheMu.Unlock()
	if ok && now.Sub(snap.at) < snapshotTTL {
		acc := snap.acc
		return &acc, nil
	}
	a, err := s.loadLive(ctx, id)
	if err != nil {
		return nil, err
	}
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, err
	}
	acc := a.coreAccount(plain)
	s.cacheMu.Lock()
	if s.epoch == epoch {
		s.accounts[id] = accountSnap{at: now, acc: acc}
	}
	s.cacheMu.Unlock()
	return &acc, nil
}

// Egress implements core.AccountDirectory: the way out of account id as it
// stands now (Load's snapshot, dropped on account:changed). Paths of this
// package holding the account's live row take proxyEgress of its proxy_id
// instead.
func (s *Service) Egress(ctx context.Context, id int64) (core.Egress, error) {
	a, err := s.Load(ctx, id)
	if err != nil {
		return core.Egress{}, err
	}
	return s.proxyEgress(ctx, a.ProxyID)
}

// proxyEgress is the way out through proxy proxyID: its client (cached by
// the proxy service) and the proxy itself, the direct client for nil.
// Proxied holds even when the client cannot be had: the request then fails,
// never going out directly, and Proxy is nil with the error.
func (s *Service) proxyEgress(ctx context.Context, proxyID *int64) (core.Egress, error) {
	pc, err := s.d.Proxies.HTTPClient(ctx, proxyID)
	return core.Egress{Client: pc.Client, Proxied: proxyID != nil, Proxy: pc.Proxy}, err
}

// loadLive is loadRow of any live account, unlocked, for Load: a miss of
// its cache runs it on a gateway request, so it is scanned through Row.
func (s *Service) loadLive(ctx context.Context, id int64) (*row, error) {
	a, err := scanRow(selectRows(store.G(ctx, s.d.DB.Gorm)).Where("a.id = ? AND a.deleted_at IS NULL", id).Row())
	if store.IsNoRows(err) {
		return nil, notFound(ctx)
	}
	return a, err
}

// IsCoolingDown reports whether the account has an active cooldown.
func (s *Service) IsCoolingDown(ctx context.Context, id int64) (bool, error) {
	if s.d.Redis == nil {
		return false, nil
	}
	n, err := s.d.Redis.Exists(ctx, cooldownKey(id)).Result()
	return n > 0, err
}

// SetCooldown excludes the account from scheduling until the given time. The
// first cooldown of a period emits account.status_changed (status "cooldown").
var extendCooldown = redis.NewScript(`
local clock = redis.call('TIME')
local ttl = tonumber(ARGV[1]) - (tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000))
if ttl <= 0 then return 0 end
local previous = redis.call('PTTL', KEYS[1])
if previous == -1 or previous >= ttl then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ttl)
if previous >= 0 then return 0 end
return 1`)

func (s *Service) SetCooldown(ctx context.Context, id int64, until time.Time, reason string) error {
	if s.d.Redis == nil {
		return nil
	}
	first, err := extendCooldown.Run(ctx, s.d.Redis, []string{cooldownKey(id)}, until.UnixMilli(), reason).Int()
	if err != nil {
		return err
	}
	if first != 1 {
		return nil // extending an existing cooldown
	}
	if s.d.DB == nil || s.d.Events == nil {
		return nil
	}
	var a model.Account
	if err := store.G(ctx, s.d.DB.Gorm).Select("plugin_key, type, name").Where("id = ?", id).Take(&a).Error; err != nil {
		if store.IsNoRows(err) {
			return nil
		}
		return err
	}
	return s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		return s.d.Events.Emit(ctx, tx, core.Event{Type: core.EventAccountStatusChanged,
			Payload: statusPayload(id, a.PluginKey, a.Type, a.Name, "cooldown", reason, &until)})
	})
}

// AutoDisable automatically blocks an account judged unusable (CONTRACTS §42):
// status=auto_disabled, with a reason and the shared status events.
// It never changes an administrator-disabled account or the scheduling switch.
// An opted-out account is unchanged; an existing disable still reports true.
func (s *Service) AutoDisable(ctx context.Context, id int64, reason string) (bool, error) {
	out, err := s.ApplyAccountAutomaticState(ctx, id, &pluginv1.AccountAutomaticState{
		Action: pluginv1.AccountAutomaticState_ACTION_DISABLE, Reason: reason,
	})
	if err != nil {
		return false, err
	}
	return out.GetStatus() == "auto_disabled" || out.GetStatus() == "disabled", nil
}

// AutoDisableUntil disables the account until release (CONTRACTS §69).
func (s *Service) AutoDisableUntil(ctx context.Context, id int64, reason string, release time.Time) (bool, error) {
	out, err := s.ApplyAccountAutomaticState(ctx, id, &pluginv1.AccountAutomaticState{
		Action: pluginv1.AccountAutomaticState_ACTION_DISABLE, Reason: reason,
		Release: &pluginv1.AccountAutomaticState_UntilUnix{UntilUnix: release.Unix()},
	})
	if err != nil {
		return false, err
	}
	return out.GetStatus() == "auto_disabled" || out.GetStatus() == "disabled", nil
}

// AutoDisableByPolicy executes the core's frozen decision, retaining account opt-out.
func (s *Service) AutoDisableByPolicy(ctx context.Context, id int64, reason string) (bool, error) {
	out, err := s.applyAutomaticState(ctx, "", id, &pluginv1.AccountAutomaticState{
		Action: pluginv1.AccountAutomaticState_ACTION_DISABLE, Reason: reason,
	}, true)
	if err != nil {
		return false, err
	}
	return out.GetStatus() == "auto_disabled" || out.GetStatus() == "disabled", nil
}

// TouchLastUsed records usage; last_used_at is written in batches by Run.
func (s *Service) TouchLastUsed(_ context.Context, id int64) {
	s.touchMu.Lock()
	s.touched[id] = time.Now().UTC()
	s.touchMu.Unlock()
}

// FlushLastUsed writes pending last_used_at updates in one statement.
func (s *Service) FlushLastUsed(ctx context.Context) error {
	s.touchMu.Lock()
	if len(s.touched) == 0 {
		s.touchMu.Unlock()
		return nil
	}
	pending := s.touched
	s.touched = map[int64]time.Time{}
	s.touchMu.Unlock()
	ids := make([]int64, 0, len(pending))
	ts := make([]time.Time, 0, len(pending))
	for id, at := range pending {
		ids = append(ids, id)
		ts = append(ts, at)
	}
	// last_used_at only moves forward, and alone: updated_at is the version
	// other writers check.
	err := store.G(ctx, s.d.DB.Gorm).Table("accounts AS a").
		Clauses(store.UpdateFrom("(SELECT unnest(?::bigint[]) AS id, unnest(?::timestamptz[]) AS t) v", store.Int64s(ids), store.Times(ts))).
		Where("a.id = v.id AND (a.last_used_at IS NULL OR a.last_used_at < v.t)").Update("last_used_at", gorm.Expr("v.t")).Error
	if err != nil {
		s.touchMu.Lock()
		for id, at := range pending {
			if cur, ok := s.touched[id]; !ok || cur.Before(at) {
				s.touched[id] = at
			}
		}
		s.touchMu.Unlock()
	}
	return err
}
