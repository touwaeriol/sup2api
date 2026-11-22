// Package httpapi is the console HTTP framework: response envelope, error
// rendering, authentication and permission middleware. Modules register their
// routes through Router; they never touch gin.Engine directly.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Page is the pagination block of list responses.
type Page struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// OK renders {"data": data} with 200.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data})
}

// Created renders {"data": data} with 201.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"data": data})
}

// Accepted renders {"data": data} with 202: the work runs in the background.
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, gin.H{"data": data})
}

// List renders {"data": items, "page": {...}}.
func List(c *gin.Context, items any, p Page) {
	c.JSON(http.StatusOK, gin.H{"data": items, "page": p})
}

// NoContent renders 204.
func NoContent(c *gin.Context) { c.Status(http.StatusNoContent) }

// NoStore marks a response as uncacheable. Console state and runtime answers
// are always read fresh; a cached copy would be a stale capability claim.
func NoStore(c *gin.Context) { c.Header("Cache-Control", "no-store") }

// Fail renders {"error": {...}} using core.Error; unknown errors become
// "internal" and are logged with their cause.
func Fail(c *gin.Context, err error) {
	e := core.AsError(err)
	if e.Status >= 500 {
		slog.ErrorContext(c.Request.Context(), "request failed",
			"request_id", core.RequestID(c.Request.Context()), "path", c.FullPath(), "err", err)
	}
	c.AbortWithStatusJSON(e.Status, gin.H{"error": e})
}

// Pagination reads ?page=&page_size= (defaults 1/20, max 200).
func Pagination(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return PageBounds(page, size)
}

// PageBounds keeps a page and its size within Pagination's bounds: page 1
// to 1,000,000, size 1 to 200 with 20 for none. A query paged with them
// never takes a negative LIMIT or OFFSET, which GORM would leave out (every
// row).
func PageBounds(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	// Prevent negative OFFSET: cap page to prevent overflow
	if page > 1000000 {
		page = 1000000
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

// BindJSON decodes the body or renders invalid_argument.
func BindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		Fail(c, core.ErrInvalidArgument.WithMessage(err.Error()))
		return false
	}
	return true
}

// BindStrictJSON is BindJSON that also rejects fields v does not have, for a
// body whose old shape must not be read as another meaning.
func BindStrictJSON(c *gin.Context, v any) bool {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		Fail(c, core.ErrInvalidArgument.WithMessage(err.Error()))
		return false
	}
	return true
}

// PathID parses a positive int64 path parameter or renders invalid_argument.
func PathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		Fail(c, core.ErrInvalidArgument.WithMessage("invalid "+name))
		return 0, false
	}
	return id, true
}
