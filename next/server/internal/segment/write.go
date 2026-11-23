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

// modelRatesField is the list of model rates of POST/PATCH /segments.
const modelRatesField = "model_rates"

// rates are the rates of the input, seen from the segment.
func (in *segmentInput) rates() rateWrite {
	return rateWrite{far: keyGroup, pairList: groupRatesField, modelList: modelRatesField, pairs: in.GroupRates, models: in.ModelRates}
}

// check fails with the field errors of the whole input, its rates w, for a
// segment of the kind.
func (s *Service) check(ctx context.Context, in *segmentInput, w *rateWrite, kind string, create bool) error {
	fe := in.textErrors(ctx, kind, create)
	rateErrs, err := w.check(ctx, s.db.Gorm)
	if err != nil {
		return err
	}
	if fe = append(fe, rateErrs...); len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// ratesWritten tells billing, once committed, that model rates changed:
// its price snapshot holds them.
func (s *Service) ratesWritten(ctx context.Context, modelRates bool) {
	if modelRates && s.ratesChanged != nil {
		s.ratesChanged(ctx)
	}
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
	w := in.rates()
	if err := s.check(ctx, &in, &w, core.SegmentNamed, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	row := model.Segment{Name: *in.Name, Description: core.ValueOr(in.Description, "")}
	err := s.tx(ctx, func(tx *gorm.DB) error {
		if err := store.G(ctx, tx).Create(&row).Error; err != nil {
			return nameTaken(ctx, err)
		}
		return s.writeRates(ctx, tx, row.ID, &w)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.ratesWritten(ctx, w.changesModelRates())
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
	w := in.rates()
	kind, _, err := segmentKind(ctx, s.db.Gorm, id, "")
	if err == nil {
		err = s.check(ctx, &in, &w, kind, false)
	}
	if err == nil {
		err = s.tx(ctx, func(tx *gorm.DB) error {
			if err := updateSegment(ctx, tx, id, &in); err != nil {
				return err
			}
			return s.writeRates(ctx, tx, id, &w)
		})
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.ratesWritten(ctx, w.changesModelRates())
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
