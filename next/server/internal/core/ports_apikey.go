package core

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// ============================================================ api keys & groups (owner: A)

type GroupInfo struct {
	ID              int64
	Name            string
	Status          string
	RateMultiplier  decimal.Decimal
	ModelAllowlist  []string // globs; empty = all
	ModelFilterMode string   // whitelist (default) or blacklist
	// Environment is the group layer of the environment variables (§74.3).
	Environment EnvironmentValues
	// ConversionLossPolicy is the group's conversion loss policy (§88), ""
	// to leave it to the accounts (default allow).
	ConversionLossPolicy string
}

// APIKeyPrincipal is the result of gateway authentication. Group.RateMultiplier
// is the group's base rate.
type APIKeyPrincipal struct {
	KeyID  int64
	UserID int64
	// SegmentID is the segment of the key's owner and RateOverride its own
	// rate in the key's group (nil = none).
	SegmentID    int64
	RateOverride *decimal.Decimal
	// KeyLimits and KeyQuota are the caps the key's owner set on it.
	KeyLimits KeyLimits
	KeyQuota  KeyQuota
	Group     GroupInfo
}

// Rates are the rate levels of the key's owner in the key's group, the
// model aside: Pricer.Resolve adds those of a model.
func (p *APIKeyPrincipal) Rates() RateLevels {
	return RateLevels{Segment: p.RateOverride, Group: p.Group.RateMultiplier}
}

// KeyQuota is an API key's spend cap in USD (Limit 0 = unlimited) and what
// its requests were charged so far (CONTRACTS §61).
type KeyQuota struct {
	Limit decimal.Decimal `json:"quota_limit"`
	Used  decimal.Decimal `json:"quota_used"`
}

// Exhausted reports whether a capped key has spent its quota.
func (q KeyQuota) Exhausted() bool { return q.Limit.IsPositive() && q.Used.GreaterThanOrEqual(q.Limit) }

// KeyLimits caps one API key (0 = unlimited): requests and tokens per minute
// and requests in flight, all counted across the nodes.
type KeyLimits struct {
	RPM         int   `json:"rpm_limit"`
	TPM         int64 `json:"tpm_limit"`
	Concurrency int   `json:"max_concurrency"`
}

// KeyUsageReporter answers GET /v1/usage for an authenticated API key: what
// it can still spend, its caps and what it used (CONTRACTS §62). The answer
// is written as JSON.
type KeyUsageReporter interface {
	KeyUsage(ctx context.Context, p *APIKeyPrincipal) (any, error)
}

// KeySlots is the Slots kind of the API keys' requests in flight.
const KeySlots = "apikey"

// AccountWaitSlots is the Slots kind of the requests queueing for an account
// (docs/GATEWAY-CONCURRENCY-DESIGN.md §3).
const AccountWaitSlots = "account-wait"

// RateHold is one admitted request in the rate windows: what it reserved in
// each set of counters of its subject, until Settle replaces that by what the
// request really used. At is the Redis time it was admitted at: the
// reservations sit in the window's segment of that time (CONTRACTS §68.3). A
// hold never settled returns by itself when that segment leaves the window.
type RateHold struct {
	Subject int64
	At      time.Time
	Items   []RateHoldItem
}

// RateHoldItem is what a hold reserved in the counters Sub ("" = the
// subject's own).
type RateHoldItem struct {
	Sub    string
	Tokens int64
}

// RateWindows counts requests and tokens of one kind of subject (API keys)
// over the last 60 seconds, shared by every node. Admit counts one request
// when the subject is under rpm and tpm (0 = unlimited), else returns the time
// until it would pass; Reserve also reserves tokens until Settle; AddTokens
// counts a finished response admitted without a reservation.
type RateWindows interface {
	Admit(ctx context.Context, id int64, rpm int, tpm int64) (ok bool, retry time.Duration, err error)
	Reserve(ctx context.Context, id int64, rpm int, tpm, tokens int64) (hold RateHold, ok bool, retry time.Duration, err error)
	Settle(ctx context.Context, hold RateHold, actual int64)
	AddTokens(ctx context.Context, id, n int64)
	Usage(ctx context.Context, ids []int64) (map[int64]RateUsage, error)
}

// APIKeyAuthenticator resolves a raw client key. Errors: ErrUnauthenticated
// (unknown/disabled/expired key or disabled user), ErrPermissionDenied (user
// lacks gateway:use or the group is not available to the user).
type APIKeyAuthenticator interface {
	Authenticate(ctx context.Context, rawKey string) (*APIKeyPrincipal, error)
}
