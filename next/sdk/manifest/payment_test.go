package manifest

import "testing"

func TestParsePaymentGatewayName(t *testing.T) {
	for _, c := range [][2]string{{"alipay", "direct"}, {PluginID("erio", "pay"), "usdt_trc20"}} {
		plugin, gw, ok := ParsePaymentGatewayName(PaymentGatewayName(c[0], c[1]))
		if !ok || plugin != c[0] || gw != c[1] {
			t.Fatalf("%v: %q %q %v", c, plugin, gw, ok)
		}
	}
	for _, name := range []string{"epay", "stripe", "", ".direct", "alipay.", "alipay.Direct", "alipay.direct.x",
		"_erio.direct", "_erio__.direct", "a23456789012345678901234567890x.direct", "alipay.d",
		"Foo.bar", "1pay.direct", "al-pay.direct", "_erio__Pay.direct", "_erio__p.direct"} {
		if plugin, gw, ok := ParsePaymentGatewayName(name); ok {
			t.Fatalf("%q parsed as %q %q", name, plugin, gw)
		}
	}
}
