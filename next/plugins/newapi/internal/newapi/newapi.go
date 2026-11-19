// Package newapi is the platform of New API sites: one apikey type serving
// the OpenAI, Anthropic, Gemini and TypeSafe protocols as they are, each
// with its official auth header, and the key's balance.
package newapi

import (
	"math"
	"strconv"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// TypeAPIKey is the account type id (manifest.json accountTypes[0].id).
const TypeAPIKey = "apikey"

// BaseURLRequired is the message of an account without a base URL.
const BaseURLRequired = "Base URL of the New API site is required / 请填写 New API 站点的地址"

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(APIKey()) }

// APIKey is the four presets on the site's own address (required, saved
// as written, a request appending each protocol's whole path to it less
// the one /v1beta or /v1 it may end in; any non-empty key; no other
// credentials), with the balance of the key in the site's
// own quota.
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
	t := standard.Merge(TypeAPIKey, spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""), typesafe.APIKey(""))
	t.Balance = &standard.Balance{Requests: []standard.BalanceRequest{{Path: "/api/usage/token/"}}, Read: readBalance}
	return t
}

// readBalance reads the key's available quota as the site answers it,
// without a unit (the answer has none) and never converted (CONTRACTS
// §52): the lower of the key's and its user's when the site reports the
// user's, the user's for a key without a limit, no limit when neither has
// one.
func readBalance(_ *standard.Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult {
	if f := standard.BalanceFailure(answers[0]); f != nil {
		return f
	}
	token := gjson.ParseBytes(answers[0].GetBody())
	if !token.Get("code").Bool() {
		return transient("token usage refused: " + token.Get("message").String() + " / 站点拒绝查询令牌额度")
	}
	var quotas []gjson.Result
	if !token.Get("data.unlimited_quota").Bool() {
		quotas = append(quotas, token.Get("data.total_available"))
	}
	if user := token.Get("data.user_quota.total_available"); user.Exists() {
		quotas = append(quotas, user)
	}
	if len(quotas) == 0 {
		return &pluginv1.BalanceResult{Unlimited: true}
	}
	lowest := int64(math.MaxInt64)
	for _, q := range quotas {
		m, ok := standard.Micros(q.String())
		if !ok {
			return transient("total_available " + strconv.Quote(q.Raw) + " is not a number in range / 额度不是可读的数字")
		}
		lowest = min(lowest, m)
	}
	return &pluginv1.BalanceResult{AmountMicros: lowest}
}

func transient(msg string) *pluginv1.BalanceResult {
	return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT, ErrorMessage: msg}
}
