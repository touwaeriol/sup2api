package gemini

import (
	"context"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

func TestVersion(t *testing.T) {
	cases := []struct {
		name       string
		meta       *pluginv1.RequestMeta
		want       string
		wantNative bool
	}{
		{"no request", nil, VersionV1Beta, false},
		{"v1beta client", &pluginv1.RequestMeta{ClientProtocol: ProtocolGenerate, Endpoint: "/v1beta/models/:model:generateContent"}, VersionV1Beta, true},
		{"v1 client", &pluginv1.RequestMeta{ClientProtocol: ProtocolStreamGenerate, Endpoint: "/v1/models/:model:streamGenerateContent"}, VersionV1, true},
		{"converted", &pluginv1.RequestMeta{ClientProtocol: "openai.chat", Endpoint: "/v1/chat/completions"}, VersionV1Beta, false},
	}
	for _, c := range cases {
		v, native := ClientVersion(c.meta)
		if got := Version(c.meta); got != c.want || native != c.wantNative || native && v != c.want {
			t.Errorf("%s: Version %q ClientVersion (%q, %v), want %q native %v", c.name, got, v, native, c.want, c.wantNative)
		}
	}
}

// Every route of the preset goes upstream on Version: a Gemini client's
// own version, v1beta for a converted request.
func TestRoutesFollowVersion(t *testing.T) {
	p := standard.New(APIKey("apikey"))
	acc := &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"AIza-0123456789"}`}
	methods := map[string]string{ProtocolGenerate: ":generateContent", ProtocolStreamGenerate: ":streamGenerateContent?alt=sse",
		ProtocolCountTokens: ":countTokens", ProtocolEmbed: ":embedContent", ProtocolBatchEmbed: ":batchEmbedContents"}
	for protocol, method := range methods {
		for client, version := range map[string]string{"/v1/models/:model:x": VersionV1, "/v1beta/models/:model:x": VersionV1Beta, "": VersionV1Beta} {
			meta := &pluginv1.RequestMeta{Protocol: protocol, ClientProtocol: protocol, Endpoint: client, Model: "models/m"}
			if client == "" {
				meta.ClientProtocol, meta.Endpoint = "openai.chat", "/v1/chat/completions"
			}
			out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{Account: acc, Meta: meta, Body: []byte(`{}`)})
			want := "https://generativelanguage.googleapis.com/" + version + "/models/m" + method
			if err != nil || out.GetUrl() != want {
				t.Errorf("%s from %q: %s %v, want %s", protocol, client, out.GetUrl(), err, want)
			}
		}
	}
}
