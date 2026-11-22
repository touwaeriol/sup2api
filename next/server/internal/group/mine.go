package group

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// MyGroup is the reduced view returned by GET /me/groups.
type MyGroup struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	RateMultiplier  string   `json:"rate_multiplier"`
	ModelAllowlist  []string `json:"model_allowlist"`
	ModelFilterMode string   `json:"model_filter_mode"`
	Platforms       []string `json:"platforms"`
	// ServedProtocols are the inbound protocols the accounts of the group
	// serve, as GET /groups/:id reports them but without the number of
	// accounts serving each: what the caller's keys of the group can call.
	ServedProtocols []MyServedProtocol `json:"served_protocols"`
	// EffectiveRate is the rate the caller pays in the group, model rates
	// aside, and RateSource where it comes from (segment.Terms);
	// RateMultiplier stays the group's base rate.
	EffectiveRate decimal.Decimal `json:"effective_rate"`
	RateSource    string          `json:"rate_source"`
	// Exclusive marks a private group the caller's segment is granted.
	Exclusive bool `json:"exclusive"`
}

// MyServedProtocol is a protocol a group serves as GET /me/groups shows it.
type MyServedProtocol struct {
	Protocol string `json:"protocol"`
}

func (s *Service) myGroups(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	out, err := myGroupRows(ctx, s.db.Gorm, uid)
	if err == nil {
		out, err = s.completeMine(ctx, uid, out)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// myGroupRows reads the groups the user may use, by id, without what
// completeMine adds.
func myGroupRows(ctx context.Context, q *gorm.DB, uid int64) ([]MyGroup, error) {
	var rows []struct {
		ID              int64                `gorm:"column:id"`
		Name            string               `gorm:"column:name"`
		Description     string               `gorm:"column:description"`
		RateMultiplier  *decimal.Decimal     `gorm:"column:rate_multiplier"`
		ModelAllowlist  store.JSON[[]string] `gorm:"column:model_allowlist"`
		ModelFilterMode *string              `gorm:"column:model_filter_mode"`
		Exclusive       bool                 `gorm:"column:exclusive"`
	}
	if err := store.G(ctx, q).Table("groups g").
		Select("g.id, g.name, g.description, g.rate_multiplier, g.model_allowlist, g.model_filter_mode, g.visibility = 'restricted' AS exclusive").
		Where(segment.UsableGroup, uid).Order("g.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]MyGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, MyGroup{ID: r.ID, Name: r.Name, Description: r.Description, RateMultiplier: r.RateMultiplier.String(),
			ModelAllowlist: apiAllowlist(r.ModelAllowlist), ModelFilterMode: core.ValueOr(r.ModelFilterMode, ""), Exclusive: r.Exclusive})
	}
	return out, nil
}

// completeMine adds to the user's groups their platforms, the protocols they
// serve and the rate the user pays there. A group deleted since it was
// listed has no terms: it is left out.
func (s *Service) completeMine(ctx context.Context, uid int64, groups []MyGroup) ([]MyGroup, error) {
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	ps, err := s.Platforms(ctx, ids)
	if err != nil {
		return nil, err
	}
	terms, err := segment.UserTerms(ctx, s.db.Gorm, uid, ids)
	if err != nil {
		return nil, err
	}
	served, err := s.servedProtocols(ctx, ids)
	if err != nil {
		return nil, err
	}
	mine := groups[:0]
	for _, g := range groups {
		tm, ok := terms[g.ID]
		if !ok {
			continue
		}
		g.Platforms, g.ServedProtocols = ps[g.ID], myServed(served[g.ID])
		rate := segment.RateOf(tm.Levels())
		g.EffectiveRate, g.RateSource = rate.Value, rate.Source
		mine = append(mine, g)
	}
	return mine, nil
}

// myServed drops the account counts of served; never nil.
func myServed(served []core.GroupServedProtocol) []MyServedProtocol {
	out := make([]MyServedProtocol, len(served))
	for i, p := range served {
		out[i] = MyServedProtocol{Protocol: p.Protocol}
	}
	return out
}
