package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// ProxyChoice is the proxy an account not saved yet would use, as the
// account form sends it (models/fetch, proxy-ticket): an existing proxy, a
// pasted proxy URL - only parsed and used, never looked up or stored
// (CONTRACTS §21.2), though models/fetch hands it to the plugin as
// Account.proxy with id 0 (§124.2) - or neither, a direct connection.
type ProxyChoice struct {
	ProxyID  *int64  `json:"proxy_id,omitempty"`
	ProxyURL *string `json:"proxy_url,omitempty"`
}

// egress is a ProxyChoice the caller may use: the proxy id, or the pasted
// URL as given and parsed, or neither.
type egress struct {
	proxyID *int64
	rawURL  string
	spec    *core.ProxySpec
}

func (e egress) proxied() bool { return e.proxyID != nil || e.spec != nil }

// choice is e as the account form would send it again.
func (e egress) choice() ProxyChoice {
	if e.spec != nil {
		return ProxyChoice{ProxyURL: &e.rawURL}
	}
	return ProxyChoice{ProxyID: e.proxyID}
}

// checkChoice is c as the caller of ctx may use it for an account written
// under action: a proxy_url (blank is none) needs a proxy write key and
// must parse (never echoed), a proxy_id must be one the caller may
// reference (§21.2), and both together conflict.
func (s *Service) checkChoice(ctx context.Context, c ProxyChoice, action perm.Key) (egress, error) {
	raw := ""
	if c.ProxyURL != nil {
		raw = strings.TrimSpace(*c.ProxyURL)
	}
	if raw != "" && c.ProxyID != nil {
		return egress{}, core.InvalidFields(core.FieldError{Field: "proxy_url", Code: "conflict",
			Message: t(ctx, "proxy_url and proxy_id cannot both be given", "proxy_url 与 proxy_id 不能同时给出")})
	}
	if raw != "" {
		// Reaching a proxy by address takes a proxy write key, as when saving.
		byURL, err := s.parseProxyURL(ctx, raw)
		if err != nil {
			return egress{}, err
		}
		return egress{rawURL: raw, spec: &byURL.spec}, nil
	}
	if err := s.checkProxyRef(ctx, c.ProxyID, action); err != nil {
		return egress{}, err
	}
	return egress{proxyID: c.ProxyID}, nil
}

// egressClient is the way out through e: the proxy's client (cached), a
// transient one for a pasted URL (Proxy is that URL's address, id 0), or the
// direct one. release ends what a transient client holds.
func (s *Service) egressClient(ctx context.Context, e egress) (eg core.Egress, release func(), err error) {
	if e.spec == nil {
		eg, err = s.proxyEgress(ctx, e.proxyID)
		return eg, func() {}, err
	}
	eg.Proxied = true
	if eg.Client, err = s.d.Proxies.HTTPClientFor(ctx, *e.spec); err != nil {
		return eg, nil, err
	}
	eg.Proxy = &core.ProxyRef{ProxySpec: *e.spec}
	return eg, eg.Client.CloseIdleConnections, nil
}

// ---------------------------------------------------------------- proxy tickets (CONTRACTS §117)

// proxyTicket is what a proxy ticket seals: the user it was issued to and
// the proxy choice.
type proxyTicket struct {
	UserID int64 `json:"u"`
	ProxyChoice
}

// ticketAAD binds a ticket to its plugin: another plugin's cannot be opened.
func ticketAAD(pluginKey string) []byte { return []byte("proxy-ticket:" + pluginKey) }

func (s *Service) sealTicket(pluginKey string, tk proxyTicket) (string, error) {
	plain, err := json.Marshal(tk)
	if err != nil {
		return "", err
	}
	sealed, err := s.d.Cipher.Encrypt(plain, ticketAAD(pluginKey))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Service) openTicket(pluginKey, raw string) (proxyTicket, error) {
	var tk proxyTicket
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return tk, err
	}
	plain, err := s.d.Cipher.Decrypt(sealed, ticketAAD(pluginKey))
	if err != nil {
		return tk, err
	}
	return tk, json.Unmarshal(plain, &tk)
}

// ticketAction is the account action a proxy ticket is checked under:
// creating when the caller may create some accounts, else re-authorizing
// (updating); neither is permission_denied.
func (s *Service) ticketAction(ctx context.Context) (perm.Key, error) {
	for _, action := range []perm.Key{perm.AccountCreate, perm.AccountUpdate} {
		scope, err := s.scopeOf(ctx, action)
		if err != nil {
			return "", err
		}
		if !scope.Empty() {
			return action, nil
		}
	}
	return "", core.ErrPermissionDenied.WithDetails(map[string]any{"permission": string(perm.AccountCreate)})
}

// issueProxyTicket is POST /account-types/:plugin_key/:type/proxy-ticket: the
// account form's proxy choice, checked as models/fetch checks it, sealed for
// the type's plugin and the caller. A native authorization flow hands the
// ticket to its plugin, whose fetches then go through that proxy.
func (s *Service) issueProxyTicket(c *gin.Context) {
	ctx := c.Request.Context()
	var in ProxyChoice
	if !httpapi.BindJSON(c, &in) {
		return
	}
	bt, ok := s.accountType(c.Param("plugin_key"), c.Param("type"))
	if !ok {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "type", Code: "unknown",
			Message: t(ctx, "unknown account type (is its plugin enabled?)", "未知的账号类型（插件是否已启用？）")}))
		return
	}
	action, err := s.ticketAction(ctx)
	var e egress
	if err == nil {
		e, err = s.checkChoice(ctx, in, action)
	}
	uid, _ := core.UserID(ctx)
	var ticket string
	if err == nil {
		ticket, err = s.sealTicket(bt.Plugin.Key, proxyTicket{UserID: uid, ProxyChoice: e.choice()})
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"ticket": ticket})
}

// FetchWithProxyTicket implements core.ProxyTicketFetcher: the ticket must be
// pluginKey's and issued to the user of ctx (the route call), who must still
// be allowed to use its proxy; the fetch then goes through that proxy, or
// directly behind the SSRF guard, without redirects and without a timeout
// of its own (the route call bounds it).
func (s *Service) FetchWithProxyTicket(ctx context.Context, pluginKey string, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	tk, err := s.openTicket(pluginKey, in.GetProxyTicket())
	if uid, _ := core.UserID(ctx); err != nil || uid <= 0 || tk.UserID != uid {
		return nil, core.ErrPermissionDenied.WithMessage("the proxy ticket was not issued to the caller of this route for this plugin")
	}
	action, err := s.ticketAction(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.checkChoice(ctx, tk.ProxyChoice, action)
	if err != nil {
		return nil, err
	}
	// A proxy that cannot be used (disabled since the ticket was issued) is
	// a refusal like the checks above: nothing was sent.
	eg, release, err := s.egressClient(ctx, e)
	if err != nil {
		return nil, err
	}
	defer release()
	return execfetch.Do(ctx, in, execfetch.Binding{
		CheckURL: func(ctx context.Context, raw string) (*url.URL, error) {
			return netguard.CheckURLVia(ctx, raw, s.d.AllowPrivateUpstream, eg.Proxied, netguard.DefaultLookup)
		},
		Client: func(context.Context) (*http.Client, error) { return eg.Client, nil },
	})
}

var _ core.ProxyTicketFetcher = (*Service)(nil)
