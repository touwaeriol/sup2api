package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/mailer/internal/mailer"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestValidateSettings: every value the settings JSON Schema refused (one
// counterexample per constraint, written out here) is still refused, at its
// field, by ValidateSettings and Configure alike; the plugin's own checks
// (from address) too; the default set and real settings pass.
func TestValidateSettings(t *testing.T) {
	h := pluginsdktest.Start(t, mailer.New(), pluginsdktest.Options{Manifest: manifestJSON})
	refused := []struct {
		settings string
		field    string
		code     string
	}{
		{`{"host":` + strconv.Quote(strings.Repeat("h", 254)) + `}`, "host", "maxLength"},
		{`{"host":1}`, "host", "type"},
		{`{"port":0}`, "port", "minimum"},
		{`{"port":65536}`, "port", "maximum"},
		{`{"port":587.5}`, "port", "type"},
		{`{"port":"587"}`, "port", "type"},
		{`{"security":"ssl"}`, "security", "enum"},
		{`{"username":` + strconv.Quote(strings.Repeat("u", 321)) + `}`, "username", "maxLength"},
		{`{"password":` + strconv.Quote(strings.Repeat("p", 1001)) + `}`, "password", "maxLength"},
		{`{"password":null}`, "password", "type"},
		{`{"from_address":` + strconv.Quote(strings.Repeat("f", 321)) + `}`, "from_address", "maxLength"},
		{`{"from_name":` + strconv.Quote(strings.Repeat("名", 101)) + `}`, "from_name", "maxLength"},
		{`{"timeout_seconds":4}`, "timeout_seconds", "minimum"},
		{`{"timeout_seconds":26}`, "timeout_seconds", "maximum"},
		{`{"timeout_seconds":true}`, "timeout_seconds", "type"},
		{`{"smtp_host":"smtp.example.com"}`, "smtp_host", "unknown"},
		{`[]`, "", "invalid_json"},
		{`{"host":"smtp.example.com","from_address":"Ops <ops@example.com>"}`, "from_address", "invalid"},
		{`{"host":"smtp.example.com"}`, "from_address", "invalid"},
	}
	for _, c := range refused {
		resp, err := h.ValidateSettings(json.RawMessage(c.settings), nil)
		if err != nil {
			t.Fatalf("%s: %v", c.settings, err)
		}
		if errs := resp.GetErrors(); len(errs) != 1 || errs[0].GetField() != c.field || errs[0].GetCode() != c.code {
			t.Errorf("%s: errors = %v, want %s %s", c.settings, errs, c.field, c.code)
		}
		if _, ok := pluginsdk.AsFieldErrors(mailer.New().Configure(context.Background(), pluginsdk.Config{JSON: json.RawMessage(c.settings)})); !ok {
			t.Errorf("%s: Configure accepted it", c.settings)
		}
	}
	for _, ok := range []string{
		`{}`,
		// The set the console form saves untouched (its defaults).
		`{"host":"","port":587,"security":"starttls","username":"","password":"","from_address":"","from_name":"Sub2API","timeout_seconds":20}`,
		`{"host":"smtp.example.com","port":465,"security":"tls","username":"ops","password":"secret","from_address":"noreply@example.com","from_name":"运维","timeout_seconds":5}`,
		`{"host":"127.0.0.1","port":25,"security":"none","from_address":"noreply@example.com","timeout_seconds":25}`,
		`{"host":` + strconv.Quote(strings.Repeat("h", 253)) + `,"from_address":"a@b.co","from_name":` + strconv.Quote(strings.Repeat("名", 100)) + `}`,
	} {
		resp, err := h.ValidateSettings(json.RawMessage(ok), nil)
		if err != nil || len(resp.GetErrors()) != 0 || resp.GetNormalizedConfigJson() != "" {
			t.Errorf("%s: %v %v", ok, resp.GetErrors(), err)
		}
	}
}
