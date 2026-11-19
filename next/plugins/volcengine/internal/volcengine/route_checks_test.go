package volcengine

import (
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// The create dialogs of the asset pages are native code and the routes are
// the only check of what they send, so each limit of the create bodies is
// pinned here: each one refused, the boundary accepted.
func TestCreateBodyChecks(t *testing.T) {
	group := createGroupBody{AccountID: 1, Name: strings.Repeat("名", 128), Title: strings.Repeat("t", 256), Description: strings.Repeat("d", 1024), GroupType: strings.Repeat("g", 64)}
	if errs := group.check(); len(errs) != 0 {
		t.Fatalf("boundary group refused: %v", errs)
	}
	asset := createAssetBody{GroupID: 1, Name: strings.Repeat("n", 128), AssetType: strings.Repeat("a", 64), URL: "https://x.example.com/" + strings.Repeat("u", 2048-len("https://x.example.com/"))}
	if errs := asset.check(); len(errs) != 0 {
		t.Fatalf("boundary asset refused: %v", errs)
	}
	for _, u := range []string{"http://x.example.com/a.png", "tos://bucket/a.mp4"} {
		if errs := (createAssetBody{GroupID: 1, URL: u}).check(); len(errs) != 0 {
			t.Fatalf("%s refused: %v", u, errs)
		}
	}

	cases := []struct {
		name  string
		errs  []string
		field string
	}{
		{"group name of 129 characters", fields(createGroupBody{AccountID: 1, Name: strings.Repeat("n", 129)}.check()), "name"},
		{"group title of 257 characters", fields(createGroupBody{AccountID: 1, Name: "n", Title: strings.Repeat("t", 257)}.check()), "title"},
		{"group description of 1025 characters", fields(createGroupBody{AccountID: 1, Name: "n", Description: strings.Repeat("d", 1025)}.check()), "description"},
		{"group_type of 65 characters", fields(createGroupBody{AccountID: 1, Name: "n", GroupType: strings.Repeat("g", 65)}.check()), "group_type"},
		{"asset name of 129 characters", fields(createAssetBody{GroupID: 1, URL: "https://x.example.com/a", Name: strings.Repeat("n", 129)}.check()), "name"},
		{"asset_type of 65 characters", fields(createAssetBody{GroupID: 1, URL: "https://x.example.com/a", AssetType: strings.Repeat("a", 65)}.check()), "asset_type"},
		{"url of 2049 characters", fields(createAssetBody{GroupID: 1, URL: "https://x.example.com/" + strings.Repeat("u", 2049-len("https://x.example.com/"))}.check()), "url"},
		{"url not http or tos", fields(createAssetBody{GroupID: 1, URL: "ftp://x.example.com/a"}.check()), "url"},
		{"url without a scheme", fields(createAssetBody{GroupID: 1, URL: "x.example.com/a"}.check()), "url"},
		{"url with a space", fields(createAssetBody{GroupID: 1, URL: "https://x.example.com/a b"}.check()), "url"},
		{"url of a scheme only", fields(createAssetBody{GroupID: 1, URL: "https://"}.check()), "url"},
	}
	for _, c := range cases {
		if len(c.errs) != 1 || c.errs[0] != c.field {
			t.Errorf("%s: errors at %v, want %s", c.name, c.errs, c.field)
		}
	}
}

// fields lists the fields errs name, in order.
func fields(errs pluginsdk.FieldErrors) []string {
	out := []string{}
	for _, e := range errs {
		out = append(out, e.GetField())
	}
	return out
}
