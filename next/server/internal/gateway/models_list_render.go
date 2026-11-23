package gateway

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// What the model list says of a model that the core does not know: the
// protocols require these fields, and a model's owner, release time or
// Gemini version is not recorded anywhere in the core.
const (
	modelOwner         = "sub2api"
	modelCreatedAt     = "1970-01-01T00:00:00Z" // Anthropic's created_at of Unix time 0
	geminiModelVersion = "001"
)

type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type anthropicModel struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type geminiModel struct {
	Name                       string   `json:"name"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

// modelView renders models in one protocol's format.
type modelView struct {
	format  string
	methods geminiMethods
}

// item is one model, as the protocol's GET of a model answers it.
func (v modelView) item(id string) any {
	switch v.format {
	case FormatGemini:
		return geminiModel{Name: geminiModelPrefix + id, Version: geminiModelVersion, DisplayName: id,
			SupportedGenerationMethods: v.methods.of(id)}
	case FormatAnthropic:
		return anthropicModel{Type: "model", ID: id, DisplayName: id, CreatedAt: modelCreatedAt}
	}
	return openaiModel{ID: id, Object: "model", OwnedBy: modelOwner}
}

// list is the protocol's model list of ids, in one page: the paging
// parameters are ignored, so there is never a next page.
func (v modelView) list(ids []string) gin.H {
	items := make([]any, len(ids))
	for i, id := range ids {
		items[i] = v.item(id)
	}
	switch v.format {
	case FormatGemini:
		return gin.H{"models": items}
	case FormatAnthropic:
		body := gin.H{"data": items, "has_more": false, "first_id": nil, "last_id": nil}
		if len(ids) > 0 {
			body["first_id"], body["last_id"] = ids[0], ids[len(ids)-1]
		}
		return body
	}
	return gin.H{"object": "list", "data": items}
}

// geminiMethods are the generation methods the gateway serves on one Gemini
// API version, split into the embedding methods and the others. They come
// from the endpoints of the generation, so a model lists what the gateway
// really answers.
type geminiMethods struct{ embed, generate []string }

// of returns the methods of model id: an embedding model's are the
// embedding methods, any other model's the rest.
func (m geminiMethods) of(id string) []string {
	if strings.Contains(strings.ToLower(id), "embedding") {
		return m.embed
	}
	return m.generate
}

// geminiMethods collects the methods of the endpoints
// POST /<version>/models/:model:<method>, each once and sorted.
func (t *routeTable) geminiMethods(version string) geminiMethods {
	m := geminiMethods{embed: []string{}, generate: []string{}}
	if t == nil {
		return m
	}
	for i := range t.routes {
		name, ok := t.routes[i].pattern.geminiMethod(version)
		switch {
		case !ok:
		case strings.Contains(strings.ToLower(name), "embed"):
			m.embed = append(m.embed, name)
		default:
			m.generate = append(m.generate, name)
		}
	}
	slices.Sort(m.embed)
	slices.Sort(m.generate)
	return geminiMethods{embed: slices.Compact(m.embed), generate: slices.Compact(m.generate)}
}

// geminiMethod returns the method of a POST /<version>/models/:model:<method>
// pattern.
func (p *pathPattern) geminiMethod(version string) (string, bool) {
	s := p.segs
	if p.method != http.MethodPost || len(s) != 3 || s[0] != (segment{literal: version}) || s[1] != (segment{literal: "models"}) {
		return "", false
	}
	if !s[2].isParam || s[2].rest || s[2].literal == "" {
		return "", false
	}
	return strings.TrimPrefix(s[2].literal, ":"), true
}
