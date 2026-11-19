package conformance

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// --- 12 ---

// balanceItems has an item per account type that declares a balance or
// has a case: the first case's account of the type is queried.
func balanceItems(s *suite) []item {
	var out []item
	for _, at := range s.declared.m.AccountTypes {
		account := s.sampleAccount(at.ID)
		declared := at.Balance != nil
		if account == nil && !declared {
			continue
		}
		out = append(out, item{"balance: " + at.ID, func(t testing.TB) {
			switch {
			case account == nil:
				t.Errorf("the type declares manifest balance but no case has an account of it: add one")
			case declared:
				s.checkBalance(t, account)
			default:
				s.checkNoBalance(t, account)
			}
		}})
	}
	return out
}

// sampleAccount is the account of the first case of accountType, nil for
// none.
func (s *suite) sampleAccount(accountType string) *pluginv1.Account {
	for _, c := range s.cases {
		if c.Account.GetType() == accountType {
			return c.Account
		}
	}
	return nil
}

// unreadable are answers no balance can be read from.
var unreadable = []struct {
	what   string
	answer *pluginv1.BalanceAnswer
}{
	{"a transport error", &pluginv1.BalanceAnswer{TransportError: "conformance: connection refused"}},
	{"an empty 200 answer", &pluginv1.BalanceAnswer{Status: 200}},
}

// checkBalance queries account's balance through the fake host, which
// refuses a request to a host the core would not send it to, with answers
// the plugin cannot read: each must be an error result.
func (s *suite) checkBalance(t testing.TB, account *pluginv1.Account) {
	h := s.serve(t, s.p.Platform, s.p.Manifest, s.p.SDK, s.p.Config, upstream{})
	for _, u := range unreadable {
		ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
		res, err := h.Balance(ctx, account, func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer { return u.answer })
		cancel()
		switch {
		case status.Code(err) == codes.Unimplemented:
			t.Errorf("the type declares manifest balance but the plugin does not read it: implement pluginsdk.BalanceReader (standard.Type.Balance): %v", err)
			return
		case err != nil:
			t.Errorf("%s: %v", u.what, err)
		case res.GetErrorType() == pluginv1.BalanceResult_ERROR_TYPE_UNSPECIFIED:
			t.Errorf("%s read as a balance (%s): an answer the plugin cannot read is an ERROR_TYPE_TRANSIENT result", u.what, res)
		}
	}
}

// checkNoBalance: a type that declares no balance answers UNIMPLEMENTED,
// or the balance it reads is never shown.
func (s *suite) checkNoBalance(t testing.TB, account *pluginv1.Account) {
	h := s.serve(t, s.p.Platform, s.p.Manifest, s.p.SDK, s.p.Config, upstream{})
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	defer cancel()
	_, err := h.Platform.BuildBalanceRequest(ctx, &pluginv1.BuildBalanceRequestRequest{Account: account})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("the type declares no manifest balance but BuildBalanceRequest does not answer UNIMPLEMENTED (%v): declare \"balance\" or answer UNIMPLEMENTED", err)
	}
}
