package group

import (
	"fmt"
	"testing"
)

func TestGroupEnvironment(t *testing.T) {
	e := setup(t)
	code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "Env",
		"environment": map[string]int64{"relay_timeout_seconds": 90, "stream_idle": 0}})
	if code != 400 {
		t.Fatalf("unknown key: %d %v", code, out)
	}
	// The request body limit is a gateway setting, not a group variable.
	if code, out = e.do(e.admin, "POST", "/groups", map[string]any{"name": "Env",
		"environment": map[string]int64{"max_request_body_mb": 64}}); code != 400 {
		t.Fatalf("removed variable: %d %v", code, out)
	}
	code, out = e.do(e.admin, "POST", "/groups", map[string]any{"name": "Env",
		"environment": map[string]int64{"relay_timeout_seconds": 90, "task_timeout_minutes": 0}})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	g := out["data"].(map[string]any)
	if fmt.Sprint(g["environment"]) != "map[relay_timeout_seconds:90]" {
		t.Fatalf("zero not dropped: %v", g["environment"])
	}
	path := fmt.Sprintf("/groups/%d", int64(g["id"].(float64)))

	// Absent leaves it; a range error is a field error.
	_, out = e.do(e.admin, "PATCH", path, map[string]any{"name": "Env2"})
	if fmt.Sprint(out["data"].(map[string]any)["environment"]) != "map[relay_timeout_seconds:90]" {
		t.Fatalf("absent changed it: %v", out)
	}
	if code, out = e.do(e.admin, "PATCH", path, map[string]any{"environment": map[string]int64{"relay_timeout_seconds": 999999}}); code != 400 {
		t.Fatalf("range: %d %v", code, out)
	}
	code, out = e.do(e.admin, "PATCH", path, map[string]any{"environment": map[string]int64{"task_poll_max_failures": 5}})
	if code != 200 || fmt.Sprint(out["data"].(map[string]any)["environment"]) != "map[task_poll_max_failures:5]" {
		t.Fatalf("replace: %d %v", code, out)
	}
	_, out = e.do(e.admin, "PATCH", path, map[string]any{"environment": map[string]int64{}})
	if fmt.Sprint(out["data"].(map[string]any)["environment"]) != "map[]" {
		t.Fatalf("clear: %v", out)
	}
}

// conversion_loss_policy (CONTRACTS §88): allow, safe, strict or null
// (accounts decide); absent keeps it, an unknown value is a field error.
func TestGroupConversionLossPolicy(t *testing.T) {
	e := setup(t)
	if code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "Loss", "conversion_loss_policy": "lax"}); code != 400 ||
		fmt.Sprint(out["error"].(map[string]any)["details"]) != "map[fields:[map[code:invalid field:conversion_loss_policy message:conversion loss policy must be allow, safe, strict or null]]]" {
		t.Fatalf("invalid: %d %v", code, out)
	}
	code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "Loss", "conversion_loss_policy": "safe"})
	if code != 201 || out["data"].(map[string]any)["conversion_loss_policy"] != "safe" {
		t.Fatalf("create: %d %v", code, out)
	}
	path := fmt.Sprintf("/groups/%d", int64(out["data"].(map[string]any)["id"].(float64)))
	if _, out = e.do(e.admin, "PATCH", path, map[string]any{"name": "Loss2"}); out["data"].(map[string]any)["conversion_loss_policy"] != "safe" {
		t.Fatalf("absent changed it: %v", out)
	}
	if _, out = e.do(e.admin, "PATCH", path, map[string]any{"conversion_loss_policy": nil}); out["data"].(map[string]any)["conversion_loss_policy"] != nil {
		t.Fatalf("null did not clear: %v", out)
	}
	if _, out = e.do(e.admin, "PATCH", path, map[string]any{"conversion_loss_policy": "strict"}); out["data"].(map[string]any)["conversion_loss_policy"] != "strict" {
		t.Fatalf("set: %v", out)
	}
}
