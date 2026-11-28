package codex

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// identity is what the plugin reads from OpenAI's tokens.
type identity struct {
	Email     string
	AccountID string // chatgpt_account_id
	PlanType  string // chatgpt_plan_type
	FedRAMP   bool   // chatgpt_account_is_fedramp
}

// authClaim and profileClaim are the OpenAI-specific claim objects of
// OpenAI's tokens: the ChatGPT account and the user's profile.
const (
	authClaim    = "https://api.openai.com/auth"
	profileClaim = "https://api.openai.com/profile"
)

// jwtClaims decodes the payload of a JWT WITHOUT verifying its signature.
//
// It reads tokens from two places:
//
//   - the answers of OpenAI's token endpoint (authorization and refresh),
//     straight over TLS in the same exchange;
//   - the id_token an administrator submits with the account's credentials
//     (ValidateCredentials), from which missing email / plan_type /
//     chatgpt_account_id are filled in. That account id is
//     then sent upstream as the chatgpt-account-id header.
//
// Not verifying is deliberate and safe in both cases. The credentials are
// the operator's own (the account owner pastes or authorizes their own
// ChatGPT login; only administrators with account permissions can submit
// them), so there is no other party whose claims could be forged against
// them. The plugin copies display fields (email, plan) and the account id,
// which the upstream itself checks against the access token: a wrong id only
// makes the operator's own requests fail. No authorization decision of
// sup2api is ever made on these claims.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil
	}
	seg := strings.TrimRight(parts[1], "=")
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(seg); err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

// parseIdentity reads the identity as the Codex CLI reads it
// (codex-rs login/src/token_data.rs): email at the top level, else in the
// profile claim; chatgpt_account_id, chatgpt_plan_type and
// chatgpt_account_is_fedramp in the auth claim. The id_token comes first,
// the access token (which carries the same claims) fills what it lacks.
// Missing values stay empty.
func parseIdentity(idToken, accessToken string) identity {
	var id identity
	for _, tok := range []string{idToken, accessToken} {
		if c := jwtClaims(tok); c != nil {
			id.fill(c)
		}
	}
	return id
}

// fill sets the fields of id still empty from the claims c of one token.
func (id *identity) fill(c map[string]any) {
	profile, _ := c[profileClaim].(map[string]any)
	auth, _ := c[authClaim].(map[string]any)
	id.Email = cmp.Or(id.Email, claimString(c, "email"), claimString(profile, "email"))
	id.AccountID = cmp.Or(id.AccountID, claimString(auth, "chatgpt_account_id"))
	id.PlanType = cmp.Or(id.PlanType, claimString(auth, "chatgpt_plan_type"))
	fedramp, _ := auth["chatgpt_account_is_fedramp"].(bool)
	id.FedRAMP = id.FedRAMP || fedramp
}

// claimString is the string claim key of c, "" when c or the claim is
// missing or not a string.
func claimString(c map[string]any, key string) string {
	s, _ := c[key].(string)
	return s
}

// tokenExpiry returns the exp claim of a JWT access token, 0 when it has none.
func tokenExpiry(accessToken string) int64 {
	if exp, ok := jwtClaims(accessToken)["exp"].(float64); ok && exp > 0 {
		return int64(exp)
	}
	return 0
}
