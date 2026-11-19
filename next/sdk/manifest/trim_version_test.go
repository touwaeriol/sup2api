package manifest

import "testing"

func TestTrimVersion(t *testing.T) {
	cases := []struct {
		name, path string
		versions   []string
		want       string
	}{
		{"no versions", "/api/v1", nil, "/api/v1"},
		{"empty versions", "/api/v1", []string{}, "/api/v1"},
		{"whole segment", "/api/v1", []string{"/v1"}, "/api"},
		{"only the version", "/v1", []string{"/v1"}, ""},
		{"inside a segment", "/apixv1", []string{"/v1"}, "/apixv1"},
		{"not at the end", "/v1/chat", []string{"/v1"}, "/v1/chat"},
		{"escaped slash", "/a%2Fv1", []string{"/v1"}, "/a%2Fv1"},
		{"escaped slash then a segment", "/a%2F/v1", []string{"/v1"}, "/a%2F"},
		{"case-sensitive", "/api/V1", []string{"/v1"}, "/api/V1"},
		{"multi-segment version", "/api/openai/v1", []string{"/openai/v1"}, "/api"},
		{"longest", "/api/openai/v1", []string{"/openai/v1", "/v1"}, "/api"},
		{"longest whatever the order", "/api/openai/v1", []string{"/v1", "/openai/v1"}, "/api"},
		{"at most one", "/api/v1/v1", []string{"/v1"}, "/api/v1"},
		{"trailing slash is not a version", "/api/v1/", []string{"/v1"}, "/api/v1/"},
	}
	for _, c := range cases {
		if got := TrimVersion(c.path, c.versions); got != c.want {
			t.Errorf("%s: TrimVersion(%q, %q) = %q, want %q", c.name, c.path, c.versions, got, c.want)
		}
	}
}
