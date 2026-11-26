package registry

import (
	"log/slog"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// PaymentGateways implements core.PaymentGatewayGeneration: the gateways of
// the enabled plugins holding the payment.gateway grant. A gateway missing
// here (plugin disabled, uninstalled or not granted) is unavailable.
func (g *generation) PaymentGateways() []core.PaymentGatewayBinding { return g.payments }

// addPaymentGateways registers the gateways the plugin declares, named
// "<plugin id>.<gateway id>": the plugin id is unique and the manifest's
// gateway ids are (manifest/check), so the names are, and no built-in name
// holds a dot. A gateway the runtime has no client for (the process does
// not serve PaymentGatewayService) is skipped and marks the plugin
// abnormal, like a conflicting notification channel.
func (g *generation) addPaymentGateways(ext Extension, info core.PluginInfo, m *manifest.Manifest, grants Grants) []core.ResourceConflict {
	source, ok := ext.(interface {
		PaymentGateway(id string) core.PaymentGateway
	})
	if !ok || m.Payments == nil || !hasCapability(m, manifest.CapPaymentGateway) || !grants.Has(manifest.PermPaymentGateway) {
		return nil
	}
	var conflicts []core.ResourceConflict
	for _, gw := range m.Payments.Gateways {
		name := manifest.PaymentGatewayName(info.Key, gw.ID)
		client := source.PaymentGateway(gw.ID)
		if client == nil {
			reason := "the plugin process does not serve " + manifest.CapPaymentGateway
			slog.Error("plugin registry: payment gateway skipped, the plugin is abnormal", "plugin", info.Key, "gateway", name, "reason", reason)
			conflicts = append(conflicts, core.ResourceConflict{Resource: "payment_gateway", ID: name, Reason: reason})
			continue
		}
		g.payments = append(g.payments, core.PaymentGatewayBinding{Plugin: info, Gateway: paymentGatewayInfo(name, gw), Client: client})
	}
	return conflicts
}

func paymentGatewayInfo(name string, gw manifest.PaymentGateway) core.PaymentGatewayInfo {
	methods := make([]core.PaymentMethodTemplate, len(gw.Methods))
	for i, m := range gw.Methods {
		methods[i] = core.PaymentMethodTemplate{Type: m.Type, Name: core.LocalizedText(m.Name), Icon: m.Icon}
	}
	return core.PaymentGatewayInfo{ID: name, Name: core.LocalizedText(gw.Name), Icon: gw.Icon, Methods: methods, Query: gw.Query}
}
