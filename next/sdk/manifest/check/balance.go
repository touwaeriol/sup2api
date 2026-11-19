package check

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

const (
	minBalanceUpdateInterval = 60
	maxBalanceUpdateInterval = 86400
)

// balance validates accountTypes[].balance (CONTRACTS §52): the update
// interval bounds (0 is the host's default).
func (v *validator) balance(f string, b *manifest.AccountBalance) {
	if b == nil {
		return
	}
	if b.UpdateInterval != 0 && (b.UpdateInterval < minBalanceUpdateInterval || b.UpdateInterval > maxBalanceUpdateInterval) {
		v.add(f+".balance.updateInterval", "out_of_range", "updateInterval must be %d-%d", minBalanceUpdateInterval, maxBalanceUpdateInterval)
	}
}
