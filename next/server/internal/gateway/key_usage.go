package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// keyUsagePath is reserved for the core (manifest.CoreGatewayPrefixes).
const keyUsagePath = "/v1/usage"

// serveKeyUsage answers GET /v1/usage with the report of the calling API key
// (CONTRACTS §62); errors are in the Anthropic format.
func (g *Gateway) serveKeyUsage(c *gin.Context) bool {
	path := c.Request.URL.Path
	if path != keyUsagePath && !strings.HasPrefix(path, keyUsagePath+"/") {
		return false
	}
	httpapi.NoStore(c)
	switch {
	case g.d.KeyUsage == nil || g.d.Auth == nil || path != keyUsagePath:
		writeError(c, FormatAnthropic, fromCore(core.ErrNotFound, ""))
	case c.Request.Method != http.MethodGet:
		writeError(c, FormatAnthropic, &gwError{Status: http.StatusMethodNotAllowed, Code: core.ErrInvalidArgument.Code, Message: "use GET"})
	default:
		g.reportKeyUsage(c)
	}
	return true
}

func (g *Gateway) reportKeyUsage(c *gin.Context) {
	ctx := c.Request.Context()
	p, e := g.authenticateKey(ctx, c.ClientIP(), requestKey(c))
	if e != nil {
		writeError(c, FormatAnthropic, e)
		return
	}
	report, err := g.d.KeyUsage.KeyUsage(ctx, p)
	if err != nil {
		writeError(c, FormatAnthropic, fromCore(core.AsError(err), ""))
		return
	}
	c.JSON(http.StatusOK, report)
}

// authenticateKey authenticates the API key a gateway request carries, sent
// from clientIP: the pipeline's, the model list's and /v1/usage's one check.
func (g *Gateway) authenticateKey(ctx context.Context, clientIP, key string) (*core.APIKeyPrincipal, *gwError) {
	if key == "" {
		return nil, fromCore(core.ErrUnauthenticated.WithMessage("missing api key"), "")
	}
	p, err := g.d.Auth.Authenticate(core.WithClientIP(ctx, clientIP), key)
	if err != nil {
		return nil, fromCore(core.AsError(err), "")
	}
	return p, nil
}

// requestKey reads an API key from x-api-key, Authorization: Bearer or
// x-goog-api-key, the headers the gateway's clients send it in.
func requestKey(c *gin.Context) string {
	if key := strings.TrimSpace(c.GetHeader("x-api-key")); key != "" {
		return key
	}
	if v := strings.TrimSpace(c.GetHeader("Authorization")); len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return strings.TrimSpace(c.GetHeader("x-goog-api-key"))
}
