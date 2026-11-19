package gateway

import (
	"strings"
	"testing"
)

func TestVideoPriceRejectsTextEndpoint(t *testing.T) {
	e := newEnv(t)
	e.pricer.rules[testModel].VideoOnly = true
	e.balance.broke = map[int64]bool{testUser: true}
	r := e.do("/v1/messages", body(testModel, false), nil)
	if r.status != 400 || r.json().Get("error.code").String() != "billing_type_not_supported" {
		t.Fatalf("response %d %s", r.status, r.body)
	}
	if len(e.accounts.lastTypes) != 0 || e.plat.buildCount() != 0 || len(e.up.keys()) != 0 {
		t.Fatal("billing mismatch reached scheduling or upstream")
	}
	if rec := e.record(); rec.Billable || rec.Price != nil || rec.Reservation != nil || rec.AccountID != nil {
		t.Fatalf("rejected request reached billing: %+v", rec)
	}
}

func TestVideoContentTypesSurviveLargeInput(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"` + strings.Repeat("x", 4<<10+1) + `"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", 4<<10+1) + `"}},{"type":"video_url","video_url":{"url":"asset://video"}}]}`)
	fields := usageRequestFields([]string{"content.#.type", "content.0.text"}, body)
	if fields["content.#.type"] != `["text","image_url","video_url"]` {
		t.Fatalf("types: %v", fields)
	}
	if len(fields["content.0.text"]) != 4<<10+3 {
		t.Fatalf("large text: %d bytes", len(fields["content.0.text"]))
	}
}
