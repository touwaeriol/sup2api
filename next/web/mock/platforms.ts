// Mock catalog of gateway platforms (CONTRACTS §13): the core's built-in
// anthropic / openai / gemini, plus the platforms of enabled plugins.
// GET /me/platforms is registered in accounts.ts.

type L = { en: string; zh: string }
export interface MockEndpoint {
  method: string
  path: string
  protocol: string
  billing: 'usage' | 'free'
}
export interface MockPlatform {
  id: string
  label: L
  builtin: boolean
  plugin_key: string | null
  endpoints: MockEndpoint[]
}

const ep = (method: string, path: string, protocol: string, billing: 'usage' | 'free' = 'usage'): MockEndpoint => ({ method, path, protocol, billing })

export const builtinPlatforms: MockPlatform[] = [
  {
    id: 'anthropic',
    label: { en: 'Anthropic', zh: 'Anthropic' },
    builtin: true,
    plugin_key: null,
    endpoints: [ep('POST', '/v1/messages', 'anthropic.messages'), ep('POST', '/v1/messages/count_tokens', 'anthropic.count_tokens', 'free')]
  },
  {
    id: 'openai',
    label: { en: 'OpenAI', zh: 'OpenAI' },
    builtin: true,
    plugin_key: null,
    endpoints: [ep('POST', '/v1/chat/completions', 'openai.chat'), ep('POST', '/v1/responses', 'openai.responses'), ep('POST', '/v1/embeddings', 'openai.embeddings')]
  },
  {
    id: 'gemini',
    label: { en: 'Gemini', zh: 'Gemini' },
    builtin: true,
    plugin_key: null,
    endpoints: [
      ep('POST', '/v1beta/models/:model:generateContent', 'gemini.generate'),
      ep('POST', '/v1beta/models/:model:streamGenerateContent', 'gemini.stream_generate'),
      ep('POST', '/v1beta/models/:model:countTokens', 'gemini.count_tokens', 'free')
    ]
  }
]

/** Platforms declared by plugins, keyed by id; `enabled` mirrors the plugin status. */
export const pluginPlatforms: Array<MockPlatform & { enabled: boolean }> = [
  {
    id: 'myvideo',
    label: { en: 'MyVideo', zh: 'MyVideo 视频' },
    builtin: false,
    plugin_key: 'videogen',
    enabled: true,
    endpoints: [ep('POST', '/v1/video/generations', 'myvideo.generate'), ep('GET', '/v1/video/generations/:id', 'myvideo.status', 'free')]
  },
  {
    // foo_platform is awaiting consent: its platform does not exist yet.
    id: 'foo',
    label: { en: 'Foo', zh: 'Foo' },
    builtin: false,
    plugin_key: 'foo_platform',
    enabled: false,
    endpoints: [ep('POST', '/v1/foo/chat', 'foo.chat')]
  }
]

/** Platforms that exist now: built-in + enabled plugin platforms. */
export function activePlatforms(): MockPlatform[] {
  return [...builtinPlatforms, ...pluginPlatforms.filter((p) => p.enabled).map(({ enabled: _e, ...p }) => p)]
}

export function platformById(id: string): MockPlatform | undefined {
  return activePlatforms().find((p) => p.id === id)
}

/** Label of any known platform (also disabled plugin platforms). */
export function platformLabel(id: string): L {
  return [...builtinPlatforms, ...pluginPlatforms].find((p) => p.id === id)?.label || { en: id, zh: id }
}
