package group

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

func dupName(ctx context.Context) error {
	return core.InvalidFields(fieldError(ctx, "name", "duplicate", "a group with this name already exists", "分组名称已存在"))
}

func (s *Service) create(c *gin.Context) {
	ctx := c.Request.Context()
	var in groupInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.validate(ctx, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	row := in.newRow()
	err := store.G(ctx, s.db.Gorm).Create(row).Error
	if store.IsUniqueViolation(err, "") {
		httpapi.Fail(c, dupName(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	g, err := s.load(ctx, row.ID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, g)
}

// newRow is the groups row a create of the validated input writes: the
// fields present as given (a 0 rate included), the column defaults for the
// others.
func (in *groupInput) newRow() *model.Group {
	row := &model.Group{Name: *in.Name, Description: core.ValueOr(in.Description, ""), Status: in.Status,
		Visibility: in.Visibility, ModelFilterMode: in.ModelFilterMode, ConversionLossPolicy: in.ConversionLossPolicy.Stored()}
	if in.RateMultiplier != nil {
		rate := decimal.RequireFromString(*in.RateMultiplier) // validate normalized it
		row.RateMultiplier = &rate
	}
	if in.ModelAllowlist != nil {
		row.ModelAllowlist, _ = json.Marshal(*in.ModelAllowlist)
	}
	if in.Environment != nil {
		row.Environment, _ = json.Marshal(*in.Environment)
	}
	return row
}

func (s *Service) update(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in groupInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	err := in.validate(ctx, false)
	if err == nil {
		err = s.updateRow(ctx, id, &in)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropKeyCache(ctx, id)
	g, err := s.load(ctx, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, g)
}

// updateRow writes the fields of the validated input present to group id; a
// field left out keeps its value. A name another group has is the field
// error.
func (s *Service) updateRow(ctx context.Context, id int64, in *groupInput) error {
	res := store.G(ctx, s.db.Gorm).Model(&model.Group{}).Where("id = ?", id).Updates(in.columns())
	switch {
	case store.IsUniqueViolation(res.Error, ""):
		return dupName(ctx)
	case res.Error != nil:
		return res.Error
	case res.RowsAffected == 0:
		return groupNotFound(ctx)
	}
	return nil
}
