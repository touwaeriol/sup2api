package grpcruntime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
)

// Limits enforced by HostService. The rules of the kv calls (the limits of
// one entry and of one batch call, CONTRACTS §93) are in sdk/protocol/kv.go,
// shared with the SDK: protocol.KVMax*, protocol.KVBatchMax* and the
// protocol.CheckKV functions.
const (
	KVMaxListLimit   = 500
	LogMaxMessage    = 8 << 10
	LogMaxAttrs      = 32
	LedgerMaxIdemKey = 100
	// AccountsMaxListLimit caps one ListAccounts page; 0 or a larger value
	// uses AccountsDefaultListLimit.
	AccountsMaxListLimit     = 200
	AccountsDefaultListLimit = 100
	// ClearStickyBindings bounds its salt; its model has any length.
	ClearStickyBindingsMaxSalt = 128
	// GetUsersMaxIDs caps the ids of one GetUsers call.
	GetUsersMaxIDs = protocol.GetUsersMaxIDs
)

// hostServer serves HostService to exactly one plugin instance, so the
// calling plugin is known by construction.
type hostServer struct {
	pluginv1.UnimplementedHostServiceServer
	i *Instance
	p *proc // the broker belongs to this process, never to its replacement
}

func (h *hostServer) key() string { return h.i.pkg.Key }

func (h *hostServer) require(ctx context.Context, permission string) error {
	if !h.i.Grants().Has(permission) {
		return status.Errorf(codes.PermissionDenied, "plugin %s has no %q grant", h.i.name(), permission)
	}
	// Pub/sub accelerates local convergence but cannot authorize calls made
	// after an emergency revocation has committed in PG. Also reject a stale
	// broader scope until the local settings have refreshed.
	if db := h.i.rt.o.DB; db != nil {
		allowed, err := grants.Holds(ctx, db.Gorm, h.key(), h.i.pkg.Version, permission, h.i.Grants()[permission])
		if err != nil {
			return status.Error(codes.Unavailable, "cannot verify current plugin grant")
		}
		if !allowed {
			return status.Errorf(codes.PermissionDenied, "plugin %s grant %q was revoked or changed", h.i.name(), permission)
		}
	}
	return nil
}

// ------------------------------------------------------------------ log

func (h *hostServer) Log(ctx context.Context, in *pluginv1.LogRequest) (*pluginv1.LogResponse, error) {
	lvl := slog.LevelInfo
	switch in.GetLevel() {
	case pluginv1.LogRequest_LEVEL_DEBUG:
		lvl = slog.LevelDebug
	case pluginv1.LogRequest_LEVEL_WARN:
		lvl = slog.LevelWarn
	case pluginv1.LogRequest_LEVEL_ERROR:
		lvl = slog.LevelError
	}
	msg := in.GetMessage()
	if len(msg) > LogMaxMessage {
		msg = msg[:LogMaxMessage] + "...(truncated)"
	}
	attrs := make([]slog.Attr, 0, len(in.GetAttrs())+1)
	attrs = append(attrs, slog.String("source", "plugin"))
	n := 0
	for k, v := range in.GetAttrs() {
		if n >= LogMaxAttrs {
			break
		}
		n++
		attrs = append(attrs, slog.String("p."+k, v))
	}
	h.i.log.LogAttrs(ctx, lvl, msg, attrs...)
	return &pluginv1.LogResponse{}, nil
}

// ------------------------------------------------------------------ kv

// kvPrefix is what the host puts before every key of namespace ns of this
// plugin. The namespace is checked by the caller.
func (h *hostServer) kvPrefix(ns string) string {
	return "plugin:kv:" + h.key() + ":" + ns + ":"
}

// kvKey checks the namespace and key of a single call and returns the Redis
// key.
func (h *hostServer) kvKey(ns, k string) (string, error) {
	if err := cmp.Or(protocol.CheckKVNamespace(ns), protocol.CheckKVKey(k)); err != nil {
		return "", err
	}
	return h.kvPrefix(ns) + k, nil
}

// kvKeys returns the Redis keys of the checked keys of a batch call, in
// order.
func (h *hostServer) kvKeys(ns string, keys []string) []string {
	prefix := h.kvPrefix(ns)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = prefix + k
	}
	return out
}

