package account

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The plugin account reader is the host side of HostService.ListAccounts and
// GetAccount. Its whole security boundary is the plugin_key
// predicate, so the tests below are about what a plugin must NOT be able to
// see or do, not about the happy path alone.

// mkRawAccount inserts an account of pluginKey/typ directly; credentials are
// encrypted with the same AAD the service uses.
func (e *env) mkRawAccount(pluginKey, typ, name, apiKey string, active bool) int64 {
	e.t.Helper()
	enc, err := e.svc.d.Cipher.Encrypt([]byte(`{"api_key":"`+apiKey+`"}`), aad(pluginKey))
	if err != nil {
		e.t.Fatal(err)
	}
	status, sched := "active", true
	if !active {
		status, sched = "disabled", false
	}
	return e.exec1(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, settings, status, schedulable)
		VALUES ($1, $2, $3, $4, '{"base_url":"https://api.example.com"}', $5, $6) RETURNING id`,
		name, pluginKey, typ, enc, status, sched)
}

// baseURL reads the settings object back (jsonb re-renders the JSON).
func baseURL(b json.RawMessage) string {
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m["base_url"]
}

func (e *env) auditRows(action string) []map[string]any {
	e.t.Helper()
	rows, err := e.db.SQL.QueryContext(context.Background(),
		`SELECT user_id, target_type, target_id, detail FROM audit_logs WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var uid *int64
		var targetType, targetID string
		var detail []byte
		if err := rows.Scan(&uid, &targetType, &targetID, &detail); err != nil {
			e.t.Fatal(err)
		}
		m := map[string]any{"target_type": targetType, "target_id": targetID}
		if uid != nil {
			m["user_id"] = *uid
		}
		d := map[string]any{}
		_ = json.Unmarshal(detail, &d)
		m["detail"] = d
		out = append(out, m)
	}
	return out
}

func TestListPluginAccountsOnlyOwnTypes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mine := e.mkRawAccount("anthropic", "apikey", "mine-1", "sk-mine-1", true)
	mine2 := e.mkRawAccount("anthropic", "apikey", "mine-2", "sk-mine-2", true)
	other := e.mkRawAccount("relay", "relay_key", "theirs", "sk-theirs", true)

	got, err := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != mine || got[1].ID != mine2 {
		t.Fatalf("accounts = %+v", got)
	}
	for _, a := range got {
		if a.ID == other {
			t.Fatal("another plugin's account listed")
		}
		if a.Type != "apikey" || a.Status != "active" || !a.Schedulable {
			t.Fatalf("summary = %+v", a)
		}
		// The summary is metadata only: nothing in it may carry a secret. The
		// stored api key must appear nowhere in the marshalled row.
		b, _ := json.Marshal(a)
		if strings.Contains(string(b), "sk-") {
			t.Fatalf("credential leaked into the summary: %s", b)
		}
		if baseURL(a.Settings) != "https://api.example.com" {
			t.Fatalf("settings = %s", a.Settings)
		}
	}

	// The other plugin sees exactly its own one.
	got, err = e.svc.ListPluginAccounts(ctx, "relay", core.PluginAccountQuery{})
	if err != nil || len(got) != 1 || got[0].ID != other {
		t.Fatalf("relay accounts = %+v (%v)", got, err)
	}

	// A plugin with no accounts at all gets an empty page, not everything.
	if got, err = e.svc.ListPluginAccounts(ctx, "video", core.PluginAccountQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("video accounts = %+v (%v)", got, err)
	}
}

func TestListPluginAccountsFilters(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a1 := e.mkRawAccount("anthropic", "apikey", "a1", "sk-1", true)
	a2 := e.mkRawAccount("anthropic", "apikey", "a2", "sk-2", true)
	off := e.mkRawAccount("anthropic", "apikey", "off", "sk-3", false)

	// Inactive accounts are hidden by default.
	got, _ := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{})
	if len(got) != 2 {
		t.Fatalf("default page = %+v", got)
	}
	got, _ = e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{IncludeInactive: true})
	if len(got) != 3 || got[2].ID != off || got[2].Status != "disabled" || got[2].Schedulable {
		t.Fatalf("with inactive = %+v", got)
	}

	// Paging is keyset on id and bounded: there is no "give me everything".
	page1, err := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{Limit: 1})
	if err != nil || len(page1) != 1 || page1[0].ID != a1 {
		t.Fatalf("page 1 = %+v (%v)", page1, err)
	}
	page2, err := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{Limit: 1, AfterID: page1[0].ID})
	if err != nil || len(page2) != 1 || page2[0].ID != a2 {
		t.Fatalf("page 2 = %+v (%v)", page2, err)
	}

	// An unknown type matches nothing rather than falling back to "all".
	if got, _ = e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{Type: "nope"}); len(got) != 0 {
		t.Fatalf("unknown type = %+v", got)
	}
	if got, _ = e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{Type: "apikey"}); len(got) != 2 {
		t.Fatalf("type filter = %+v", got)
	}

	// A plugin key of another plugin's account type never widens the page.
	if got, _ = e.svc.ListPluginAccounts(ctx, "relay", core.PluginAccountQuery{Type: "apikey"}); len(got) != 0 {
		t.Fatalf("cross plugin type = %+v", got)
	}
}

