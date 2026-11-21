package billing

import (
	"context"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
)

// MyPriceRow is a row of GET /me/prices.
type MyPriceRow struct {
	Model string `json:"model"`
	PriceDef
	// Groups (without a group): the caller's groups that provide the model.
	Groups []core.GroupRef `json:"groups,omitzero"`
	// Source (with a group): where the list price comes from.
	Source string `json:"source,omitempty"`
}

// MyPrices is the response of GET /me/prices.
type MyPrices struct {
	Group          *core.GroupRef  `json:"group"`
	RateMultiplier decimal.Decimal `json:"rate_multiplier"`
	RateSource     string          `json:"rate_source,omitempty"`
	Prices         []MyPriceRow    `json:"prices"`
}

// myPrices answers GET /me/prices?group_id= (CONTRACTS §79.5) from the price
// snapshot requests are billed by. Without a group it is every model at its
// global price, with the caller's groups that provide it. With one it is the
// models that group provides and can bill, at the list price and the rate
// the caller gets there. A group the caller cannot use is a 404.
func (s *Service) myPrices(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var groupID int64
	if raw := c.Query("group_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("invalid group_id"))
			return
		}
		groupID = id
	}
	var out *MyPrices
	var err error
	if groupID == 0 {
		out, err = s.pricesOfGroups(ctx, uid)
	} else {
		out, err = s.pricesInGroup(ctx, uid, groupID)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// usableBy selects the active groups a user may use; it takes the user's id.
const usableBy = segment.UsableGroup

// pricesOfGroups lists every model with a global price, at that price, with
// the groups of the user that provide it.
func (s *Service) pricesOfGroups(ctx context.Context, uid int64) (*MyPrices, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := loadGroups(ctx, s.db.Gorm, usableBy, uid)
	if err != nil {
		return nil, err
	}
	offered, err := offeredModels(ctx, s.db.Gorm, groups, snap.bases)
	if err != nil {
		return nil, err
	}
	out := &MyPrices{RateMultiplier: decimal.NewFromInt(1), Prices: []MyPriceRow{}}
	for _, model := range snap.bases.models() {
		row := MyPriceRow{Model: model, PriceDef: snap.bases[model], Groups: []core.GroupRef{}}
		for _, g := range groups {
			if _, ok := slices.BinarySearch(offered[g.ID], model); ok {
				row.Groups = append(row.Groups, g.ref())
			}
		}
		out.Prices = append(out.Prices, row)
	}
	return out, nil
}

// pricesInGroup lists the models one of the user's groups provides and can
// bill, at the list price and the rate the user gets there.
func (s *Service) pricesInGroup(ctx context.Context, uid, groupID int64) (*MyPrices, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	gp, err := loadGroupPrices(ctx, s.db.Gorm, snap.bases, usableBy+" AND g.id = ?", uid, groupID)
	if err != nil {
		return nil, err
	}
	terms, err := segment.UserTerms(ctx, s.db.Gorm, uid, []int64{groupID})
	if err != nil {
		return nil, err
	}
	t, ok := terms[groupID]
	if !ok {
		return nil, core.ErrNotFound
	}
	group := gp.group.ref()
	out := &MyPrices{Group: &group, RateMultiplier: t.Rate, RateSource: t.RateSource, Prices: []MyPriceRow{}}
	snap.each(groupID, gp.models, func(model string, base PriceDef, rule *storedRule) {
		out.Prices = append(out.Prices, MyPriceRow{Model: model, PriceDef: listPrice(base, rule), Source: rule.source()})
	})
	return out, nil
}
