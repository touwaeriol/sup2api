package redeem

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// RegisterRoutes mounts POST /me/redeem (redeem:use) and the management
// routes /redeem-codes (redeem:manage).
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Perm(http.MethodPost, "/me/redeem", perm.RedeemUse, s.redeemHandler)
	r.Perm(http.MethodGet, "/redeem-codes", perm.RedeemManage, s.listHandler)
	r.Perm(http.MethodPost, "/redeem-codes", perm.RedeemManage, s.generateHandler)
	r.Perm(http.MethodDelete, "/redeem-codes/:id", perm.RedeemManage, s.voidHandler)
}

// POST /me/redeem {code} → {amount, balance}.
func (s *Service) redeemHandler(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	out, err := s.Redeem(ctx, uid, in.Code)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// GET /redeem-codes?status=&page=&page_size=.
func (s *Service) listHandler(c *gin.Context) {
	page, size := httpapi.Pagination(c)
	items, total, err := s.List(c.Request.Context(), c.Query("status"), page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// POST /redeem-codes {count, value, expires_at?, notes?} → {codes}: the only
// response that ever holds plain codes, so it is never cached.
func (s *Service) generateHandler(c *gin.Context) {
	var in GenerateRequest
	if !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := audit.Context(c)
	uid, _ := core.UserID(ctx)
	out, err := s.Generate(ctx, uid, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoStore(c)
	httpapi.Created(c, out)
}

// DELETE /redeem-codes/:id voids an unused code.
func (s *Service) voidHandler(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := audit.Context(c)
	uid, _ := core.UserID(ctx)
	if err := s.Void(ctx, uid, id); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}
