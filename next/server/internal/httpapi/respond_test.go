package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// PageBounds is Pagination's clamp: page 1 to 1,000,000, size 1 to 200 with
// 20 for none, so a paged query never gets a negative LIMIT or OFFSET.
func TestPageBounds(t *testing.T) {
	for _, c := range []struct{ page, size, wantPage, wantSize int }{
		{1, 20, 1, 20},
		{3, 50, 3, 50},
		{0, 0, 1, 20},
		{-4, -1, 1, 20},
		{2_000_000, 500, 1_000_000, 200},
	} {
		if p, s := PageBounds(c.page, c.size); p != c.wantPage || s != c.wantSize {
			t.Errorf("PageBounds(%d, %d) = %d, %d; want %d, %d", c.page, c.size, p, s, c.wantPage, c.wantSize)
		}
	}
}

// Every 429 that says how long to wait sends it as Retry-After too.
func TestFailRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct {
		err  error
		want string
	}{
		{core.RateLimitedFor("slow down", 1500*time.Millisecond), "2"},
		{core.ErrRateLimited, ""},
		{core.ErrConflict.WithDetails(map[string]any{core.RetryAfterSeconds: int64(5)}), ""},
	} {
		w := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w)
		c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		Fail(c2, c.err)
		if got := w.Header().Get("Retry-After"); got != c.want {
			t.Errorf("%v: Retry-After = %q, want %q", c.err, got, c.want)
		}
	}
}
