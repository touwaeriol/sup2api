package e2e

import (
	"fmt"
	"math/big"
	"sync"
	"testing"
)

// Tenant is an isolated slice of the system for one test: a restricted group
// with its own mock-backed accounts, a user allowed to use it (with balance)
// and an API key bound to it.
type Tenant struct {
	GroupID  int64
	Accounts []TenantAccount
	User     *Session
	KeyID    int64
	APIKey   string
	Model    string // a model covered by the run's per-token price
}

// TenantAccount is an upstream account of a tenant.
type TenantAccount struct {
	ID  int64
	Key string // upstream api_key, recorded by the mock as x-api-key
}

// TenantOpts configures NewTenant.
type TenantOpts struct {
	Accounts   int     // default 1
	Balance    string  // initial credit, default "10"; "0" = none
	Rate       float64 // group rate multiplier, default 1
	Priorities []int   // per-account priority (default all 10)
}

// NewTenant builds a tenant. Requires identity, resources, billing and an
// enabled anthropic plugin (EnsurePlugin is called).
func (e *Env) NewTenant(admin *Session, o TenantOpts) *Tenant {
	e.T.Helper()
	if o.Accounts == 0 {
		o.Accounts = 1
	}
	if o.Balance == "" {
		o.Balance = "10"
	}
	if o.Rate == 0 {
		o.Rate = 1
	}
	e.EnsurePlugin(admin, "anthropic", "")
	tn := e.newTenantShell(admin, e.Name("grp"), o.Rate, o.Balance)
	tn.Model = e.RunModel(admin, "sonnet")
	for i := 0; i < o.Accounts; i++ {
		prio := 10
		if i < len(o.Priorities) {
			prio = o.Priorities[i]
		}
		key := fmt.Sprintf("sk-ant-mock-%s-%d", e.RunID, nextSeq())
		tn.addAccount(e, admin, AccountSpec{APIKey: key, Priority: prio}, key)
	}
	return tn
}

// newTenantShell creates a tenant without accounts: a restricted group
// named groupName with rate, a user allowed to use it with credit balance
// ("0" = none) and a key of the group. The caller adds the accounts
// (addAccount) and sets Model.
func (e *Env) newTenantShell(admin *Session, groupName string, rate float64, balance string) *Tenant {
	e.T.Helper()
	tn := &Tenant{GroupID: e.CreateGroup(admin, groupName, "restricted", rate, nil)}
	tn.User = e.CreateUser(admin, UserSpec{})
	e.GrantGroups(admin, tn.User.UserID, []int64{tn.GroupID})
	if balance != "0" {
		e.AdjustBalance(admin, tn.User.UserID, balance, true, "e2e initial credit")
	}
	tn.KeyID, tn.APIKey = e.CreateAPIKey(tn.User, tn.GroupID)
	return tn
}

// addAccount creates spec in the tenant's group; key is the upstream key
// the mock records for it (the one spec carries). It returns the account.
func (tn *Tenant) addAccount(e *Env, admin *Session, spec AccountSpec, key string) TenantAccount {
	e.T.Helper()
	spec.GroupIDs = []int64{tn.GroupID}
	a := TenantAccount{ID: e.CreateAccount(admin, spec), Key: key}
	tn.Accounts = append(tn.Accounts, a)
	return a
}

// AccountKeys returns the upstream keys of the tenant's accounts.
func (tn *Tenant) AccountKeys() map[string]bool {
	m := map[string]bool{}
	for _, a := range tn.Accounts {
		m[a.Key] = true
	}
	return m
}

// Per-token price of the run model (USD per million tokens).
var RunPrice = map[string]float64{"p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6}

// RunPriceConfig is RunPrice as a visual config: one tier for every
// request, no surcharge rules (CONTRACTS §131.2).
func RunPriceConfig() map[string]any {
	tier := map[string]any{"name": "base", "unit": "token"}
	for k, v := range RunPrice {
		tier[k] = v
	}
	return map[string]any{"tiers": []any{tier}, "rules": []any{}}
}

var (
	runPriceMu   sync.Mutex
	runPriceDone = map[string]bool{}
)

// RunModel returns "claude-e2e-<run>-<suffix>" and makes sure an admin
// per-token price (RunPrice) exists for exactly that model id.
func (e *Env) RunModel(admin *Session, suffix string) string {
	e.T.Helper()
	return e.RunModelOf(admin, "claude", suffix)
}

// RunModelOf returns "<family>-e2e-<run>-<suffix>" (family e.g. "claude",
// "gpt", "gemini") and makes sure an admin per-token price (RunPrice) exists
// for that model id (prices match complete model ids, no wildcards). Admin
// prices win over plugin defaults.
func (e *Env) RunModelOf(admin *Session, family, suffix string) string {
	e.T.Helper()
	model := fmt.Sprintf("%s-e2e-%s-%s", family, e.RunID, suffix)
	runPriceMu.Lock()
	defer runPriceMu.Unlock()
	if !runPriceDone[model] {
		e.CreatePrice(admin, map[string]any{
			"model":  model,
			"mode":   "visual",
			"config": RunPriceConfig(),
			"note":   "e2e run price",
		})
		runPriceDone[model] = true
	}
	return model
}

// ExpectedTokenCost computes the per-token cost (USD) for usage with price
// coefficients per million tokens, times rate.
func ExpectedTokenCost(price map[string]float64, p, c, cr, cc, cc1h int64, rate float64) *big.Rat {
	sum := new(big.Rat)
	add := func(tokens int64, per float64) {
		x := new(big.Rat).SetInt64(tokens)
		x.Mul(x, new(big.Rat).SetFloat64(per))
		sum.Add(sum, x)
	}
	add(p, price["p"])
	add(c, price["c"])
	add(cr, price["cr"])
	add(cc, price["cc"])
	add(cc1h, price["cc1h"])
	sum.Quo(sum, big.NewRat(1_000_000, 1))
	return sum.Mul(sum, new(big.Rat).SetFloat64(rate))
}

// Money parses a decimal money string.
func Money(t testing.TB, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("not a decimal amount: %q", s)
	}
	return r
}

// AssertMoney requires got (decimal string, <= 8 decimals) to equal want
// rounded to 8 decimals (within 1e-8).
func AssertMoney(t testing.TB, what, got string, want *big.Rat) {
	t.Helper()
	g := Money(t, got)
	diff := new(big.Rat).Sub(g, want)
	diff.Abs(diff)
	if diff.Cmp(big.NewRat(1, 100_000_000)) > 0 {
		t.Fatalf("%s: got %s, want %s", what, got, want.FloatString(8))
	}
}
