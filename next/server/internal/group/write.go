package group

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

func dupName(ctx context.Context) error {
	return core.InvalidFields(core.NewFieldError(ctx, "name", "duplicate", "a group with this name already exists", "分组名称已存在"))
}

// groupWrite is a checked POST /groups or PATCH /groups/:id: the group's own
// fields and the writes of the parts (Service.Parts) that the body has.
type groupWrite struct {
	in    groupInput
	parts []core.GroupWrite
}

// bindWrite reads the body of a group write once, checks the group's own
// fields and every part's, and renders their field errors as one
// invalid_argument. id is 0 on a create.
func (s *Service) bindWrite(c *gin.Context, id int64) (*groupWrite, bool) {
	ctx := c.Request.Context()
	w := &groupWrite{}
	body, err := decodeWrite(c, &w.in)
	if err == nil {
		err = s.prepare(ctx, id, w, body)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return nil, false
	}
	return w, true
}

// decodeWrite decodes the request body into the group's own fields and, by
// key, for the parts.
func decodeWrite(c *gin.Context, in *groupInput) (map[string]json.RawMessage, error) {
	raw, err := c.GetRawData()
	var body map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	if err == nil {
		err = json.Unmarshal(raw, in)
	}
	if err != nil {
		return nil, core.ErrInvalidArgument.WithMessage(err.Error())
	}
	return body, nil
}

// prepare checks the group's own fields and has every part prepare its
// write; the field errors of all of them fail together.
func (s *Service) prepare(ctx context.Context, id int64, w *groupWrite, body map[string]json.RawMessage) error {
	fe := w.in.check(ctx, id == 0)
	for _, part := range s.Parts {
		write, errs, err := part.PrepareGroupWrite(ctx, id, body)
		if err != nil {
			return err
		}
		fe = append(fe, errs...)
		if write.Apply != nil || write.AfterCommit != nil {
			w.parts = append(w.parts, write)
		}
	}
	if len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// save writes the group row (put) and then the parts in one transaction,
// and runs what the parts do after the commit.
func (s *Service) save(ctx context.Context, w *groupWrite, put func(tx *gorm.DB) (int64, error)) (int64, error) {
	var id int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) (err error) {
		if id, err = put(tx); err != nil {
			return err
		}
		for _, p := range w.parts {
			if p.Apply == nil {
				continue
			}
			if err := p.Apply(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, p := range w.parts {
		if p.AfterCommit != nil {
			p.AfterCommit(ctx, id)
		}
	}
	return id, nil
}

func (s *Service) create(c *gin.Context) {
	ctx := c.Request.Context()
	w, ok := s.bindWrite(c, 0)
	if !ok {
		return
	}
	id, err := s.save(ctx, w, func(tx *gorm.DB) (int64, error) { return insertRow(ctx, tx, &w.in) })
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.respond(c, id, httpapi.Created)
}

// insertRow inserts the group of the checked input and returns its id.
func insertRow(ctx context.Context, tx *gorm.DB, in *groupInput) (int64, error) {
	row := in.newRow()
	err := store.G(ctx, tx).Create(row).Error
	if store.IsUniqueViolation(err, "") {
		return 0, dupName(ctx)
	}
	return row.ID, err
}

// newRow is the groups row a create of the checked input writes: the
// fields present as given (a 0 rate included), the column defaults for the
// others.
func (in *groupInput) newRow() *model.Group {
	row := &model.Group{Name: *in.Name, Description: core.ValueOr(in.Description, ""), Status: in.Status,
		Visibility: in.Visibility, ModelFilterMode: in.ModelFilterMode, ConversionLossPolicy: in.ConversionLossPolicy.Stored()}
	if in.RateMultiplier != nil {
		rate := decimal.RequireFromString(*in.RateMultiplier) // check normalized it
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
	w, ok := s.bindWrite(c, id)
	if !ok {
		return
	}
	if _, err := s.save(ctx, w, func(tx *gorm.DB) (int64, error) { return id, updateRow(ctx, tx, id, &w.in) }); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropKeyCache(ctx, id)
	s.respond(c, id, httpapi.OK)
}

// respond answers with group id as GET /groups lists it.
func (s *Service) respond(c *gin.Context, id int64, respond func(*gin.Context, any)) {
	g, err := s.load(c.Request.Context(), id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	respond(c, g)
}

// updateRow writes the fields of the checked input present to group id,
// locking the row until tx ends; a field left out keeps its value. A name
// another group has is the field error.
func updateRow(ctx context.Context, tx *gorm.DB, id int64, in *groupInput) error {
	res := store.G(ctx, tx).Model(&model.Group{}).Where("id = ?", id).Updates(in.columns())
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
