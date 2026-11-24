// Mock of the review of a third-party plugin from the store (POST
// /plugins/review-from-market): one platform with its endpoints and an
// account type. guard and anthropic have reviews of their own (plugins.ts).
type Any = Record<string, any>

export function genericReview(id: string, version: string, entry: Any | undefined): Any {
  const { key = id, name = { en: id }, publisher = 'unknown', trust = 'unsigned' } = entry ?? {}
  // foo_platform declares the "foo" platform (bar_platform "bar"); other generic plugins use their key.
  const pid = key.replace(/_platform$/, '')
  return {
    plugin_key: id,
    version,
    name,
    publisher,
    trust,
    signature_status: 'indexed',
    host_compat_ok: key !== 'legacy_tool',
    host_compat: key === 'legacy_tool' ? '>=0.0.1 <0.1.0' : '>=0.1.0',
    capabilities: [{ id: 'gateway.platform.v1' }, { id: 'platform.adapter.v1' }],
    gateway_endpoints: [{ method: 'POST', path: `/v1/${pid}/chat`, protocol: `${pid}.chat` }],
    platforms: [
      {
        id: pid,
        label: name,
        endpoints: [
          { method: 'POST', path: `/v1/${pid}/chat`, protocol: `${pid}.chat`, billing: 'usage' },
          { method: 'POST', path: `/v1/${pid}/tokens`, protocol: `${pid}.tokens`, billing: 'free' }
        ]
      }
    ],
    account_types: [
      // manifest-shaped platform entries are accepted too
      { id: 'apikey', label: { en: 'API key', zh: 'API Key' }, form_mode: 'native', platforms: [{ platform: pid }, 'openai'] }
    ],
    hooks: [],
    jobs: [],
    events: [],
    routes: [],
    menus: [],
    user_permissions: [],
    database: null,
    resources: { memoryMB: 64, cpu: 0.1 },
    external_services: [`api.${pid}.dev`],
    host_permissions: [
      { id: 'kv', risk: 'low' },
      { id: 'log', risk: 'low' },
      { id: 'platform.register', risk: 'high', requires: 'plugin:grant:high' },
      { id: 'gateway.endpoint', risk: 'high', requires: 'plugin:grant:high' },
      { id: 'net', risk: 'high', scope: { domains: ['api.foo.dev'] }, optional: true, requires: 'plugin:grant:high' }
    ]
  }
}