func TestReadPluginAccount(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mine := e.mkRawAccount("anthropic", "apikey", "mine", "sk-mine", true)
	other := e.mkRawAccount("relay", "relay_key", "theirs", "sk-theirs", true)

	// The account as PlatformService calls receive it: no proxy, direct.
	got, err := e.svc.ReadPluginAccount(ctx, "anthropic", mine)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetId() != mine || got.GetName() != "mine" || got.GetType() != "apikey" ||
		got.GetCredentialsJson() != `{"api_key":"sk-mine"}` || baseURL(json.RawMessage(got.GetSettingsJson())) != "https://api.example.com" ||
		got.GetProperties().GetStatus() != "active" || got.GetProxy() != nil {
		t.Fatalf("account = %v", got)
	}
	// With a proxy, the proxy it goes out through.
	host, port, _ := forwardProxy(t)
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port, username) VALUES ('p', 'http', $1, $2, 'bob') RETURNING id`, host, port)
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET proxy_id = $1 WHERE id = $2`, pid, mine); err != nil {
		t.Fatal(err)
	}
	got, err = e.svc.ReadPluginAccount(ctx, "anthropic", mine)
	if p := got.GetProxy(); err != nil || p.GetId() != pid || p.GetProtocol() != "http" || p.GetHost() != host || int(p.GetPort()) != port || p.GetUsername() != "bob" {
		t.Fatalf("proxy = %v %v", p, err)
	}
	// A proxy that cannot be had: no account without its proxy, no audit.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE proxies SET status = 'disabled', updated_at = clock_timestamp() WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	e.prx.Invalidate(pid)
	reads := len(e.auditRows(AuditPluginAccountRead))
	if _, err := e.svc.ReadPluginAccount(ctx, "anthropic", mine); core.AsError(err).Code != "unavailable" || len(e.auditRows(AuditPluginAccountRead)) != reads {
		t.Fatalf("disabled proxy read = %v", err)
	}

	// Another plugin's account is not found, NOT permission denied: a
	// distinguishable error would confirm the id exists.
	_, err = e.svc.ReadPluginAccount(ctx, "anthropic", other)
	if core.AsError(err).Code != "not_found" {
		t.Fatalf("cross-plugin read = %v", err)
	}
	// ... and the answer is the same for an id that does not exist at all.
	_, missing := e.svc.ReadPluginAccount(ctx, "anthropic", other+10_000)
	if core.AsError(missing).Code != "not_found" || core.AsError(missing).Message != core.AsError(err).Message {
		t.Fatalf("existence leak: %v vs %v", err, missing)
	}

	// A soft-deleted account is gone for the plugin too.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET deleted_at = now() WHERE id = $1`, mine); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReadPluginAccount(ctx, "anthropic", mine); core.AsError(err).Code != "not_found" {
		t.Fatalf("deleted account read = %v", err)
	}
	if got, _ := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{IncludeInactive: true}); len(got) != 0 {
		t.Fatalf("deleted account listed: %+v", got)
	}
}

// Every successful read is audited: that record is the condition the
// pull-style access is granted under, so it is checked call by call, and a
// read whose row cannot be written hands nothing out.
func TestReadPluginAccountAudits(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mine := e.mkRawAccount("anthropic", "apikey", "mine", "sk-mine", true)
	other := e.mkRawAccount("relay", "relay_key", "theirs", "sk-theirs", true)

	for i := 0; i < 3; i++ {
		if _, err := e.svc.ReadPluginAccount(ctx, "anthropic", mine); err != nil {
			t.Fatal(err)
		}
	}
	rows := e.auditRows(AuditPluginAccountRead)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want one per call", len(rows))
	}
	r := rows[0]
	if r["target_type"] != "account" || r["target_id"] != itoa(mine) {
		t.Fatalf("audit target: %v", r)
	}
	if _, hasUser := r["user_id"]; hasUser {
		// No console user is in the chain; the actor is the plugin, in detail.
		t.Fatalf("audit attributed to a user: %v", r)
	}
	d := r["detail"].(map[string]any)
	if d["plugin_key"] != "anthropic" || d["account_type"] != "apikey" || d["account_name"] != "mine" ||
		d["via"] != "host.GetAccount" || len(d) != 4 {
		t.Fatalf("audit detail: %v", d)
	}

	// A refused read discloses nothing, so it writes no disclosure record.
	if _, err := e.svc.ReadPluginAccount(ctx, "anthropic", other); err == nil {
		t.Fatal("cross-plugin read succeeded")
	}
	if rows := e.auditRows(AuditPluginAccountRead); len(rows) != 3 {
		t.Fatalf("audit rows after a refused read = %d", len(rows))
	}

	// An audit row that cannot be written fails the read.
	if _, err := e.db.SQL.ExecContext(ctx, `CREATE FUNCTION actx_no_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'no audit'; END $$;
		CREATE TRIGGER actx_no_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION actx_no_audit()`); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.ReadPluginAccount(ctx, "anthropic", mine); err == nil || got != nil {
		t.Fatalf("read without its audit row = %v %v", got, err)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `DROP TRIGGER actx_no_audit ON audit_logs; DROP FUNCTION actx_no_audit()`); err != nil {
		t.Fatal(err)
	}

	// Listing metadata is not a disclosure and is not audited as one.
	if _, err := e.svc.ListPluginAccounts(ctx, "anthropic", core.PluginAccountQuery{}); err != nil {
		t.Fatal(err)
	}
	if rows := e.auditRows(AuditPluginAccountRead); len(rows) != 3 {
		t.Fatalf("ListPluginAccounts wrote an audit row: %d", len(rows))
	}
}
