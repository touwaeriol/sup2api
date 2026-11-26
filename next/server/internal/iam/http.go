package iam

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
)

// RegisterRoutes mounts /auth/*, /me, /me/password, /users*, the members of
// a segment, which are a users list, and the super admin transfer.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Public(http.MethodPost, "/auth/login", s.handleLogin)
	r.SessionOnly().Handle(http.MethodPost, "/auth/logout", s.handleLogout)
	r.AuthedRead("/me", s.handleMe)
	r.Self(http.MethodPut, "/me", "self:profile", s.handleUpdateMe)
	// Step-up self.password.change in the handler, after the new password
	// passes validation, so a rejected one does not spend the proof.
	r.Self(http.MethodPut, "/me/password", selfSecurity, s.handleChangePassword)

	// Every user or the team (CONTRACTS §103.3): a user out of the scope is
	// not found. Deleting a user, resetting its password and the other
	// per-user routes below take the unscoped keys.
	r.PermScoped(http.MethodGet, "/users", s.handleListUsers, perm.UserRead)
	r.PermScoped(http.MethodPost, "/users", s.handleCreateUser, perm.UserCreate).Uses(perm.RoleAssign, perm.SegmentManage)
	r.PermScoped(http.MethodGet, "/users/:id", s.handleGetUser, perm.UserRead)
	r.PermScoped(http.MethodPatch, "/users/:id", s.handleUpdateUser, perm.UserUpdate).Uses(perm.UserPasswordReset)
	r.StepUp(core.StepUpUserDelete).Perm(http.MethodDelete, "/users/:id", "user:delete", s.handleDeleteUser)
	r.Perm(http.MethodPut, "/users/:id/roles", "role:assign", s.handleSetUserRoles)
	r.Perm(http.MethodGet, "/segments/:id/members", "segment:read", s.handleSegmentMembers).Uses(perm.UserRead)
	// The super admin's own standing: under self:security for an access key.
	r.StepUp(core.StepUpSuperAdminTransfer).Self(http.MethodPost, "/system/super-admin/transfer", selfSecurity, s.handleTransferSuperAdmin)
	s.registerSecurityRoutes(r)
}

func actor(c *gin.Context) int64 {
	uid, _ := core.UserID(c.Request.Context())
	return uid
}

func (s *Service) handleLogin(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	result, err := s.Login(sessionContext(c, loginMethodPassword), in.Email, in.Password, c.ClientIP())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, result)
}

func (s *Service) handleLogout(c *gin.Context) {
	if err := s.Logout(c.Request.Context(), actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

func (s *Service) handleMe(c *gin.Context) {
	me, err := s.GetMe(c.Request.Context(), actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, me)
}

func (s *Service) handleUpdateMe(c *gin.Context) {
	var in struct {
		DisplayName string `json:"display_name"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := s.UpdateProfile(audit.Context(c), actor(c), in.DisplayName); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.handleMe(c)
}

func (s *Service) handleChangePassword(c *gin.Context) {
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if f := validatePassword("new_password", in.NewPassword); f != nil {
		httpapi.Fail(c, core.InvalidFields(*f))
		return
	}
	if err := s.ConsumeStepUp(c.Request.Context(), core.StepUpSelfPassword); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := s.ChangePassword(c.Request.Context(), actor(c), s.currentSession(c), in.NewPassword); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

// redactBalance hides balances from callers without balance:all:read.
func (s *Service) redactBalance(c *gin.Context, users ...*User) error {
	ok, err := s.authz.Can(c.Request.Context(), actor(c), "balance:all:read")
	if err != nil {
		return err
	}
	if !ok {
		for _, u := range users {
			u.Balance = nil
		}
	}
	return nil
}

func (s *Service) handleListUsers(c *gin.Context) {
	f := ListUsersFilter{Query: c.Query("q"), Status: c.Query("status"), Role: c.Query("role"), Segment: c.Query("segment")}
	if v := c.Query("owner_id"); v != "" {
		owner, ok := ownerFilter(v)
		if !ok {
			httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "owner_id", Code: "invalid", Message: "must be a user id or none"}))
			return
		}
		f.Owner = &owner
	}
	if v := c.Query("direct"); v != "" {
		direct, err := strconv.ParseBool(v)
		if err != nil {
			httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "direct", Code: "invalid", Message: "must be a boolean"}))
			return
		}
		if direct {
			f.Direct = actor(c)
		}
	}
	s.listUsers(c, f)
}

// handleSegmentMembers lists the members of a segment (CONTRACTS §97): the
// users list with one more condition, so it needs user:read besides the
// route's segment:read.
func (s *Service) handleSegmentMembers(c *gin.Context) {
	if !httpapi.CanAny(c, s.authz, "user:read") {
		return
	}
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	if _, err := segment.Ref(c.Request.Context(), s.db.Gorm, id, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.listUsers(c, ListUsersFilter{Query: c.Query("q"), Segment: c.Param("id")})
}

// ownerFilter reads owner_id=<user id>|none, none as 0.
func ownerFilter(v string) (int64, bool) {
	if v == "none" {
		return 0, true
	}
	id, err := strconv.ParseInt(v, 10, 64)
	return id, err == nil && id > 0
}

// listUsers responds with the page of the users f selects within the
// caller's scope of user:read.
func (s *Service) listUsers(c *gin.Context, f ListUsersFilter) {
	ctx := c.Request.Context()
	f.Page, f.PageSize = httpapi.Pagination(c)
	scope, err := core.Scope(ctx, s.authz, perm.UserRead)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	f.Scope = scope
	users, total, err := s.ListUsers(ctx, f)
	if err == nil {
		err = s.redactBalance(c, users...)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, users, httpapi.Page{Page: f.Page, PageSize: f.PageSize, Total: total})
}

func (s *Service) handleGetUser(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	scope, err := core.Scope(ctx, s.authz, perm.UserRead)
	var u *User
	if err == nil {
		u, err = s.getUserIn(ctx, s.db.Gorm, id, scope)
	}
	if err == nil {
		err = s.redactBalance(c, u)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, u)
}

func (s *Service) handleCreateUser(c *gin.Context) {
	var in CreateUserInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := c.Request.Context()
	// Choosing roles other than the default is a role assignment: it needs
	// role:assign as well.
	if !IsDefaultRoles(in.RoleKeys) && !httpapi.CanAny(c, s.authz, "role:assign") {
		return
	}
	// Choosing the segment is a segment assignment: it needs segment:manage
	// as well.
	if in.SegmentID != nil && !httpapi.CanAny(c, s.authz, "segment:manage") {
		return
	}
	u, err := s.CreateUser(ctx, actor(c), in)
	if err == nil {
		err = s.redactBalance(c, u)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, u)
}

func (s *Service) handleUpdateUser(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in UpdateUserInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := audit.Context(c)
	scope, err := core.Scope(ctx, s.authz, perm.UserUpdate)
	var u *User
	if err == nil {
		u, err = s.UpdateUser(ctx, actor(c), id, scope, in)
	}
	if err == nil {
		err = s.redactBalance(c, u)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, u)
}

func (s *Service) handleDeleteUser(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	if err := s.DeleteUser(c.Request.Context(), actor(c), id); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

func (s *Service) handleSetUserRoles(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		RoleKeys []string `json:"role_keys"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	u, err := s.SetUserRoles(c.Request.Context(), actor(c), id, in.RoleKeys)
	if err == nil {
		err = s.redactBalance(c, u)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, u)
}
