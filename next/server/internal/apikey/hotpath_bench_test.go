package apikey

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The key lookup is the first database statement of every gateway request
// (Gateway.serve → Authenticate) and is not cached by design (see cached).
// Its GORM form (gorm, lookup) is measured next to the same statement on
// raw database/sql (sql), against a real PostgreSQL (TEST_DATABASE_URL), as
// recorded in internal/store/gormtest/BASELINE.md:
//
//	go test -run '^$' -bench Hot -benchmem -benchtime 1000x -count 5 ./internal/apikey/
func BenchmarkHotKeyLookup(b *testing.B) {
	ctx := context.Background()
	db := testutil.DB(b)
	user := testutil.InsertUser(b, db, "bench@test.invalid")
	group := testutil.InsertGroup(b, db, "bench")
	raw, err := generateKey()
	if err != nil {
		b.Fatal(err)
	}
	hash := HashKey(raw)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO api_keys (user_id, group_id, name, key_prefix, key_hash) VALUES ($1, $2, 'k', $3, $4)`,
		user, group, raw[:displayPrefix], hash); err != nil {
		b.Fatal(err)
	}
	svc := New(db, nil, nil, nil)
	// rawLookupSQL is lookupQuery's statement.
	rawLookupSQL, _, err := gormtest.Build(func(tx *gorm.DB) *gorm.DB { return lookupQuery(tx, hash).Scan(&struct{}{}) })
	if err != nil {
		b.Fatal(err)
	}
	for _, bc := range []struct {
		name   string
		lookup func() (*cached, error)
	}{
		{"sql", func() (*cached, error) {
			e := &cached{}
			err := db.SQL.QueryRowContext(ctx, rawLookupSQL, hash).Scan(
				&e.KeyID, &e.UserID, &e.KeyStatus, &e.ExpiresAt, &e.UserActive,
				&e.GroupID, &e.GroupName, &e.GroupStatus, &e.RateMultiplier, &e.ModelAllowlist, &e.ModelFilterMode, &e.GroupEnvironment, &e.GroupAvailable, &e.AllowedIPs,
				&e.KeyLimits.RPM, &e.KeyLimits.TPM, &e.KeyLimits.Concurrency, &e.KeyQuota.Limit, &e.KeyQuota.Used, &e.GroupLossPolicy,
				&e.SegmentID, &e.RateOverride)
			e.Found = err == nil
			return e, err
		}},
		{"gorm", func() (*cached, error) { return svc.lookup(ctx, hash) }},
	} {
		b.Run(bc.name, func(b *testing.B) {
			if e, err := bc.lookup(); err != nil || !e.Found {
				b.Fatalf("lookup: %+v, %v", e, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := bc.lookup(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