func (h *hostServer) KVGet(ctx context.Context, in *pluginv1.KVGetRequest) (*pluginv1.KVGetResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	key, err := h.kvKey(in.GetNamespace(), in.GetKey())
	if err != nil {
		return nil, err
	}
	v, err := h.i.rt.o.Redis.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return &pluginv1.KVGetResponse{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	return &pluginv1.KVGetResponse{Found: true, Value: v}, nil
}

func (h *hostServer) KVSet(ctx context.Context, in *pluginv1.KVSetRequest) (*pluginv1.KVSetResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	key, err := h.kvKey(in.GetNamespace(), in.GetKey())
	if err != nil {
		return nil, err
	}
	if err := cmp.Or(protocol.CheckKVValue(len(in.GetValue())), protocol.CheckKVTTL(in.GetTtlSeconds())); err != nil {
		return nil, err
	}
	if err := h.i.rt.o.Redis.Set(ctx, key, in.GetValue(), time.Duration(in.GetTtlSeconds())*time.Second).Err(); err != nil {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	return &pluginv1.KVSetResponse{}, nil
}

func (h *hostServer) KVDelete(ctx context.Context, in *pluginv1.KVDeleteRequest) (*pluginv1.KVDeleteResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	key, err := h.kvKey(in.GetNamespace(), in.GetKey())
	if err != nil {
		return nil, err
	}
	if err := h.i.rt.o.Redis.Del(ctx, key).Err(); err != nil {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	return &pluginv1.KVDeleteResponse{}, nil
}

func (h *hostServer) KVList(ctx context.Context, in *pluginv1.KVListRequest) (*pluginv1.KVListResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	if err := protocol.CheckKVNamespace(in.GetNamespace()); err != nil {
		return nil, err
	}
	limit := int(in.GetLimit())
	if limit <= 0 || limit > KVMaxListLimit {
		limit = KVMaxListLimit
	}
	var cursor uint64
	if c := in.GetCursor(); c != "" {
		if _, err := fmt.Sscan(c, &cursor); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid cursor")
		}
	}
	base := h.kvPrefix(in.GetNamespace())
	match := globEscape(base) + globEscape(in.GetPrefix()) + "*"
	out := &pluginv1.KVListResponse{}
	for {
		keys, next, err := h.i.rt.o.Redis.Scan(ctx, cursor, match, int64(limit)).Result()
		if err != nil {
			return nil, status.Error(codes.Unavailable, "kv unavailable")
		}
		for _, k := range keys {
			out.Keys = append(out.Keys, strings.TrimPrefix(k, base))
		}
		cursor = next
		if cursor == 0 || len(out.Keys) >= limit {
			break
		}
	}
	if cursor != 0 {
		out.NextCursor = fmt.Sprint(cursor)
	}
	return out, nil
}

// ------------------------------------------------------------------ kv batch
//
// KVGetMany, KVSetMany and KVDeleteMany (CONTRACTS §93) do for many keys of
// one namespace what the single calls do for one: the grant is checked once
// and Redis is asked once, whatever the number of keys. The request is
// checked by protocol.CheckKVBatchKeys and protocol.CheckKVBatchItems, the
// functions the SDK calls before it sends one. The calls are valid on the
// single Redis instance the core connects to (cluster.OpenRedis); on a
// Redis Cluster the keys of one call would hash to different slots.

// kvSetManyLua writes the items of one KVSetMany call. KEYS[i] is the Redis
// key of item i, ARGV[2i-1] its value and ARGV[2i] its ttl in whole seconds
// (0: no expiry). The host wrote this script and it is the only script the
// kv calls run: a plugin supplies keys and values, never code.
//
// All or nothing: the arguments are checked before the first write (the host
// has validated every item already, including the upper bound of the ttl),
// SET cannot fail on an existing key of another type, and Redis runs a
// script as one step that no other command interleaves.
const kvSetManyLua = `
if #ARGV ~= 2 * #KEYS then
  return redis.error_reply('kv batch: ' .. #KEYS .. ' keys with ' .. #ARGV .. ' arguments')
end
for i = 1, #KEYS do
  local ttl = tonumber(ARGV[2 * i])
  if ttl == nil or ttl < 0 or ttl ~= math.floor(ttl) then
    return redis.error_reply('kv batch: bad ttl of item ' .. i)
  end
end
for i = 1, #KEYS do
  if tonumber(ARGV[2 * i]) == 0 then
    redis.call('SET', KEYS[i], ARGV[2 * i - 1])
  else
    redis.call('SET', KEYS[i], ARGV[2 * i - 1], 'EX', ARGV[2 * i])
  end
end
return #KEYS
`

// kvSetManyScript sends kvSetManyLua by its hash (EVALSHA) and falls back to
// EVAL when the server does not know it yet.
var kvSetManyScript = redis.NewScript(kvSetManyLua)

func (h *hostServer) KVGetMany(ctx context.Context, in *pluginv1.KVGetManyRequest) (*pluginv1.KVGetManyResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	if err := protocol.CheckKVBatchKeys(in.GetNamespace(), in.GetKeys()); err != nil {
		return nil, err
	}
	keys := h.kvKeys(in.GetNamespace(), in.GetKeys())
	out := &pluginv1.KVGetManyResponse{Items: make([]*pluginv1.KVGetManyItem, len(keys))}
	if len(keys) == 0 {
		return out, nil
	}
	values, err := h.i.rt.o.Redis.MGet(ctx, keys...).Result()
	if err != nil || len(values) != len(keys) {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	total := 0
	for i, v := range values {
		item := &pluginv1.KVGetManyItem{}
		if s, ok := v.(string); ok {
			// The answer is refused at the first value that takes it over
			// the limit, before the rest is copied.
			total += len(s)
			if err := protocol.CheckKVBatchAnswer(total); err != nil {
				return nil, err
			}
			item.Found, item.Value = true, []byte(s)
		}
		out.Items[i] = item
	}
	return out, nil
}

func (h *hostServer) KVSetMany(ctx context.Context, in *pluginv1.KVSetManyRequest) (*pluginv1.KVSetManyResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	// A missing (nil) item has no key and fails like one with an empty key.
	items := in.GetItems()
	if err := protocol.CheckKVBatchItems(in.GetNamespace(), len(items), func(i int) (string, int, int64) {
		return items[i].GetKey(), len(items[i].GetValue()), items[i].GetTtlSeconds()
	}); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return &pluginv1.KVSetManyResponse{}, nil
	}
	prefix := h.kvPrefix(in.GetNamespace())
	keys := make([]string, len(items))
	args := make([]any, 0, 2*len(items))
	for i, it := range items {
		keys[i] = prefix + it.GetKey()
		args = append(args, it.GetValue(), strconv.FormatInt(it.GetTtlSeconds(), 10))
	}
	if err := kvSetManyScript.Run(ctx, h.i.rt.o.Redis, keys, args...).Err(); err != nil {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	return &pluginv1.KVSetManyResponse{}, nil
}

func (h *hostServer) KVDeleteMany(ctx context.Context, in *pluginv1.KVDeleteManyRequest) (*pluginv1.KVDeleteManyResponse, error) {
	if err := h.require(ctx, "kv"); err != nil {
		return nil, err
	}
	if err := protocol.CheckKVBatchKeys(in.GetNamespace(), in.GetKeys()); err != nil {
		return nil, err
	}
	if len(in.GetKeys()) == 0 {
		return &pluginv1.KVDeleteManyResponse{}, nil
	}
	if err := h.i.rt.o.Redis.Del(ctx, h.kvKeys(in.GetNamespace(), in.GetKeys())...).Err(); err != nil {
		return nil, status.Error(codes.Unavailable, "kv unavailable")
	}
	return &pluginv1.KVDeleteManyResponse{}, nil
}

func globEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ------------------------------------------------------------------ db

func (h *hostServer) GetDSN(ctx context.Context, _ *pluginv1.GetDSNRequest) (*pluginv1.GetDSNResponse, error) {
	if err := h.require(ctx, "db.schema"); err != nil {
		return nil, err
	}
	if h.i.pkg.Manifest.Database == nil {
		return nil, status.Error(codes.FailedPrecondition, "manifest declares no database")
	}
	if h.i.rt.o.Schemas == nil {
		return nil, status.Error(codes.Unavailable, "database schemas unavailable")
	}
	dsn, schema, err := h.i.rt.o.Schemas.DSN(ctx, h.key())
	if err != nil {
		h.i.log.Error("GetDSN failed", "err", err)
		// A refusal of the core's (plugin_db_isolation_unavailable) says
		// what the operator has to change.
		if ce := (*core.Error)(nil); errors.As(err, &ce) {
			return nil, status.Error(codes.FailedPrecondition, ce.Code+": "+ce.Message)
		}
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	return &pluginv1.GetDSNResponse{Dsn: dsn, Schema: schema}, nil
}

// ------------------------------------------------------------------ authz

// AuthzCheck answers for the plugin's own permissions without a grant; asking
// about any other key reads the deployment's role assignments and needs the
// "authz.read" grant.
func (h *hostServer) AuthzCheck(ctx context.Context, in *pluginv1.AuthzCheckRequest) (*pluginv1.AuthzCheckResponse, error) {
	if in.GetUserId() <= 0 || in.GetPermission() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and permission are required")
	}
	if h.i.rt.o.Authorizer == nil {
		return nil, status.Error(codes.Unavailable, "authorizer unavailable")
	}
	key, own := h.qualify(in.GetPermission())
	if !own {
		if err := h.require(ctx, "authz.read"); err != nil {
			return nil, err
		}
	}
	ok, err := h.i.rt.o.Authorizer.Can(h.i.callers.narrow(ctx, in.GetUserId()), in.GetUserId(), key)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pluginv1.AuthzCheckResponse{Allowed: ok}, nil
}

// AccountScopeCheck answers which accounts the caller of a request may act
// on: core.AccountAccess, the rule the core proxy enforces on plugin routes,
// with its errors mapped like every other core error. It needs no grant: it
// hands out no field of any account, only which of the caller's own view
// the caller may act on, as AuthzCheck answers for the plugin's own keys.
// user_id must be the caller of an HTTP request this plugin is serving right
// now, so a plugin cannot survey other users.
func (h *hostServer) AccountScopeCheck(ctx context.Context, in *pluginv1.AccountScopeCheckRequest) (*pluginv1.AccountScopeCheckResponse, error) {
	ids := in.GetAccountIds()
	if in.GetUserId() <= 0 || in.GetAction() == "" || len(ids) > manifest.MaxAccountBatch ||
		slices.ContainsFunc(ids, func(id int64) bool { return id <= 0 }) {
		return nil, status.Errorf(codes.InvalidArgument, "user_id, action and at most %d positive account_ids are required", manifest.MaxAccountBatch)
	}
	if !h.i.callers.has(in.GetUserId()) {
		return nil, status.Error(codes.PermissionDenied, "user_id is not the caller of a request in flight")
	}
	access, ok := h.i.rt.o.Accounts.(core.AccountAccess)
	if !ok {
		return nil, status.Error(codes.Unavailable, "account access unavailable")
	}
	all, allowed, err := access.AccountsInScope(h.i.callers.narrow(ctx, in.GetUserId()), in.GetUserId(), in.GetAction(), ids)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pluginv1.AccountScopeCheckResponse{All: all, AllowedIds: allowed}, nil
}

// qualify resolves the permission a plugin asks about to its RBAC key. own
// reports that the key belongs to this plugin: its fully qualified key, or a
// local key whose action its manifest declares (with or without a scope,
// "rules:read:own"), which becomes "plugin.<key>:<local>". Every other key (a
// core permission such as "user:read", another plugin's key) is used as given
// and is not the plugin's own.
func (h *hostServer) qualify(p string) (key string, own bool) {
	if _, ok := manifest.LocalPermission(h.key(), p); ok {
		return p, true
	}
	// A declared key never contains a ".", so another plugin's qualified key
	// cannot match here and falls through as not own.
	key = manifest.PermissionKey(h.key(), p)
	pat, err := perm.Parse(key)
	if err != nil {
		return p, false
	}
	local, _ := manifest.LocalPermission(h.key(), string(pat.Action))
	if _, ok := h.i.pkg.Manifest.FindUserPermission(local); ok {
		return key, true
	}
	return p, false
}

// ------------------------------------------------------------------ ledger

func (h *hostServer) LedgerCredit(ctx context.Context, in *pluginv1.LedgerChangeRequest) (*pluginv1.LedgerChangeResponse, error) {
	return h.ledger(ctx, in, true)
}

func (h *hostServer) LedgerDebit(ctx context.Context, in *pluginv1.LedgerChangeRequest) (*pluginv1.LedgerChangeResponse, error) {
	return h.ledger(ctx, in, false)
}

func (h *hostServer) ledger(ctx context.Context, in *pluginv1.LedgerChangeRequest, credit bool) (*pluginv1.LedgerChangeResponse, error) {
	perm, kind, dir := "ledger.debit", "plugin_debit", "debit"
	if credit {
		perm, kind, dir = "ledger.credit", "plugin_credit", "credit"
	}
	if err := h.require(ctx, perm); err != nil {
		return nil, err
	}
	if h.i.rt.o.Ledger == nil {
		return nil, status.Error(codes.Unavailable, "ledger unavailable")
	}
	if in.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	idem := strings.TrimSpace(in.GetIdempotencyKey())
	if idem == "" || len(idem) > LedgerMaxIdemKey {
		return nil, status.Errorf(codes.InvalidArgument, "idempotency_key is required (<= %d bytes)", LedgerMaxIdemKey)
	}
	amount, err := decimal.NewFromString(in.GetAmount())
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Round(8)) {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive decimal with at most 8 decimals")
	}
	scope := h.i.Grants().Scope(perm)
	if max, ok := scopeDecimal(scope, "maxPerTx"); ok && amount.GreaterThan(max) {
		return nil, status.Errorf(codes.PermissionDenied, "amount exceeds the approved maxPerTx (%s)", max.String())
	}
	// Daily cap: reserve in Redis first, release on failure or duplicate.
	var release func()
	if max, ok := scopeDecimal(scope, "maxPerDay"); ok {
		units := amount.Shift(8).IntPart()
		day := time.Now().UTC().Format("20060102")
		key := "plugin:ledger:" + h.key() + ":" + dir + ":" + day
		rdb := h.i.rt.o.Redis
		total, err := rdb.IncrBy(ctx, key, units).Result()
		if err != nil {
			return nil, status.Error(codes.Unavailable, "ledger limiter unavailable")
		}
		rdb.Expire(ctx, key, 48*time.Hour)
		release = func() { rdb.DecrBy(context.WithoutCancel(ctx), key, units) }
		if total > max.Shift(8).IntPart() {
			release()
			return nil, status.Errorf(codes.PermissionDenied, "daily %s limit (%s) exceeded", dir, max.String())
		}
	}
	refType := in.GetRefType()
	if refType == "" {
		refType = "plugin"
	}
	res, err := h.i.rt.o.Ledger.Apply(ctx, core.LedgerChange{
		UserID:         in.GetUserId(),
		Amount:         amount,
		Credit:         credit,
		Kind:           kind,
		RefType:        refType,
		RefID:          in.GetRefId(),
		IdempotencyKey: "plugin:" + h.key() + ":" + idem,
		PluginKey:      h.key(),
		Note:           in.GetNote(),
	})
	if err != nil {
		if release != nil {
			release()
		}
		return nil, toStatus(err)
	}
	if res.Duplicate && release != nil {
		release()
	}
	return &pluginv1.LedgerChangeResponse{
		LedgerId:     res.LedgerID,
		BalanceAfter: res.BalanceAfter.StringFixed(8),
		Duplicate:    res.Duplicate,
	}, nil
}

// ------------------------------------------------------------------ accounts

// The account calls need no grant: a plugin receives the accounts of the
// account types it declares (CONTRACTS §124). The account module filters by
// plugin key in SQL, so a plugin declaring none gets nothing, and an account
// of another plugin is NOT_FOUND, not PERMISSION_DENIED: these calls cannot
// be used to probe which ids exist.

// ListAccounts returns the metadata of the accounts of this plugin's own
// account types.
func (h *hostServer) ListAccounts(ctx context.Context, in *pluginv1.ListAccountsRequest) (*pluginv1.ListAccountsResponse, error) {
	if h.i.rt.o.Accounts == nil {
		return nil, status.Error(codes.Unavailable, "accounts unavailable")
	}
	limit := int(in.GetLimit())
	if limit <= 0 || limit > AccountsMaxListLimit {
		limit = AccountsDefaultListLimit
	}
	var after int64
	if c := in.GetCursor(); c != "" {
		// strconv, not fmt.Sscan: Sscan stops at the first byte it cannot use,
		// so "1;drop" would parse as 1 without an error.
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n < 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid cursor")
		}
		after = n
	}
	rows, err := h.i.rt.o.Accounts.ListPluginAccounts(ctx, h.key(), core.PluginAccountQuery{
		Type: in.GetType(), AfterID: after, Limit: limit, IncludeInactive: in.GetIncludeInactive(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pluginv1.ListAccountsResponse{Accounts: make([]*pluginv1.AccountSummary, 0, len(rows))}
	for _, r := range rows {
		out.Accounts = append(out.Accounts, accountSummary(r))
	}
	// A full page may or may not be the last one; the next call settles it.
	if len(rows) == limit {
		out.NextCursor = fmt.Sprint(rows[len(rows)-1].ID)
	}
	return out, nil
}

// GetAccount returns one account of this plugin's own account types as
// PlatformService calls receive it, proxy included; the account module
// writes the audit row first.
func (h *hostServer) GetAccount(ctx context.Context, in *pluginv1.GetAccountRequest) (*pluginv1.GetAccountResponse, error) {
	if h.i.rt.o.Accounts == nil {
		return nil, status.Error(codes.Unavailable, "accounts unavailable")
	}
	if in.GetAccountId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	a, err := h.i.rt.o.Accounts.ReadPluginAccount(ctx, h.key(), in.GetAccountId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pluginv1.GetAccountResponse{Account: a}, nil
}

// SendAccountRequest has the account module send one request for one account
// of this plugin's own account types through that account's proxy (CONTRACTS
// §91), and reaches only the hosts
// a relay of the account may (core.CheckPluginUpstream, §112.17, §113): the
// manifest's externalServices, which the operator reviewed at install, or
// the account's own upstream addresses, which the account module reads with
// the account, and only those the administrator's plugin network access
// lets the plugin reach. Neither the body nor the answer is bounded.
func (h *hostServer) SendAccountRequest(ctx context.Context, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	if in.GetAccountId() <= 0 || in.GetUrl() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id and url are required")
	}
	if _, err := httpHost(in.GetUrl()); err != nil {
		return nil, err
	}
	requester, ok := h.i.rt.o.Accounts.(core.PluginAccountRequester)
	if !ok {
		return nil, status.Error(codes.Unavailable, "account requests unavailable")
	}
	out, err := requester.SendPluginAccountRequest(ctx, h.key(), h.i.pkg.Manifest, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return out, nil
}

// httpHost is the lower-case host name of the http(s) URL raw, without its
// port.
func httpHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", status.Error(codes.InvalidArgument, "invalid url")
	}
	host := strings.ToLower(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || host == "" {
		return "", status.Error(codes.PermissionDenied, "url must be http(s) with a host")
	}
	return host, nil
}

func (h *hostServer) SetAccountAutomaticState(ctx context.Context, in *pluginv1.SetAccountAutomaticStateRequest) (*pluginv1.SetAccountAutomaticStateResponse, error) {
	if err := h.require(ctx, "accounts.auto_state"); err != nil {
		return nil, err
	}
	scope := h.i.Grants().Scope("accounts.auto_state")
	if scope["types"] != "own" || len(scope) != 1 {
		return nil, status.Error(codes.PermissionDenied, `accounts.auto_state scope must be {"types":"own"}`)
	}
	if in.GetAccountId() <= 0 || in.GetState() == nil {
		return nil, status.Error(codes.InvalidArgument, "account_id and state are required")
	}
	controller, ok := h.i.rt.o.Accounts.(core.AccountAutomaticStateController)
	if !ok {
		return nil, status.Error(codes.Unavailable, "account automatic state unavailable")
	}
	out, err := controller.ApplyPluginAccountAutomaticState(ctx, h.key(), in.GetAccountId(), in.GetState())
	if err != nil {
		return nil, toStatus(err)
	}
	return out, nil
}

func (h *hostServer) ClearStickyBindings(ctx context.Context, in *pluginv1.ClearStickyBindingsRequest) (*pluginv1.ClearStickyBindingsResponse, error) {
	if err := h.require(ctx, "sticky.clear"); err != nil {
		return nil, err
	}
	if in.GetAccountId() <= 0 || in.GetGroupId() <= 0 || in.GetModel() == "" ||
		in.GetPercent() < 1 || in.GetPercent() > 100 || len(in.GetSalt()) > ClearStickyBindingsMaxSalt {
		return nil, status.Errorf(codes.InvalidArgument, "account_id, group_id, model and percent (1..100) are required; salt is at most %d bytes",
			ClearStickyBindingsMaxSalt)
	}
	if h.i.rt.o.Sticky == nil {
		return nil, status.Error(codes.Unavailable, "sticky bindings unavailable")
	}
	matched, cleared, err := h.i.rt.o.Sticky.ClearStickyBindings(ctx, core.StickyClearRequest{
		AccountID: in.GetAccountId(), GroupID: in.GetGroupId(), Model: in.GetModel(), Percent: int(in.GetPercent()), Salt: in.GetSalt()})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pluginv1.ClearStickyBindingsResponse{Matched: matched, Cleared: cleared}, nil
}

// GetEnvironment needs no grant: the values are operating parameters every
// plugin is expected to honour, not data (CONTRACTS §74.3).
func (h *hostServer) GetEnvironment(ctx context.Context, in *pluginv1.GetEnvironmentRequest) (*pluginv1.GetEnvironmentResponse, error) {
	if in.GetGroupId() < 0 || in.GetAccountId() < 0 {
		return nil, status.Error(codes.InvalidArgument, "group_id and account_id must not be negative")
	}
	if h.i.rt.o.Environment == nil {
		return nil, status.Error(codes.Unavailable, "environment unavailable")
	}
	env, err := h.i.rt.o.Environment.ResolveEnvironment(ctx, in.GetGroupId(), in.GetAccountId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pluginv1.GetEnvironmentResponse{Environment: env}, nil
}

func (h *hostServer) GetUsers(ctx context.Context, in *pluginv1.GetUsersRequest) (*pluginv1.GetUsersResponse, error) {
	if err := h.require(ctx, "users.read"); err != nil {
		return nil, err
	}
	if len(in.GetUserIds()) > GetUsersMaxIDs {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d user_ids", GetUsersMaxIDs)
	}
	if h.i.rt.o.Users == nil {
		return nil, status.Error(codes.Unavailable, "users unavailable")
	}
	users, err := h.i.rt.o.Users.LookupUsers(ctx, in.GetUserIds())
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pluginv1.UserInfo, len(users))
	for i, u := range users {
		out[i] = &pluginv1.UserInfo{Id: u.ID, DisplayName: u.DisplayName, Status: u.Status}
	}
	return &pluginv1.GetUsersResponse{Users: out}, nil
}

func accountSummary(a core.PluginAccountSummary) *pluginv1.AccountSummary {
	settings := string(a.Settings)
	if settings == "" {
		settings = "{}"
	}
	return &pluginv1.AccountSummary{
		Id: a.ID, Name: a.Name, Type: a.Type, Status: a.Status, Enabled: a.Schedulable, SettingsJson: settings,
	}
}

func scopeDecimal(scope map[string]any, field string) (decimal.Decimal, bool) {
	v, ok := scope[field]
	if !ok || v == nil {
		return decimal.Decimal{}, false
	}
	d, err := decimal.NewFromString(fmt.Sprint(v))
	if err != nil {
		// A malformed limit must not mean "unlimited".
		return decimal.Zero, true
	}
	return d, true
}

// toStatus maps core errors to gRPC status errors for plugins.
func toStatus(err error) error {
	e := core.AsError(err)
	code := codes.Internal
	switch e.Code {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "unauthenticated":
		code = codes.Unauthenticated
	case "permission_denied":
		code = codes.PermissionDenied
	case "not_found":
		code = codes.NotFound
	case "conflict":
		code = codes.AlreadyExists
	case "insufficient_balance":
		code = codes.FailedPrecondition
	case "rate_limited":
		code = codes.ResourceExhausted
	case "unavailable", "plugin_unavailable", "no_available_account":
		code = codes.Unavailable
	}
	msg := e.Message
	if code == codes.Internal {
		msg = "internal error"
	}
	return status.Error(code, e.Code+": "+msg)
}

var _ pluginv1.HostServiceServer = (*hostServer)(nil)

// PurgePluginKV deletes every KV entry of pluginKey (uninstall or key release
// with purge, install.PluginKVPurger).
func (r *Runtime) PurgePluginKV(ctx context.Context, pluginKey string) (int, error) {
	match := globEscape("plugin:kv:"+pluginKey+":") + "*"
	deleted := 0
	var cursor uint64
	for {
		keys, next, err := r.o.Redis.Scan(ctx, cursor, match, 500).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, err := r.o.Redis.Del(ctx, keys...).Result()
			deleted += int(n)
			if err != nil {
				return deleted, err
			}
		}
		if cursor = next; cursor == 0 {
			return deleted, nil
		}
	}
}
