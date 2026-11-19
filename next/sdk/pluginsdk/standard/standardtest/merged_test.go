package standardtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
)

// relayManifest is a relay-site plugin (the New API and Sub2API shape): one
// type on the three built-in platforms at the account's own address.
const relayManifest = `{
  "apiVersion": 1, "key": "relaysite", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "platform.adapter.v1"}],
  "accountTypes": [{"id": "apikey", "upstreamFields": ["base_url"],
    "platforms": [{"platform": "openai"}, {"platform": "anthropic"}, {"platform": "gemini"}]}]
}`

// A type merged from the three presets builds, for each file's protocols,
// what that file's plugin built: every forwarded case on an account with
// its own base URL, except where the relay's base URL rule differs on
// purpose (it also takes a trailing /v1beta for a version, which the OpenAI
// and Anthropic presets keep).
func TestMergedReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(relayManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := apikey.Spec{RequireBaseURL: true, Versions: []string{"/v1beta", "/v1"}}
	p := standard.New(standard.Merge("apikey", spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey("")))
	keepsV1beta := func(c *goldentest.Case) bool {
		base, _ := c.BaseURL()
		return !strings.HasSuffix(base, "/v1beta")
	}
	for plugin, protocols := range map[string][]string{
		"openai":    {openai.ProtocolChat, openai.ProtocolResponses, openai.ProtocolResponsesWS, openai.ProtocolEmbeddings},
		"anthropic": {anthropic.ProtocolMessages, anthropic.ProtocolCountTokens},
		"gemini":    {gemini.ProtocolGenerate, gemini.ProtocolStreamGenerate, gemini.ProtocolCountTokens},
	} {
		t.Run(plugin, func(t *testing.T) {
			Run(t, plugin, path, p, goldentest.Select(goldentest.Forwarded("apikey", protocols...)), goldentest.Select(keepsV1beta))
		})
	}
}
