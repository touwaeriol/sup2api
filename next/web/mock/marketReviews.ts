// Mock of the review of a third-party plugin from the store (POST
// /plugins/review-from-market): one platform with its endpoints and an
// account type. guard and anthropic have reviews of their own (plugins.ts).
import { displayId } from './pluginIds'

type Any = Record<string, any>

/** The platform pid of a generic plugin, named name, with a billed and a free endpoint. */
function platformOf(pid: string, name: Any) {
  const endpoint = (path: string, billing: string) => ({ method: 'POST', path: `/v1/${pid}/${path}`, protocol: `${pid}.${path}`, billing })
  return { id: pid, label: name, endpoints: [endpoint('chat', 'usage'), endpoint('tokens', 'free')] }
}

/** The host permissions of a generic plugin: kv and log, the platform's, and an optional net. */
const hostPermissions = () => [
  { id: 'kv', risk: 'low' },
  { id: 'log', risk: 'low' },
  { id: 'platform.register', risk: 'high', requires: 'plugin:grant:high' },
  { id: 'gateway.endpoint', risk: 'high', requires: 'plugin:grant:high' },
  { id: 'net', risk: 'high', scope: { domains: ['api.foo.dev'] }, optional: true, requires: 'plugin:grant:high' }
]

/** What a generic plugin declares besides its platform: nothing but its resources. */
const nothingElse = () => ({ hooks: [], jobs: [], events: [], routes: [], menus: [], user_permissions: [], database: null, resources: { memoryMB: 64, cpu: 0.1 } })

export function genericReview(id: string, version: string, entry: Any | undefined): Any {
  const { key = id, name = { en: id }, publisher = 'unknown', trust = 'unsigned' } = entry ?? {}
  // foo_platform declares the "foo" platform (bar_platform "bar"); other generic plugins use their key.
  const pid = key.replace(/_platform$/, '')
  const legacy = key === 'legacy_tool'
  return {
    plugin_key: id,
    display_id: displayId(id),
    version,
    name,
    publisher,
    trust,
    signature_status: 'indexed',
    host_compat_ok: !legacy,
    host_compat: legacy ? '>=0.0.1 <0.1.0' : '>=0.1.0',
    capabilities: [{ id: 'gateway.platform.v1' }, { id: 'platform.adapter.v1' }],
    gateway_endpoints: [{ method: 'POST', path: `/v1/${pid}/chat`, protocol: `${pid}.chat` }],
    platforms: [platformOf(pid, name)],
    // manifest-shaped platform entries are accepted too
    account_types: [{ id: 'apikey', label: { en: 'API key', zh: 'API Key' }, form_mode: 'native', platforms: [{ platform: pid }, 'openai'] }],
    ...nothingElse(),
    external_services: [`api.${pid}.dev`],
    host_permissions: hostPermissions()
  }
}
