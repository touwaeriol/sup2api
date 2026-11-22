package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// syncPrice is one price offered by a source.
type syncPrice struct {
	Model      string          `json:"model"`
	Mode       string          `json:"mode"`
	Config     json.RawMessage `json:"config"`
	Expression string          `json:"expression"`
	hash       string
	version    int
}

// fetched is the parsed content of a source.
type fetched struct {
	prices  map[string]*syncPrice
	skipped int
}

func (s *Service) fetchSource(ctx context.Context, src *PriceSource) (*fetched, error) {
	req, err := s.sourceRequest(ctx, src)
	if err != nil {
		return nil, err
	}
	body, err := s.download(req)
	if err != nil {
		return nil, err
	}
	out, err := parseSource(src, body)
	if err != nil {
		return nil, err
	}
	for m, p := range out.prices {
		if err := p.compile(); err != nil {
			delete(out.prices, m)
			out.skipped++
		}
	}
	return out, nil
}

// sourceRequest is the GET of a source: its URL, or the GET /key/prices of
// an upstream sup2api with the source's API key.
func (s *Service) sourceRequest(ctx context.Context, src *PriceSource) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sub2api-next price sync")
	if src.Kind != KindSup2API {
		return req, nil
	}
	if req.URL, err = url.Parse(strings.TrimRight(src.URL, "/") + "/api/v1/key/prices"); err != nil {
		return nil, err
	}
	if len(src.apiKeyEnc) == 0 || s.sync.Cipher == nil {
		return nil, errors.New("no API key for the upstream sup2api")
	}
	key, err := s.sync.Cipher.Decrypt(src.apiKeyEnc, []byte(sourceAPIKeyAAD))
	if err != nil {
		return nil, fmt.Errorf("decrypt API key: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	return req, nil
}

// download reads the body of an HTTP 200 answer of at most maxSourceBytes.
func (s *Service) download(req *http.Request) ([]byte, error) {
	client := s.sync.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSourceBytes {
		return nil, fmt.Errorf("response larger than %d MiB", maxSourceBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", req.URL.Redacted(), resp.StatusCode)
	}
	return body, nil
}

// parseSource parses what a source answered by its kind and options.
func parseSource(src *PriceSource, body []byte) (*fetched, error) {
	var opts sourceOptions
	_ = json.Unmarshal(src.Options, &opts)
	providers := opts.Providers
	switch src.Kind {
	case KindLiteLLM:
		if len(providers) == 0 {
			providers = defaultLiteLLMProviders
		}
		return parseLiteLLM(body, providers)
	case KindModelsDev:
		if len(providers) == 0 {
			providers = defaultModelsDevProviders
		}
		return parseModelsDev(body, providers)
	case KindSup2API:
		return parseSup2API(body, opts.ApplyMultiplier == nil || *opts.ApplyMultiplier)
	}
	return nil, fmt.Errorf("unknown source kind %q", src.Kind)
}

func truncate(s string, n int) string { return x.TruncateEllipsis(s, n) }

// compile generates (when needed) and compiles the expression.
func (p *syncPrice) compile() error {
	if p.Expression == "" {
		src, err := expressionFor(p.Mode, p.Config, "")
		if err != nil {
			return err
		}
		p.Expression = src
	}
	prog, err := expr.CompileCached(p.Expression)
	if err != nil {
		return err
	}
	p.hash, p.version = prog.Hash(), prog.Version()
	p.Expression = prog.Expression()
	return nil
}
