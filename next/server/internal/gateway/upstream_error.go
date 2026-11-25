package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Keep only the classification prefix in memory. Larger HTTP error responses
// are spooled privately and removed when the call finishes, including retries.
type upstreamErrorBody struct {
	prefix []byte
	file   *os.File
}

func (c *call) readUpstreamError(r io.Reader) (*upstreamErrorBody, error) {
	prefix, err := io.ReadAll(io.LimitReader(r, maxErrorBody+1))
	if err != nil {
		return nil, err
	}
	b := &upstreamErrorBody{prefix: prefix}
	if len(prefix) <= maxErrorBody {
		return b, nil
	}
	f, err := os.CreateTemp("", "sub2api-http-error-*")
	if err != nil {
		return nil, err
	}
	c.errorBodies = append(c.errorBodies, f)
	if _, err = io.Copy(f, io.MultiReader(bytes.NewReader(prefix), r)); err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	b.file, b.prefix = f, prefix[:maxErrorBody]
	return b, nil
}

func (c *call) closeErrorBodies() {
	for _, f := range c.errorBodies {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	c.errorBodies = nil
}

// Classification controls retries/account state, never the upstream HTTP body.
// The reply keeps the upstream status unless the plugin named another
// (replyStatus). The log message is independent of rendering; parsing it
// cannot rewrite bytes.
func (c *call) preserveHTTPError(e *gwError, status int, h http.Header, body *upstreamErrorBody) {
	if e == nil || body == nil {
		return
	}
	e.Status, e.RawResponse = replyStatus(e.PluginStatus, status), true
	e.Raw, e.RawFile = body.prefix, body.file
	e.ContentType, e.Headers = h.Get("Content-Type"), core.ProviderHeaders(h)
	e.Message = upstreamErrorMessage(body.prefix)
	if c.rec != nil {
		c.rec.ErrorMessage = truncateUTF8(e.Message, 1000)
	}
}

func upstreamErrorMessage(raw []byte) (message string) {
	// PostgreSQL text rejects NUL/invalid UTF-8. Escape only the diagnostic
	// representation; the HTTP response still uses the untouched raw body.
	defer func() {
		message = strings.ToValidUTF8(strings.ReplaceAll(message, "\x00", `\x00`), "\uFFFD")
	}()
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if s, ok := v.(string); ok {
			return s
		}
		if obj, ok := v.(map[string]any); ok {
			if detail, ok := obj["error"].(map[string]any); ok {
				if s, ok := detail["message"].(string); ok {
					return s
				}
			}
			if s, ok := obj["message"].(string); ok {
				return s
			}
			if s, ok := obj["error"].(string); ok {
				return s
			}
		}
	}
	return string(raw)
}
