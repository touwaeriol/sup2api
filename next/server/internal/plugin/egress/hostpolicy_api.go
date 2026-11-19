package egress

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// RegisterRoutes mounts the plugin network access setting (CONTRACTS §113),
// with the permissions of the other system settings.
func (p *Provider) RegisterRoutes(r *httpapi.Router) {
	r.Perm("GET", "/settings/plugin-egress", perm.SettingsRead, p.getHostPolicy)
	r.Perm("PUT", "/settings/plugin-egress", perm.SettingsManage, p.putHostPolicy)
}

func (p *Provider) getHostPolicy(c *gin.Context) {
	if p.db == nil {
		httpapi.Fail(c, core.ErrUnavailable)
		return
	}
	h, err := loadHostPolicy(c.Request.Context(), p.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, h)
}

// hostPolicyInput is the PUT body; an omitted field keeps its value and a
// list is replaced as a whole.
type hostPolicyInput struct {
	Mode           *string   `json:"mode"`
	BlockedDomains *[]string `json:"blocked_domains"`
	AllowedDomains *[]string `json:"allowed_domains"`
}

// normalize checks the fields given and puts the lists in their stored
// form, without duplicates; messages follow the request locale.
func (in *hostPolicyInput) normalize(ctx context.Context) []core.FieldError {
	zh := core.Locale(ctx) == "zh"
	var fe []core.FieldError
	if in.Mode != nil && *in.Mode != HostAllowAll && *in.Mode != HostBlockAll {
		msg := "must be allow_all or block_all"
		if zh {
			msg = "只能是 allow_all 或 block_all"
		}
		fe = append(fe, core.FieldError{Field: "mode", Code: "invalid", Message: msg})
	}
	list := func(field string, v *[]string) {
		if v == nil {
			return
		}
		out := []string{}
		seen := map[string]bool{}
		for i, entry := range *v {
			d, ok := NormalizeDomain(entry)
			if !ok {
				msg := strconv.Quote(entry) + " is not a domain name or IP address (no scheme, port, path or wildcard)"
				if zh {
					msg = strconv.Quote(entry) + " 不是域名或 IP 地址（不带协议、端口、路径和通配符）"
				}
				fe = append(fe, core.FieldError{Field: field + "." + strconv.Itoa(i), Code: "invalid", Message: msg})
				continue
			}
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
		*v = out
	}
	list("blocked_domains", in.BlockedDomains)
	list("allowed_domains", in.AllowedDomains)
	return fe
}

func (in *hostPolicyInput) applyTo(h *HostPolicy) {
	if in.Mode != nil {
		h.Mode = *in.Mode
	}
	if in.BlockedDomains != nil {
		h.BlockedDomains = *in.BlockedDomains
	}
	if in.AllowedDomains != nil {
		h.AllowedDomains = *in.AllowedDomains
	}
}

// putHostPolicy saves the setting and its audit row in one transaction, then
// drops the cached copy here and, through config:changed, on every node.
func (p *Provider) putHostPolicy(c *gin.Context) {
	ctx := c.Request.Context()
	if p.db == nil {
		httpapi.Fail(c, core.ErrUnavailable)
		return
	}
	var in hostPolicyInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if fe := in.normalize(ctx); len(fe) > 0 {
		httpapi.Fail(c, core.InvalidFields(fe...))
		return
	}
	uid, _ := core.UserID(ctx)
	var by *int64
	if uid > 0 {
		by = &uid
	}
	h := DefaultHostPolicy()
	err := p.db.Tx(ctx, func(tx *gorm.DB) error {
		if _, err := store.UpdateSettingJSONTx(ctx, tx, HostSettingsKey, by, func(raw json.RawMessage) (json.RawMessage, error) {
			if err := json.Unmarshal(raw, &h); err != nil {
				return nil, err
			}
			in.applyTo(&h)
			h = h.normalized()
			return json.Marshal(h)
		}); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, uid, "settings.plugin_egress.update", "settings", HostSettingsKey, h)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	p.hosts.invalidate()
	if p.opts.Bus != nil {
		payload, _ := json.Marshal(map[string]string{"key": hostConfigKey})
		if err := p.opts.Bus.Publish(context.WithoutCancel(ctx), core.ChannelConfigChanged, payload); err != nil {
			slog.WarnContext(ctx, "egress: publish config:changed", "err", err)
		}
	}
	httpapi.OK(c, h)
}
