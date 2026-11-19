package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func taskPlatform() manifest.Platform {
	p := validPlatform()
	p.Endpoints[0].Task = &manifest.AsyncTaskEndpoint{Action: "submit", Kind: "video", IDPaths: []string{"id", "data.id"}}
	query := p.Endpoints[0]
	query.ID, query.Method, query.Path, query.Protocol, query.Billing = "query", "GET", "/video/v1/generations/:id", "video.query", "free"
	query.BillingTypes = nil
	query.Request = manifest.EndpointRequest{}
	query.Task = &manifest.AsyncTaskEndpoint{Action: "query", Kind: "video", IDParam: "id", IDPaths: []string{"id"}}
	p.Endpoints = append(p.Endpoints, query)
	return p
}

func TestAsyncTaskManifestSafety(t *testing.T) {
	if got := platformCodes(taskPlatform()); len(got) != 0 {
		t.Fatalf("valid pair rejected: %v", got)
	}
	tests := []struct {
		name        string
		mut         func(*manifest.Platform)
		field, code string
	}{
		{"unpaired", func(p *manifest.Platform) { p.Endpoints = p.Endpoints[:1] }, "task.video", "unpaired"},
		{"spoofed model", func(p *manifest.Platform) { p.Endpoints[1].Request.ModelSource = "plugin" }, "endpoints[1].request", "conflict"},
		{"stream", func(p *manifest.Platform) { p.Endpoints[0].Request.StreamPath = "stream" }, "endpoints[0].task", "unsupported"},
		{"missing query param", func(p *manifest.Platform) { p.Endpoints[1].Task.IDParam = "other" }, "endpoints[1].task.idParam", "unknown_param"},
		{"paid query", func(p *manifest.Platform) { p.Endpoints[1].Billing = "usage" }, "endpoints[1].task", "conflict"},
		{"ambiguous JSON path", func(p *manifest.Platform) { p.Endpoints[0].Task.IDPaths = []string{"data.#.id"} }, "endpoints[0].task.idPaths[0]", "invalid_path"},
		{"free plugin usage", func(p *manifest.Platform) { p.Endpoints[0].Billing, p.Endpoints[0].UsageSource = "free", "plugin" }, "endpoints[0].billing", "conflict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := taskPlatform()
			tc.mut(&p)
			if got := platformCodes(p); got[tc.field] != tc.code {
				t.Fatalf("want %s=%s, got %v", tc.field, tc.code, got)
			}
		})
	}
}

func TestFreeAsyncTasksCanDescribeSubmissionWithoutReservation(t *testing.T) {
	p := taskPlatform()
	p.Endpoints[0].Billing = "free"
	p.Endpoints[0].BillingTypes = nil
	p.Endpoints[0].UsageRequestFields = []string{"resolution"}
	if got := platformCodes(p); len(got) != 0 {
		t.Fatalf("free task should not require plugin usage source or a reservation: %v", got)
	}
	m := pluginPlatformManifest(p.Endpoints[0])
	m.Platforms = []manifest.Platform{p}
	if got := codes(Validate(m, nil, ValidateOptions{Tooling: true})); got["platforms[0].endpoints[0].task"] != "missing_capability" {
		t.Fatalf("task submission must require a platform adapter: %v", got)
	}
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}, {ID: manifest.CapPlatformTasks}}
	if err := Validate(m, nil, ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
}
