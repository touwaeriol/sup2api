package codex

import "testing"

// The identity is read where the Codex CLI reads it: email at the top level
// or in the profile claim, the account facts in the auth claim; the
// id_token first, the access token filling the gaps.
func TestParseIdentity(t *testing.T) {
	id := fakeJWT(map[string]any{profileClaim: map[string]any{"email": "p@example.com"}, authClaim: map[string]any{"chatgpt_plan_type": "plus"}})
	at := fakeJWT(map[string]any{"email": "a@example.com", authClaim: map[string]any{"chatgpt_account_id": "acct-a", "chatgpt_plan_type": "pro", "chatgpt_account_is_fedramp": true}})
	want := identity{Email: "p@example.com", AccountID: "acct-a", PlanType: "plus", FedRAMP: true}
	if got := parseIdentity(id, at); got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
	if got := parseIdentity("not a jwt", ""); got != (identity{}) {
		t.Fatalf("unreadable tokens = %+v", got)
	}
}
