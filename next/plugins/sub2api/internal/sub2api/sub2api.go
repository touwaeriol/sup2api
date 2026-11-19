// Package sub2api is the platform of Sub2API sites (the legacy release and
// sub2api-next): one apikey type serving the OpenAI, Anthropic and Gemini
// protocols as they are, each with its official auth header, and the key's
// balance.
package sub2api

import (
	"strconv"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// TypeAPIKey is the account type id (manifest.json accountTypes[0].id).
const TypeAPIKey = "apikey"

// BaseURLRequired is the message of an account without a base URL.
const BaseURLRequired = "Base URL of the Sub2API site is required / 请填写 Sub2API 站点的地址"

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(APIKey()) }

// APIKey is the three presets on the site's own address (required, saved
// as written, a request appending each protocol's whole path to it less
// the one /v1beta or /v1 it may end in; any non-empty key; no other
// credentials), with the balance of the key from GET
// /v1/usage.
func APIKey() standard.Type {
	spec := apikey.Spec{
		RequireBaseURL: true,
		Versions:       []string{"/v1beta", "/v1"},
		Texts: apikey.Texts{
			UnsupportedAccountType: "unsupported account type {type} / 不支持的账号类型",
			BaseURLRequired:        BaseURLRequired,
			BaseURLFormat:          "base_url {reason} / base_url 必须是不含账号、查询参数的 http(s) 地址",
		},
	}
	t := standard.Merge(TypeAPIKey, spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""))
	t.Balance = &standard.Balance{Requests: []standard.BalanceRequest{{Path: "/v1/usage"}}, Read: readBalance}
	return t
}

// readBalance reads both releases' answer as it is, never converted
// (CONTRACTS §52): the amount is remaining, else balance, else
// quota.remaining (the first present), -1 meaning no limit (a legacy
// subscription without any limit); the unit is unit as the site gives it,
// none when it gives none. A key the site reports invalid (disabled or
// expired) is rejected.
func readBalance(_ *standard.Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult {
	if f := standard.BalanceFailure(answers[0]); f != nil {
		return f
	}
	b := gjson.ParseBytes(answers[0].GetBody())
	if v := b.Get("isValid"); v.Exists() && !v.Bool() {
		return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED,
			ErrorMessage: "the site reports the key disabled or expired / 上游报告此 Key 已停用或过期"}
	}
	var amount gjson.Result
	for _, path := range []string{"remaining", "balance", "quota.remaining"} {
		if amount = b.Get(path); amount.Exists() {
			break
		}
	}
	if !amount.Exists() {
		return transient("no remaining, balance or quota.remaining in /v1/usage / 应答里没有余额")
	}
	if amount.Float() == -1 {
		return &pluginv1.BalanceResult{Unlimited: true}
	}
	m, ok := standard.Micros(amount.String())
	if !ok {
		return transient("amount " + strconv.Quote(amount.Raw) + " is not a number in range / 余额不是可读的数字")
	}
	return &pluginv1.BalanceResult{AmountMicros: m, Currency: b.Get("unit").String()}
}

func transient(msg string) *pluginv1.BalanceResult {
	return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT, ErrorMessage: msg}
}
