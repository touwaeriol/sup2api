package gateway

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// The model list of an API key: GET /v1/models and /v1beta/models, and one
// model below them. The core answers it from the models the key's group
// offers (core.GroupModelLister), without an upstream, billing or a usage
// record: every account type of the group shares one list, so a client sees
// what its key may name whichever protocol it speaks.

// Gemini API versions with a model list, the first segment of its path.
var modelListVersions = []string{"v1", "v1beta"}

// geminiModelPrefix starts a Gemini model resource name ("models/x").
const geminiModelPrefix = "models/"

// modelsRequest is a GET of the model list, or of one model.
type modelsRequest struct {
	format  string // FormatOpenAI, FormatAnthropic or FormatGemini
	version string // the path's version: the Gemini methods listed
	model   string // "" for the list
}

// serveModels answers a GET of the calling key's models, or of one of them,
// in the format of the client's protocol; errors too. It reports whether it
// answered.
func (g *Gateway) serveModels(c *gin.Context) bool {
	if g.d.Models == nil || g.d.Auth == nil {
		return false
	}
	r, ok := parseModelsRequest(c)
	if !ok {
		return false
	}
	httpapi.NoStore(c)
	if models, e := g.keyModels(c); e != nil {
		writeError(c, r.format, e)
	} else {
		g.writeModels(c, r, models)
	}
	return true
}

// writeModels writes the list of the key's models, or the one asked for.
func (g *Gateway) writeModels(c *gin.Context, r modelsRequest, models []string) {
	view := modelView{format: r.format, methods: g.table.Load().geminiMethods(r.version)}
	switch _, found := slices.BinarySearch(models, r.model); {
	case r.model == "":
		c.JSON(http.StatusOK, view.list(models))
	case found:
		c.JSON(http.StatusOK, view.item(r.model))
	default:
		msg := fmt.Sprintf("model %q is not offered to this API key", r.model)
		writeError(c, r.format, fromCore(core.ErrModelNotFound.WithMessage(msg), ""))
	}
}

// keyModels authenticates the request's API key and returns the models its
// group offers, sorted.
func (g *Gateway) keyModels(c *gin.Context) ([]string, *gwError) {
	p, e := g.authenticateKey(c.Request.Context(), c.ClientIP(), modelsKey(c))
	if e != nil {
		return nil, e
	}
	models, err := g.d.Models.OfferedModels(c.Request.Context(), p.Group)
	if err != nil {
		return nil, fromCore(core.AsError(err), "")
	}
	return models, nil
}

// modelsKey reads the API key from the headers of any protocol, or from
// Gemini's ?key= parameter: the list serves the clients of every protocol.
func modelsKey(c *gin.Context) string {
	if key := requestKey(c); key != "" {
		return key
	}
	return queryKey(c)
}

// queryKey reads Gemini's ?key= parameter.
func queryKey(c *gin.Context) string { return strings.TrimSpace(c.Query("key")) }

// parseModelsRequest reads a GET of /<version>/models[/<model>]. /v1beta is
// Gemini's alone; /v1 is shared, so its format follows the client's headers.
// A Gemini model may be named by its resource name ("models/x").
func parseModelsRequest(c *gin.Context) (modelsRequest, bool) {
	if c.Request.Method != http.MethodGet {
		return modelsRequest{}, false
	}
	version, model, ok := splitModelsPath(c.Request.URL.Path)
	if !ok {
		return modelsRequest{}, false
	}
	r := modelsRequest{format: FormatGemini, version: version, model: model}
	if version == "v1" {
		r.format = modelsFormat(c)
	}
	if r.format == FormatGemini {
		r.model = strings.TrimPrefix(model, geminiModelPrefix)
	}
	return r, model == "" || r.model != ""
}

// splitModelsPath splits /<version>/models[/<model>] into the version and
// the model, "" for the list. Model ids may hold slashes.
func splitModelsPath(path string) (version, model string, ok bool) {
	for _, v := range modelListVersions {
		base := "/" + v + "/models"
		if path == base {
			return v, "", true
		}
		if m, found := strings.CutPrefix(path, base+"/"); found && m != "" {
			return v, m, true
		}
	}
	return "", "", false
}

// modelsFormat picks the format of a /v1 model request by the headers the
// protocols' SDKs send: Anthropic's version header (with an API key or a
// bearer token), then Gemini's key header or parameter, else OpenAI.
func modelsFormat(c *gin.Context) string {
	switch {
	case c.GetHeader("anthropic-version") != "":
		return FormatAnthropic
	case c.GetHeader("x-goog-api-key") != "" || queryKey(c) != "":
		return FormatGemini
	}
	return FormatOpenAI
}
