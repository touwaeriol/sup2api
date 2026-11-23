package group

import (
	"context"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Models contains only request model IDs; it never exposes account credentials.
type Models struct {
	Models               []string `json:"models"`
	UnrestrictedAccounts int      `json:"unrestricted_accounts"`
}

func (s *Service) models(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, err := s.loadRow(ctx, id); err != nil {
		httpapi.Fail(c, err)
		return
	}
	byGroup, err := ProvidedModels(ctx, s.db.Gorm, []int64{id})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, byGroup[id])
}

// ProvidedModels returns, for each of the groups, the request models its
// accounts declare, sorted, and how many of its accounts declare none (they
// serve every model). Deleted accounts do not count. Every asked group has an
// entry.
func ProvidedModels(ctx context.Context, q *gorm.DB, ids []int64) (map[int64]Models, error) {
	var rows []struct {
		GroupID int64         `gorm:"column:group_id"`
		Models  store.Strings `gorm:"column:models"`
	}
	if err := liveAccounts(store.G(ctx, q), ids).Select("ag.group_id, a.models").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]Models, len(ids))
	for _, id := range ids {
		out[id] = Models{Models: []string{}}
	}
	for _, r := range rows {
		m := out[r.GroupID]
		m.add(r.Models)
		out[r.GroupID] = m
	}
	// Several accounts may declare a model: each is listed once.
	for id, m := range out {
		slices.Sort(m.Models)
		m.Models = slices.Compact(m.Models)
		out[id] = m
	}
	return out, nil
}

// add counts an account that declares models; one that declares none
// serves every model.
func (m *Models) add(models []string) {
	if len(models) == 0 {
		m.UnrestrictedAccounts++
	}
	for _, model := range models {
		if model = strings.TrimSpace(model); model != "" {
			m.Models = append(m.Models, model)
		}
	}
}

// Offers reports whether a group with these accounts provides model: an
// account declares it, or one declares none and the model has a list price
// in the group (priced: an enabled global price or the group's own), and
// the model policy of the group allows it. It is the one place the rule is
// written. Models must be sorted, as ProvidedModels returns them.
func (m Models) Offers(model string, priced bool, policy core.GroupInfo) bool {
	_, declared := slices.BinarySearch(m.Models, model)
	return (declared || priced && m.UnrestrictedAccounts > 0) && policy.AllowsModel(model)
}

// Offered returns the models a group with these accounts provides (Offers),
// sorted; priced are the models with a list price in the group.
func (m Models) Offered(priced []string, policy core.GroupInfo) []string {
	out := []string{}
	keep := func(names []string, havePrice bool) {
		for _, name := range names {
			if m.Offers(name, havePrice, policy) {
				out = append(out, name)
			}
		}
	}
	keep(m.Models, false)
	keep(priced, true)
	slices.Sort(out)
	return slices.Compact(out)
}

// OfferedModels returns the models each of the groups provides
// (Models.Offered), by group id; priced returns the models with a list price
// in a group. Of a group it reads the id and the model policy.
func OfferedModels(ctx context.Context, q *gorm.DB, groups []core.GroupInfo, priced func(groupID int64) []string) (map[int64][]string, error) {
	provided, err := ProvidedModels(ctx, q, core.GroupIDs(groups))
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]string, len(groups))
	for _, g := range groups {
		out[g.ID] = provided[g.ID].Offered(priced(g.ID), g)
	}
	return out, nil
}

// offeredModels returns OfferedModels of the groups against the models that
// have a list price there now (Service.priced).
func (s *Service) offeredModels(ctx context.Context, groups []core.GroupInfo) (map[int64][]string, error) {
	priced, err := s.priced.PricedModels(ctx, core.GroupIDs(groups), "")
	if err != nil {
		return nil, err
	}
	return OfferedModels(ctx, s.db.Gorm, groups, func(id int64) []string { return priced[id] })
}

// groupsProviding returns the ids of the groups that provide the model name
// (Models.Offers); never nil.
func (s *Service) groupsProviding(ctx context.Context, name string) ([]int64, error) {
	db := store.G(ctx, s.db.Gorm)
	groups, err := groupPolicies(db)
	if err != nil {
		return nil, err
	}
	ids := core.GroupIDs(groups)
	provided, err := ProvidedModels(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	priced, err := s.priced.PricedModels(ctx, ids, name)
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for _, g := range groups {
		if provided[g.ID].Offers(name, len(priced[g.ID]) > 0, g) {
			out = append(out, g.ID)
		}
	}
	return out, nil
}

// groupPolicies reads the id and the model policy of every group.
func groupPolicies(db *gorm.DB) ([]core.GroupInfo, error) {
	var rows []struct {
		ID              int64                `gorm:"column:id"`
		ModelAllowlist  store.JSON[[]string] `gorm:"column:model_allowlist"`
		ModelFilterMode *string              `gorm:"column:model_filter_mode"`
	}
	if err := db.Model(&model.Group{}).Select("id", "model_allowlist", "model_filter_mode").Find(&rows).Error; err != nil {
		return nil, err
	}
	groups := make([]core.GroupInfo, len(rows))
	for i, r := range rows {
		groups[i] = core.GroupInfo{ID: r.ID, ModelAllowlist: r.ModelAllowlist.V, ModelFilterMode: core.ValueOr(r.ModelFilterMode, "")}
	}
	return groups, nil
}
