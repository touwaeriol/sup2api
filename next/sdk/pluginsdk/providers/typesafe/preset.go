package typesafe

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// APIKey is a TypeSafe API key account type: Bearer key on
// https://api.typesafe.ai (a trailing /v1 not repeated; any non-empty key),
// systemone with the body as it is, the TypeSafe error policy, a
// one-question systemone test on jev-latest and the model listing (GET
// /v1/models, models.#.name). It filters no request field and has no
// balance: TypeSafe has no balance endpoint.
func APIKey(id string) standard.Type {
	return standard.Type{
		ID: id,
		Spec: apikey.Spec{
			DefaultBaseURL: DefaultBaseURL,
			Versions:       []string{"/v1"},
			Texts: apikey.Texts{
				UnsupportedAccountType: "unsupported account type {type} / 不支持的账号类型",
				BaseURLFormat:          "base_url {reason} / base_url 必须是不含账号、查询参数的 http(s) 地址",
			},
		},
		Auth:            standard.Auth{Header: "authorization", Prefix: "Bearer "},
		Headers:         []standard.Header{standard.Set("content-type", standard.Fixed("application/json"))},
		Routes:          map[string]standard.Route{ProtocolSystemOne: {Path: standard.Path("/v1/systemone")}},
		DefaultProtocol: ProtocolSystemOne,
		Classify:        Policy,
		Test:            &standard.Test{Protocol: ProtocolSystemOne, Model: "jev-latest", Body: PingBody},
		Models:          &apikey.ModelList{Path: "/v1/models", IdsPath: "models.#.name"},
	}
}
