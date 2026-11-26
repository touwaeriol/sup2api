package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// currencies are the currencies a top-up may settle in (the console's list).
var currencies = []string{"CNY", "USD", "HKD", "EUR", "GBP", "JPY"}

var (
	typeKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)
	colorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	logoDataURL    = regexp.MustCompile(`^data:image/(png|jpeg|webp|svg\+xml);base64,`)
	stripeKey      = regexp.MustCompile(`^(sk|rk)_(live|test)_`)
)

const (
	maxListItems = 20
	maxMethods   = 50
	logoMaxBytes = 64 << 10
	logoMaxURL   = 2048
)

// fieldErrs gathers validation problems under the API's field paths
// ("values.epay.key"), which the console maps to its tabs.
type fieldErrs struct {
	ctx  context.Context
	list []core.FieldError
}

func (f *fieldErrs) add(path, en, zh string) {
	f.list = append(f.list, core.FieldError{Field: "values." + path, Code: "invalid", Message: t(f.ctx, en, zh)})
}

func (f *fieldErrs) check(ok bool, path, en, zh string) {
	if !ok {
		f.add(path, en, zh)
	}
}

func (f *fieldErrs) err() error {
	if len(f.list) == 0 {
		return nil
	}
	return core.InvalidFields(f.list...)
}

// validateValues checks the settings and methods on their own (open
// orders are checked against the stored settings by lockedGateways).
func (s *Service) validateValues(ctx context.Context, v settingsValues) *fieldErrs {
	f := &fieldErrs{ctx: ctx}
	s.validateGeneral(f, v.Settings)
	validateLists(f, v.Settings)
	validateMethods(f, v.Methods)
	validateGateways(f, v.Settings)
	return f
}

func (s *Service) validateGeneral(f *fieldErrs, v Settings) {
	f.check(!v.Enabled || s.siteSecure(), "enabled", "top-up needs an https site address (SUB2API_PUBLIC_URL)", "站点地址（SUB2API_PUBLIC_URL）必须是 https 才能开启充值")
	f.check(slices.Contains(currencies, v.Currency), "currency", "unsupported currency", "不支持的货币")
	f.check(v.Price.IsPositive(), "price", "must be greater than 0", "必须大于 0")
	f.check(!v.MinTopup.IsNegative(), "min_topup", "must not be negative", "不能小于 0")
	f.check(v.MaxTopup.GreaterThan(v.MinTopup.Decimal), "max_topup", "must be above the minimum", "必须大于最低充值")
	f.check(v.MaxTopup.LessThanOrEqual(maxCreditPerTx), "max_topup",
		"must not exceed the ledger's single credit limit "+maxCreditPerTx.String(), "不能超过账本单次入账上限 "+maxCreditPerTx.String())
	f.check(v.OrderTimeoutMinutes >= 1 && v.OrderTimeoutMinutes <= 1440, "order_timeout_minutes", "must be 1 to 1440", "必须在 1 到 1440 之间")
	f.check(v.MaxPendingOrders >= 1 && v.MaxPendingOrders <= 100, "max_pending_orders", "must be 1 to 100", "必须在 1 到 100 之间")
}

func validateLists(f *fieldErrs, v Settings) {
	f.check(len(v.AmountOptions) <= maxListItems, "amount_options", "at most 20 amounts", "最多 20 个金额")
	for _, a := range v.AmountOptions {
		f.check(!a.LessThan(v.MinTopup.Decimal) && !a.GreaterThan(v.MaxTopup.Decimal), "amount_options",
			"amounts must be within the minimum and maximum", "金额必须在最低和最高充值之间")
	}
	f.check(len(v.Discounts) <= maxListItems, "discounts", "at most 20 tiers", "最多 20 档折扣")
	seen := map[string]bool{}
	for i, d := range v.Discounts {
		p := fmt.Sprintf("discounts.%d.", i)
		f.check(d.Amount.IsPositive() && !seen[d.Amount.String()], p+"amount", "a positive amount, once", "金额必须大于 0 且不重复")
		f.check(d.Rate.IsPositive() && d.Rate.LessThan(decimal.NewFromInt(1)), p+"rate", "must be between 0 and 1", "必须在 0 到 1 之间")
		seen[d.Amount.String()] = true
	}
}

func validateMethods(f *fieldErrs, ms []Method) {
	f.check(len(ms) <= maxMethods, "methods", "at most 50 methods", "最多 50 个支付方式")
	seen := map[string]bool{}
	for i, m := range ms {
		p := fmt.Sprintf("methods.%d.", i)
		f.check(typeKeyPattern.MatchString(m.Type) && !seen[m.Type], p+"type", "1-50 of a-z, 0-9, _, once", "1-50 位小写字母、数字或下划线，且不重复")
		seen[m.Type] = true
		f.check(strings.TrimSpace(m.Name) != "" && len(m.Name) <= 100, p+"name", "1-100 characters", "1-100 个字符")
		f.check(validGatewayID(m.Gateway), p+"gateway", "unknown gateway", "未知网关")
		f.check(len(m.Icon) <= 50, p+"icon", "at most 50 characters", "最多 50 个字符")
		f.check(m.Color == "" || colorPattern.MatchString(m.Color), p+"color", "a #rrggbb color", "颜色格式为 #rrggbb")
		f.check(validLogo(m.Logo), p+"logo", "an https address or an image of at most 64 KB", "https 地址或不超过 64 KB 的图片")
		f.check(!m.MinTopup.IsNegative(), p+"min_topup", "must not be negative", "不能小于 0")
	}
}

// validGatewayID: a built-in gateway or a plugin's "<plugin id>.<gateway id>".
// A plugin gateway need not be enabled now: its methods only show while it is.
func validGatewayID(id string) bool {
	if validBuiltin(id) {
		return true
	}
	plugin, gw, ok := strings.Cut(id, ".")
	return ok && plugin != "" && gw != "" && len(id) <= 100
}

// validLogo: none, an https address, or a base64 image data URL of an
// allowed type within logoMaxBytes.
func validLogo(v string) bool {
	if v == "" {
		return true
	}
	if strings.HasPrefix(v, "https://") {
		u, err := url.Parse(v)
		return err == nil && u.Host != "" && len(v) <= logoMaxURL
	}
	loc := logoDataURL.FindStringIndex(v)
	if loc == nil {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(v[loc[1]:])
	return err == nil && len(data) <= logoMaxBytes
}

func validateGateways(f *fieldErrs, v Settings) {
	if e := v.Epay; e.Enabled {
		f.check(strings.TrimSpace(e.MerchantID) != "", "epay.merchant_id", "required", "必填")
		f.check(e.Key != "", "epay.key", "required", "必填")
		// The gateway's own check: an https endpoint without credentials,
		// query or fragment, and a pid it can echo.
		if err := epayConfigOf(v).Validate(); err != nil && e.MerchantID != "" && e.Key != "" {
			f.add("epay.endpoint", err.Error(), err.Error())
		}
	}
	if st := v.Stripe; st.Enabled {
		f.check(stripeKey.MatchString(st.SecretKey), "stripe.secret_key", "a Stripe secret key (sk_live_, sk_test_, rk_live_ or rk_test_)", "Stripe 密钥（sk_live_、sk_test_、rk_live_ 或 rk_test_ 开头）")
		f.check(strings.HasPrefix(st.WebhookSecret, "whsec_"), "stripe.webhook_secret", "a Stripe webhook secret (whsec_)", "Stripe Webhook 密钥（whsec_ 开头）")
	}
}
