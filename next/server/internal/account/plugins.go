package account

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// core.PluginAccountReader: the accounts a plugin may read about its own
// account types (HostService.ListAccounts / GetAccount).
//
// The security boundary of both methods is one SQL predicate: plugin_key =
// $pluginKey. accounts.plugin_key is the plugin that declared the account
// type, written when the account was created and never rewritten, and it is
// also the AES-GCM associated data of the stored credentials (aad()), so a row
// belonging to another plugin could not even be decrypted here. Keeping the
// predicate in the statement rather than in a post-filter means there is no
// code path in which a row of another plugin is loaded and then discarded.
var (
	_ core.PluginAccountReader    = (*Service)(nil)
	_ core.PluginAccountRequester = (*Service)(nil)
)

// PluginAccountsMaxLimit caps one ListPluginAccounts page. A plugin with many
// accounts must page; there is no "give me everything" call.
const PluginAccountsMaxLimit = 200

// PluginAccountsDefaultLimit is used for Limit <= 0.
const PluginAccountsDefaultLimit = 100

// AuditPluginAccountRead is the audit_logs action of one GetAccount call.
const AuditPluginAccountRead = "plugin.account.read"

// AuditPluginAccountRequest is the audit_logs action of one
// SendAccountRequest call.
const AuditPluginAccountRequest = "plugin.account_request"

// ListPluginAccounts returns one page of the accounts of pluginKey's own
// account types, ordered by id. It selects no credential column at all, so no
// later mistake in this file can put one in the result.
func (s *Service) ListPluginAccounts(ctx context.Context, pluginKey string, q core.PluginAccountQuery) ([]core.PluginAccountSummary, error) {
	if pluginKey == "" {
		return nil, core.ErrInvalidArgument.WithMessage("plugin key is required")
	}
	limit := q.Limit
	if limit <= 0 || limit > PluginAccountsMaxLimit {
		limit = PluginAccountsDefaultLimit
	}
	var rows []pluginAccount
	if err := pluginAccountsPage(store.G(ctx, s.d.DB.Gorm), pluginKey, q, limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]core.PluginAccountSummary, len(rows))
	for i, r := range rows {
		out[i] = core.PluginAccountSummary{ID: r.ID, Name: r.Name, Type: r.Type, Status: r.Status, Schedulable: r.Schedulable,
			Settings: settingsOrEmpty(r.Settings)}
	}
	return out, nil
}

// pluginAccountsPage selects the page of ListPluginAccounts.
func pluginAccountsPage(db *gorm.DB, pluginKey string, q core.PluginAccountQuery, limit int) *gorm.DB {
	db = db.Model(&model.Account{}).Select("id, name, type, status, schedulable, settings").
		Where("plugin_key = ? AND deleted_at IS NULL AND id > ?", pluginKey, q.AfterID)
	if q.Type != "" {
		db = db.Where("type = ?", q.Type)
	}
	if !q.IncludeInactive {
		db = db.Where("status = 'active' AND schedulable")
	}
	return db.Order("id").Limit(limit)
}

// pluginAccount is a row of pluginAccountsPage: no credential column.
type pluginAccount struct {
	ID          int64
	Name        string
	Type        string
	Status      string
	Schedulable bool
	Settings    []byte
}

// ReadPluginAccount returns one account of pluginKey's own account types as
// the plugin receives it on PlatformService calls (CONTRACTS §124):
// properties, settings, decrypted credentials and the proxy it goes out
// through, after writing the audit row. The row is read like the gateway's
// (scanRow), its proxy through proxyEgress: an account whose proxy cannot be
// had is not handed out without it (Unavailable).
//
// Order matters: the audit row is committed before the plaintext leaves this
// function. An audit failure is a call failure - the record of "this plugin
// read this account at this time" is what makes a read outside traffic
// reviewable, so handing out the secret without it would quietly remove it.
func (s *Service) ReadPluginAccount(ctx context.Context, pluginKey string, id int64) (*pluginv1.Account, error) {
	if pluginKey == "" {
		return nil, core.ErrInvalidArgument.WithMessage("plugin key is required")
	}
	a, err := scanRow(selectRows(store.G(ctx, s.d.DB.Gorm)).Where("a.id = ? AND a.plugin_key = ? AND a.deleted_at IS NULL", id, pluginKey).Row())
	if store.IsNoRows(err) {
		// Deliberately the same answer for "no such account" and "an account
		// of another plugin's account type": a distinguishable error would
		// turn this call into an account-id oracle.
		return nil, notFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.decrypt(pluginKey, a.CredEnc)
	if err != nil {
		return nil, err
	}
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		return nil, core.ErrUnavailable.WithMessage(core.AsError(err).Message)
	}
	if err := audit.Audit(ctx, s.d.DB.Gorm, 0, AuditPluginAccountRead, "account", itoa(id), map[string]any{
		// No console user is in this chain: the plugin process asks the host
		// directly, so the actor is the plugin, recorded here.
		"plugin_key":   pluginKey,
		"account_type": a.Type,
		"account_name": a.Name,
		"via":          "host.GetAccount",
	}); err != nil {
		return nil, fmt.Errorf("audit plugin account read: %w", err)
	}
	return a.pluginAccount(plain, eg.Proxy), nil
}

