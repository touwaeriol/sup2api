package billing

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// PreviewRequest is the body of POST /prices/preview.
type PreviewRequest struct {
	PriceID    *int64            `json:"price_id"`
	Mode       string            `json:"mode"`
	Config     json.RawMessage   `json:"config"`
	Expression string            `json:"expression"`
	Usage      PreviewUsage      `json:"usage"`
	Metrics    map[string]any    `json:"metrics"`
	Headers    map[string]string `json:"headers"`
	Params     map[string]any    `json:"params"`
	At         *time.Time        `json:"at"`
	GroupID    *int64            `json:"group_id"`
}

// PreviewUsage holds token counts in the exclusive form (p excludes cache
// and image input, c image output). They are normalized like settlement
// (expr.Normalize): cache categories the expression does not price are not
// billed, image tokens it does not price are billed as p and c. len defaults
// to p + cr + cc + cc1h + img.
type PreviewUsage struct {
	P    float64  `json:"p"`
	C    float64  `json:"c"`
	CR   float64  `json:"cr"`
	CC   float64  `json:"cc"`
	CC1h float64  `json:"cc1h"`
	Img  float64  `json:"img"`
	ImgO float64  `json:"img_o"`
	Len  *float64 `json:"len"`
}

// PreviewResult is the response of POST /prices/preview.
type PreviewResult struct {
	Cost           decimal.Decimal   `json:"cost"`      // after the group multiplier
	BaseCost       decimal.Decimal   `json:"base_cost"` // before the group multiplier
	RateMultiplier decimal.Decimal   `json:"rate_multiplier"`
	Tier           string            `json:"tier"`
	Rules          []expr.RuleResult `json:"rules"`
	Breakdown      expr.Breakdown    `json:"breakdown"`
	Expression     string            `json:"expression"`
	ExprHash       string            `json:"expr_hash"`
}

func (s *Service) previewPrice(c *gin.Context) {
	ctx := c.Request.Context()
	var in PreviewRequest
	if !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := s.previewSource(ctx, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	prog, err := expr.CompileCached(src)
	if err != nil {
		httpapi.Fail(c, previewError("invalid price expression", src, err))
		return
	}
	res, err := prog.Eval(previewInput(in, prog))
	if err != nil {
		httpapi.Fail(c, previewError(err.Error(), src, err))
		return
	}
	mult, err := s.previewRate(ctx, in.GroupID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, &PreviewResult{Cost: FinalCost(res.Cost, mult), BaseCost: res.Cost, RateMultiplier: mult, Tier: res.Tier,
		Rules: res.Rules, Breakdown: res.Breakdown, Expression: prog.Expression(), ExprHash: prog.Hash()})
}

// previewSource is the expression a preview prices: the stored price's, or
// the one the price in the body stands for.
func (s *Service) previewSource(ctx context.Context, in PreviewRequest) (string, error) {
	if in.PriceID != nil {
		p, err := s.getPrice(ctx, s.db.Gorm, *in.PriceID, false)
		if err != nil {
			return "", err
		}
		return p.Expression, nil
	}
	src, err := expressionFor(cmp.Or(in.Mode, expr.ModeExpression), in.Config, strings.TrimSpace(in.Expression))
	if err != nil {
		return "", core.ErrInvalidArgument.WithMessage(err.Error())
	}
	return src, nil
}

// previewError is the invalid_argument of an expression that does not
// compile or evaluate, with its issue when there is one.
func previewError(message, src string, err error) error {
	details := map[string]any{"expression": src}
	switch e := err.(type) {
	case *expr.CompileError:
		details["errors"] = []expr.Issue{e.Issue}
	case *expr.EvalError:
		details["errors"] = []expr.Issue{e.Issue}
	}
	return core.ErrInvalidArgument.WithMessage(message).WithDetails(details)
}

// previewInput is the usage and request of a preview, normalized like
// settlement for what prog prices.
func previewInput(in PreviewRequest, prog *expr.Program) expr.Input {
	u := in.Usage
	vars := expr.Normalize(expr.SemanticsExclusive, expr.Tokens{
		Input: int64(u.P + u.Img), Output: int64(u.C + u.ImgO), CacheRead: int64(u.CR), CacheCreation: int64(u.CC), CacheCreation1h: int64(u.CC1h),
		ImageInput: int64(u.Img), ImageOutput: int64(u.ImgO),
	}, prog.Uses)
	if u.Len != nil {
		vars.Len = *u.Len
	}
	input := expr.Input{Vars: vars, Metrics: in.Metrics, Headers: map[string]string{}, Params: map[string]string{}}
	for k, v := range in.Headers {
		input.Headers[strings.ToLower(k)] = v
	}
	for k, v := range in.Params {
		b, _ := json.Marshal(v)
		input.Params[k] = string(b)
	}
	if in.At != nil {
		input.At = *in.At
	}
	return input
}

// previewRate is the rate of the group a preview asks for, 1 without one.
func (s *Service) previewRate(ctx context.Context, groupID *int64) (decimal.Decimal, error) {
	mult := decimal.NewFromInt(1)
	if groupID == nil {
		return mult, nil
	}
	var g model.Group
	err := store.G(ctx, s.db.Gorm).Select("rate_multiplier").Where("id = ?", *groupID).Take(&g).Error
	if store.IsNoRows(err) {
		return mult, core.ErrNotFound.WithMessage("group not found")
	}
	if err != nil {
		return mult, err
	}
	return core.ValueOr(g.RateMultiplier, mult), nil // NOT NULL: always read
}
