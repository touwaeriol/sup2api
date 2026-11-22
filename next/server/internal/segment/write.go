package segment

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// groupRatesField is the list of POST/PATCH /segments that sets what the
// segment has in groups: each item a group, the grant and the rate.
const groupRatesField = "group_rates"

// segmentInput is POST /segments and PATCH /segments/:id: the segment and
// its rates, written in one transaction; PATCH sends only what changed.
type segmentInput struct {
	Name        *string          `json:"name"`
	Description *string          `json:"description"`
	GroupRates  []pairInput      `json:"group_rates"`
	ModelRates  []modelRateInput `json:"model_rates"`
}

// textErrors checks the name and the description of a segment of the
// kind, trimming the name; create requires the name. A personal segment
// has neither: it goes by its user.
func (in *segmentInput) textErrors(ctx context.Context, kind string, create bool) []core.FieldError {
	if kind == core.SegmentPersonal && (in.Name != nil || in.Description != nil) {
		return []core.FieldError{core.NewFieldError(ctx, "name", "invalid", "a personal user group has no name or description", "个人用户分组没有名称和说明")}
	}
	var fe []core.FieldError
	name, given := "", in.Name != nil
	if given {
		name = strings.TrimSpace(*in.Name)
		in.Name = &name
	}
	if (given || create) && name == "" {
		fe = append(fe, core.NewFieldError(ctx, "name", "required", "name is required", "名称必填"))
	} else if utf8.RuneCountInString(name) > 100 {
		fe = append(fe, core.NewFieldError(ctx, "name", "invalid", "name must be at most 100 characters", "名称最多 100 个字符"))
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 2000 {
		fe = append(fe, core.NewFieldError(ctx, "description", "too_long", "description is too long", "描述过长"))
	}
	return fe
}

// groupIDs are the distinct groups the rates are in.
func (in *segmentInput) groupIDs() []int64 {
	ids := ends(in.GroupRates, keyGroup)
	for _, it := range in.ModelRates {
		ids = append(ids, it.GroupID)
	}
	return x.Unique(ids)
}

// trimModels trims the models of the model rates, as they are checked and
// stored.
func (in *segmentInput) trimModels() {
	for i := range in.ModelRates {
		in.ModelRates[i].Model = strings.TrimSpace(in.ModelRates[i].Model)
	}
}

// refs reads what the rates refer to. With lock, in a transaction, the
// groups are locked against deletion until it ends.
func (in *segmentInput) refs(ctx context.Context, q *gorm.DB, lock bool) (rateRefs, error) {
	groups, err := knownRows(ctx, q, "groups", in.groupIDs(), lock)
	if err != nil {
		return rateRefs{}, err
	}
	models := make([]string, len(in.ModelRates))
	for i, it := range in.ModelRates {
		models[i] = it.Model
	}
	priced, err := pricedModels(ctx, q, x.Unique(models))
	return rateRefs{groups: groups, priced: priced}, err
}

// rateErrors are the field errors of the rates against what they refer to.
func (in *segmentInput) rateErrors(ctx context.Context, refs rateRefs) []core.FieldError {
	return append(pairErrors(ctx, groupRatesField, keyGroup, in.GroupRates, refs.groups), modelRateErrors(ctx, in.ModelRates, refs)...)
}

// check fails with the field errors of the whole input, for a segment of
// the kind.
func (s *Service) check(ctx context.Context, in *segmentInput, kind string, create bool) error {
	fe := in.textErrors(ctx, kind, create)
	in.trimModels()
	refs, err := in.refs(ctx, s.db.Gorm, false)
	if err != nil {
		return err
	}
	if fe = append(fe, in.rateErrors(ctx, refs)...); len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// writeRates writes the checked rates of segment id, which tx has written:
// the groups are locked and must still exist, and the segment may not be
// the caller's own (guardOwnSegments).
func (s *Service) writeRates(ctx context.Context, tx *gorm.DB, id int64, in *segmentInput) error {
	if len(in.GroupRates)+len(in.ModelRates) == 0 {
		return nil
	}
	if err := guardOwnSegments(ctx, tx, s.users, []int64{id}); err != nil {
		return err
	}
	refs, err := in.refs(ctx, tx, true)
	if err != nil {
		return err
	}
	if fe := in.rateErrors(ctx, refs); len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	for i := range in.GroupRates {
		in.GroupRates[i].SegmentID = id
	}
	if err := putPairs(ctx, tx, in.GroupRates); err != nil {
		return err
	}
	return putModelRates(ctx, tx, id, in.ModelRates)
}

// ratesWritten tells billing, once committed, that model rates changed:
// its price snapshot holds them.
func (s *Service) ratesWritten(ctx context.Context, modelRates bool) {
	if modelRates && s.ratesChanged != nil {
		s.ratesChanged(ctx)
	}
}

// changesModelRates reports whether the write changes model rates: it
// sets some, or removes a pair and with it the segment's model rates in
// that group.
func (in *segmentInput) changesModelRates() bool {
	return len(in.ModelRates) > 0 || removes(in.GroupRates)
}

// columns are the segments columns of the provided fields, as written; the
// others keep their value.
func (in *segmentInput) columns() map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("now()")}
	store.SetIf(set, "name", in.Name)
	store.SetIf(set, "description", in.Description)
	return set
}

// nameTaken turns a clash with the name of another named segment into the
// field error; other errors pass through.
func nameTaken(ctx context.Context, err error) error {
	if store.IsUniqueViolation(err, "segments_name_uq") {
		return core.InvalidFields(core.NewFieldError(ctx, "name", "duplicate", "name already in use", "名称已被使用"))
	}
	return err
}

func (s *Service) create(c *gin.Context) {
	ctx := c.Request.Context()
	var in segmentInput
	if !httpapi.BindStrictJSON(c, &in) {
		return
	}
	if err := s.check(ctx, &in, core.SegmentNamed, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	row := model.Segment{Name: *in.Name, Description: core.ValueOr(in.Description, "")}
	err := s.tx(ctx, func(tx *gorm.DB) error {
		if err := store.G(ctx, tx).Create(&row).Error; err != nil {
			return nameTaken(ctx, err)
		}
		return s.writeRates(ctx, tx, row.ID, &in)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.ratesWritten(ctx, in.changesModelRates())
	s.render(c, row.ID, httpapi.Created)
}

func (s *Service) update(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in segmentInput
	if !httpapi.BindStrictJSON(c, &in) {
		return
	}
	kind, _, err := segmentKind(ctx, s.db.Gorm, id, "")
	if err == nil {
		err = s.check(ctx, &in, kind, false)
	}
	if err == nil {
		err = s.tx(ctx, func(tx *gorm.DB) error {
			if err := updateSegment(ctx, tx, id, &in); err != nil {
				return err
			}
			return s.writeRates(ctx, tx, id, &in)
		})
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.ratesWritten(ctx, in.changesModelRates())
	s.render(c, id, httpapi.OK)
}

// updateSegment writes the fields of in present to segment id, locking it
// until tx ends.
func updateSegment(ctx context.Context, tx *gorm.DB, id int64, in *segmentInput) error {
	res := segmentUpdate(store.G(ctx, tx), id, in)
	if res.Error != nil {
		return nameTaken(ctx, res.Error)
	}
	if res.RowsAffected == 0 {
		return NotFound(ctx)
	}
	return nil
}

// segmentUpdate writes the fields of in present to segment id.
func segmentUpdate(db *gorm.DB, id int64, in *segmentInput) *gorm.DB {
	return db.Model(&model.Segment{}).Where("id = ?", id).Updates(in.columns())
}
