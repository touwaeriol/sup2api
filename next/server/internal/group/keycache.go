package group

import (
	"context"
	"log/slog"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// dropKeyCache deletes cached API key principals ("apikey:{sha256}", see
// CONTRACTS §7) for the keys of the group, so a change of the group applies
// immediately instead of after the cache TTL.
func (s *Service) dropKeyCache(ctx context.Context, groupID int64) {
	if s.rdb == nil {
		return
	}
	var hashes []string
	if err := store.G(ctx, s.db.Gorm).Model(&model.APIKey{}).Where("group_id = ?", groupID).Pluck("key_hash", &hashes).Error; err != nil {
		slog.WarnContext(ctx, "group: list api keys for cache drop", "err", err)
		return
	}
	s.delKeys(ctx, hashes)
}

func (s *Service) delKeys(ctx context.Context, hashes []string) {
	if s.rdb == nil || len(hashes) == 0 {
		return
	}
	for start := 0; start < len(hashes); start += 500 {
		end := min(start+500, len(hashes))
		keys := make([]string, 0, end-start)
		for _, h := range hashes[start:end] {
			keys = append(keys, "apikey:"+h)
		}
		if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
			slog.WarnContext(ctx, "group: drop api key cache", "err", err)
			return
		}
	}
}
