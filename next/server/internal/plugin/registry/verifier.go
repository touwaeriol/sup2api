package registry

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// TrustVerifier verifies packages with the plugin trust store as their
// version rows were recorded (TrustStore.VerifyRecorded: official root keys
// and publisher_keys, or the publisher of a market install, read through
// q). lim bounds unpacking and should match the upload limits.
func TrustVerifier(trust *pluginpkg.TrustStore, q *gorm.DB, lim pluginpkg.Limits) PackageVerifier {
	return func(ctx context.Context, key, version string, data []byte) error {
		p, err := pluginpkg.Open(data, lim)
		if err != nil {
			return err
		}
		if p.Manifest.Key != key || p.Manifest.Version != version {
			return fmt.Errorf("package manifest identifies %s@%s", p.Manifest.Key, p.Manifest.Version)
		}
		return trust.VerifyRecorded(ctx, q, p)
	}
}
