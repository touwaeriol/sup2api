package payment

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// gateway returns the gateway of id: a built-in one, or a plugin's in the
// current generation. A plugin gateway missing from it (plugin disabled or
// uninstalled) is unavailable. The built-in gateways answer whether or not
// they are enabled: being switched off only stops new orders, and the
// orders already made are still verified and credited.
func (s *Service) gateway(id string) (core.PaymentGateway, bool) {
	if g, ok := s.builtin[id]; ok {
		return g, true
	}
	for _, b := range s.pluginGateways() {
		if b.Gateway.ID == id {
			return b.Client, true
		}
	}
	return nil, false
}

// pluginGateways are the gateways of the current plugin generation.
func (s *Service) pluginGateways() []core.PaymentGatewayBinding {
	if s.plugins == nil {
		return nil
	}
	g, ok := s.plugins.Current().(core.PaymentGatewayGeneration)
	if !ok {
		return nil
	}
	return g.PaymentGateways()
}

// gatewayOpen reports whether gateway id takes new orders: a built-in one
// switched on in v, a plugin one while its plugin is enabled.
func (s *Service) gatewayOpen(v Settings, id string) bool {
	switch id {
	case core.PaymentGatewayEpay:
		return v.Epay.Enabled
	case core.PaymentGatewayStripe:
		return v.Stripe.Enabled
	}
	_, ok := s.gateway(id)
	return ok
}

// pluginGatewayView is a plugin gateway on the settings' gateways tab.
type pluginGatewayView struct {
	ID         string           `json:"id"`
	PluginKey  string           `json:"plugin_key"`
	PluginName string           `json:"plugin_name"`
	Name       string           `json:"name"`
	Ready      bool             `json:"ready"`
	Methods    []methodTemplate `json:"methods"`
	InUse      int              `json:"in_use"`
}

type methodTemplate struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// pluginGatewaysHandler lists the plugin gateways of the current generation
// and, as not ready, those the methods still name.
func (s *Service) pluginGatewaysHandler(c *gin.Context) {
	ctx := c.Request.Context()
	ms, err := loadMethods(ctx, s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	inUse := map[string]int{}
	for _, m := range ms {
		inUse[m.Gateway]++
	}
	out := []pluginGatewayView{}
	for _, b := range s.pluginGateways() {
		out = append(out, bindingView(ctx, b, inUse[b.Gateway.ID]))
		delete(inUse, b.Gateway.ID)
	}
	for id, n := range inUse {
		if !validBuiltin(id) {
			key, _, _ := strings.Cut(id, ".")
			out = append(out, pluginGatewayView{ID: id, PluginKey: key, PluginName: key, Name: id, Methods: []methodTemplate{}, InUse: n})
		}
	}
	httpapi.OK(c, out)
}

func bindingView(ctx context.Context, b core.PaymentGatewayBinding, inUse int) pluginGatewayView {
	loc := core.Locale(ctx)
	v := pluginGatewayView{ID: b.Gateway.ID, PluginKey: b.Plugin.Key, PluginName: b.Plugin.Key, Name: b.Gateway.Name.Get(loc),
		Ready: true, Methods: make([]methodTemplate, len(b.Gateway.Methods)), InUse: inUse}
	if b.Plugin.Manifest != nil {
		v.PluginName = core.LocalizedText(b.Plugin.Manifest.Name).Get(loc)
	}
	for i, m := range b.Gateway.Methods {
		v.Methods[i] = methodTemplate{Type: m.Type, Name: m.Name.Get(loc)}
	}
	return v
}

func validBuiltin(id string) bool {
	return id == core.PaymentGatewayEpay || id == core.PaymentGatewayStripe
}