// SendPluginAccountRequest sends one request a plugin described for one
// account of pluginKey's own account types through that account's proxy, as
// the quota and refresh requests are sent (sendPluginCall), except that a
// redirect is answered as it is. It is bounded by refreshTimeout. The URL's
// host must be one m allows for the account (the plugin's externalServices
// or the account's own upstream addresses, read from its settings and
// credentials) and the administrator's plugin network access lets the
// plugin reach (core.CheckPluginUpstream); another is refused before
// anything is sent or audited.
//
// Unlike a credential read, the audit row is written after the request: it
// records the answer's status, and nothing secret is handed out here that the
// plugin did not already hold, so an audit failure is logged and the answer
// still returned. The row names the method, host and path only - never the
// query, the headers or a body, which carry the plugin's credentials.
func (s *Service) SendPluginAccountRequest(ctx context.Context, pluginKey string, m *manifest.Manifest, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	if pluginKey == "" {
		return nil, core.ErrInvalidArgument.WithMessage("plugin key is required")
	}
	id := in.GetAccountId()
	var (
		proxyID       *int64
		typ           string
		settings, enc []byte
	)
	err := pluginAccountByID(store.G(ctx, s.d.DB.Gorm), id, pluginKey).Select("proxy_id, type, settings, credentials_enc").Row().
		Scan(&proxyID, &typ, &settings, &enc)
	if store.IsNoRows(err) {
		// The same answer as for a missing id (ReadPluginAccount).
		return nil, notFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.decrypt(pluginKey, enc)
	if err != nil {
		return nil, err
	}
	if ce := core.CheckPluginUpstream(ctx, s.d.NetworkAccess, pluginKey, m, in.GetUrl(), typ, string(settings), string(plain)); ce != nil {
		return nil, ce
	}
	method := cmp.Or(strings.ToUpper(in.GetMethod()), http.MethodGet)
	sendCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	var ans pluginAnswer
	if eg, err := s.proxyEgress(sendCtx, proxyID); err != nil {
		ans.transportError = core.AsError(err).Message
	} else {
		ans = s.sendPluginCall(sendCtx, pluginCall{plugin: pluginKey, what: "account request", method: method, url: in.GetUrl(),
			headers: in.GetHeaders(), body: in.GetBody(), noRedirects: true}, eg)
	}
	detail := map[string]any{"plugin_key": pluginKey, "method": method, "status": ans.status, "via": "host.SendAccountRequest"}
	if u, err := url.Parse(in.GetUrl()); err == nil {
		// Escaped: a decoded %00 would make the jsonb insert fail and leave
		// the request sent without its row.
		detail["host"], detail["path"] = u.Host, u.EscapedPath()
	}
	if err := audit.Audit(context.WithoutCancel(ctx), s.d.DB.Gorm, 0, AuditPluginAccountRequest, "account", itoa(id), detail); err != nil {
		slog.WarnContext(ctx, "account: audit plugin account request", "plugin", pluginKey, "account", id, "err", err)
	}
	return &pluginv1.ExecutionHTTPResponse{Status: ans.status, Headers: ans.headers, Body: ans.body,
		TransportError: ans.transportError}, nil
}

// pluginAccountByID is live account id if it is of pluginKey's own types.
func pluginAccountByID(db *gorm.DB, id int64, pluginKey string) *gorm.DB {
	return db.Model(&model.Account{}).Where("id = ? AND plugin_key = ? AND deleted_at IS NULL", id, pluginKey)
}

func settingsOrEmpty(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
