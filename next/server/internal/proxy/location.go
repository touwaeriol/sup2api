package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

type Location struct {
	IP          string `json:"ip"`
	CountryCode string `json:"country_code"`
	Timezone    string `json:"timezone"`
}

func (s *Service) location(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, err := s.load(ctx, id, s.scopeOf(ctx, perm.ProxyRead)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	pc, err := s.HTTPClient(ctx, &id)
	if err != nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage(t(ctx, "Proxy location is unavailable", "无法获取代理地区信息")))
		return
	}
	location, err := s.lookupLocation(ctx, pc.Client)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, location)
}

func (s *Service) lookupLocation(ctx context.Context, client *http.Client) (Location, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opts.ProbeTimeout)
	defer cancel()
	failed := core.ErrUnavailable.WithMessage(t(ctx, "Proxy location lookup failed; select locale and timezone manually", "代理地区查询失败，请手动选择语言和时区"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/?fields=success,ip,country_code,timezone.id", nil)
	if err != nil {
		return Location{}, failed
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return Location{}, failed
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Location{}, failed
	}
	var result struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		Timezone    struct {
			ID string `json:"id"`
		} `json:"timezone"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil || !result.Success || net.ParseIP(result.IP) == nil {
		return Location{}, failed
	}
	if len(result.CountryCode) != 2 || result.CountryCode[0] < 'A' || result.CountryCode[0] > 'Z' || result.CountryCode[1] < 'A' || result.CountryCode[1] > 'Z' || result.Timezone.ID == "" || result.Timezone.ID == "Local" {
		return Location{}, failed
	}
	if _, err := time.LoadLocation(result.Timezone.ID); err != nil {
		return Location{}, failed
	}
	return Location{IP: result.IP, CountryCode: result.CountryCode, Timezone: result.Timezone.ID}, nil
}
