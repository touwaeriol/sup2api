package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

var (
	typeKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)
	colorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	logoDataURL    = regexp.MustCompile(`^data:image/(png|jpeg|webp|svg\+xml);base64,`)
)

// maxPrice bounds the rate so an order's payment (at most core.MaxCreditPerTx
// times it) fits payment_orders.pay_amount, numeric(20,8). The decimals
// match the snapshot columns: price numeric(20,8), discount_rate
// numeric(10,4).
var maxPrice = decimal.NewFromInt(1000000)

const (
	priceDecimals = 8
	rateDecimals  = 4
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

// validateValues checks the settings and methods on their own (the
// merchant lock, against the stored settings, is checkSettings').
func (s *Service) validateValues(ctx context.Context, v settingsValues) *fieldErrs {
	f := &fieldErrs{ctx: ctx}
	s.validateGeneral(f, v.Settings)
	validateLists(f, v.Settings)
	validateMethods(f, v.Methods)
	for _, b := range builtinSpecs {
		b.check(f, v.Settings)
	}
	return f
}

func (s *Service) validateGeneral(f *fieldErrs, v Settings) {
	f.check(!v.Enabled || s.siteSecure(), "enabled", "top-up needs an https site address (SUB2API_PUBLIC_URL)", "站点地址（SUB2API_PUBLIC_URL）必须是 https 才能开启充值")
	settles := slices.ContainsFunc(protocol.PaymentCurrencies, func(c protocol.PaymentCurrency) bool { return c.Code == v.Currency })
	f.check(settles, "currency", "unsupported currency", "不支持的货币")
	f.check(v.Price.IsPositive() && v.Price.LessThanOrEqual(maxPrice) && x.Exact(v.Price, priceDecimals), "price",
		"must be above 0 and at most "+maxPrice.String()+", with at most 8 decimals", "必须大于 0、不超过 "+maxPrice.String()+"，最多 8 位小数")
	f.check(!v.MinTopup.IsNegative(), "min_topup", "must not be negative", "不能小于 0")
	f.check(v.MaxTopup.GreaterThan(v.MinTopup), "max_topup", "must be above the minimum", "必须大于最低充值")
	f.check(v.MaxTopup.LessThanOrEqual(core.MaxCreditPerTx), "max_topup",
		"must not exceed the ledger's single credit limit "+core.MaxCreditPerTx.String(), "不能超过账本单次入账上限 "+core.MaxCreditPerTx.String())
	f.check(v.OrderTimeoutMinutes >= 1 && v.OrderTimeoutMinutes <= 1440, "order_timeout_minutes", "must be 1 to 1440", "必须在 1 到 1440 之间")
	f.check(v.MaxPendingOrders >= 1 && v.MaxPendingOrders <= 100, "max_pending_orders", "must be 1 to 100", "必须在 1 到 100 之间")
}

func validateLists(f *fieldErrs, v Settings) {
	f.check(len(v.AmountOptions) <= maxListItems, "amount_options", "at most 20 amounts", "最多 20 个金额")
	for _, a := range v.AmountOptions {
		f.check(!a.LessThan(v.MinTopup) && !a.GreaterThan(v.MaxTopup), "amount_options",
			"amounts must be within the minimum and maximum", "金额必须在最低和最高充值之间")
	}
	f.check(len(v.Discounts) <= maxListItems, "discounts", "at most 20 tiers", "最多 20 档折扣")
	seen := map[string]bool{}
	for i, d := range v.Discounts {
		p := fmt.Sprintf("discounts.%d.", i)
		f.check(d.Amount.IsPositive() && !seen[d.Amount.String()], p+"amount", "a positive amount, once", "金额必须大于 0 且不重复")
		f.check(d.Rate.IsPositive() && d.Rate.LessThan(decimal.NewFromInt(1)) && x.Exact(d.Rate, rateDecimals), p+"rate",
			"must be between 0 and 1, with at most 4 decimals", "必须在 0 到 1 之间，最多 4 位小数")
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
	_, _, plugin := manifest.ParsePaymentGatewayName(id)
	return plugin || builtinOf(id) != nil
}

// validLogo: none, an https address, or a base64 image data URL of an
// allowed type within logoMaxBytes.
func validLogo(v string) bool {
	if v == "" || httpsURL(v) && len(v) <= logoMaxURL {
		return true
	}
	loc := logoDataURL.FindStringIndex(v)
	if loc == nil {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(v[loc[1]:])
	return err == nil && len(data) <= logoMaxBytes
}
