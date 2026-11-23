package install

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// isMarketSource reports whether an upload source is a market source
// ("market:<id>").
func isMarketSource(source string) bool { return strings.HasPrefix(source, "market:") }

// checkSource fails (403, reason source_mismatch) when a market install of
// the installed plugin key comes from another market source than its
// current version (CONTRACTS §136.2): each source vouches only for what it
// lists, so one source cannot take over a plugin another installed. A
// current version uploaded by hand or built in may move to any market.
func checkSource(ctx context.Context, tx *gorm.DB, key, source string) error {
	if !isMarketSource(source) {
		return nil
	}
	var current *string
	err := currentSource(store.G(ctx, tx), key).Row().Scan(&current)
	switch {
	case store.IsNoRows(err):
		return nil
	case err != nil:
		return err
	case current == nil || !isMarketSource(*current) || *current == source:
		return nil
	}
	return core.ErrPermissionDenied.
		WithMessage(fmt.Sprintf("plugin %q was installed from %s: upgrade it from the same market source", key, *current)).
		WithDetails(map[string]any{"reason": "source_mismatch", "source": *current})
}

// currentSource selects the source of the current version of key
// (currentVersion).
func currentSource(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginVersion{}).Select("source").
		Where("plugin_key = ? AND version = (?)", key, currentVersion(db, key))
}
