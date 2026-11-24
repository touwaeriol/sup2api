// Mock handlers of the plugin market: the only source, the official store,
// and GET /market/plugins as the core lists index.json v2 (CONTRACTS §11.1,
// §136): each plugin's
// publisher with its trust level and status, repo, source, built-in and
// deprecation; latest_version and update_available skip deprecated versions.
import { on } from './router'
import { HOST_VERSION, plugins, type Any } from './plugins'

const sources = [
  { id: 1, name: 'official', url: 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json', enabled: true }
]

const L = (en: string, zh: string) => ({ en, zh })
const official = {
  publisher: 'sub2api',
  publisher_name: L('Sub2API', 'Sub2API'),
  publisher_homepage: 'https://github.com/touwaeriol/sup2api',
  trust: 'official',
  publisher_status: 'active'
}
const release = (repo: string, version: string) => `https://github.com/${repo}/releases/tag/v${version}`
const v = (repo: string, version: string, hostCompat: string, size?: number, deprecated?: Any) => ({
  version,
  host_compat: hostCompat,
  size,
  release_url: release(repo, version),
  ...(deprecated ? { deprecated: { reason: deprecated } } : {})
})
const first = (key: string, name: Any, description: Any, versions: Any[], extra: Any = {}) => ({
  key,
  name,
  description,
  ...official,
  repo: 'touwaeriol/sup2api',
  source_url: `https://github.com/touwaeriol/sup2api/tree/main/next/plugins/${key}`,
  builtin: false,
  versions,
  ...extra
})

const market: Any[] = [
  first(
    'anthropic',
    L('Anthropic', 'Anthropic'),
    L('Claude API keys and OAuth accounts.', 'Claude API Key 与 OAuth 账号。'),
    [
      v(
        'touwaeriol/sup2api',
        '0.1.0',
        '>=0.1.0 <0.2.0',
        9_830_400,
        L('Fixes a token count bug; upgrade to 0.2.0.', '有 token 计数错误，请升级到 0.2.0。')
      ),
      v('touwaeriol/sup2api', '0.2.0', '>=0.1.0 <0.2.0', 10_223_616),
      // needs a newer core than HOST_VERSION
      v('touwaeriol/sup2api', '0.3.0', '>=0.2.0 <0.3.0', 10_485_760)
    ],
    { builtin: true }
  ),
  first('guard', L('Guard', '请求守卫'), L('Request inspection and blocking.', '请求检查与拦截。'), [
    v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 7_340_032)
  ]),
  first('openai', L('OpenAI', 'OpenAI'), L('OpenAI API keys for the built-in openai platform.', '内置 openai 平台的 OpenAI API Key。'), [
    v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 8_912_896)
  ]),
  first(
    'gemini',
    L('Gemini', 'Gemini'),
    L('Google AI Studio API keys for the built-in gemini platform.', '内置 gemini 平台的 Google AI Studio API Key。'),
    [v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 8_650_752)]
  ),
  // Community publishers: not installed, a deprecated plugin, a revoked
  // publisher and one the index does not list.
  {
    key: 'bar_platform',
    name: L('Bar Platform', 'Bar 平台'),
    description: L('Community adapter for the Bar API.', 'Bar API 的社区适配器。'),
    publisher: 'bar-labs',
    publisher_name: L('Bar Labs', 'Bar 实验室'),
    trust: 'community',
    publisher_status: 'active',
    repo: 'bar-labs/s2a-bar',
    builtin: false,
    versions: [v('bar-labs/s2a-bar', '1.0.0', '>=0.1.0', 6_291_456)]
  },
  {
    key: 'foo_platform',
    name: L('Foo Platform', 'Foo 平台'),
    description: L('Community adapter for the Foo API.', 'Foo API 的社区适配器。'),
    publisher: 'foo-labs',
    publisher_name: L('Foo Labs', 'Foo 实验室'),
    trust: 'verified',
    publisher_status: 'active',
    repo: 'foo-labs/s2a-foo',
    builtin: false,
    deprecated: { reason: L('Superseded by bar_platform.', '已由 bar_platform 取代。') },
    versions: [v('foo-labs/s2a-foo', '1.0.0', '>=0.1.0')]
  },
  {
    key: 'legacy_tool',
    name: L('Legacy tool', '旧版工具'),
    description: L('Its publisher was revoked.', '发布者已被吊销。'),
    publisher: 'someone',
    publisher_name: L('Someone', 'Someone'),
    trust: 'community',
    publisher_status: 'revoked',
    repo: 'someone/legacy',
    builtin: false,
    versions: [v('someone/legacy', '0.0.3', '>=0.0.1 <0.1.0')]
  },
  {
    key: 'orphan',
    name: L('Orphan', '无主插件'),
    description: L('Its publisher is not in the index.', '索引中没有它的发布者。'),
    publisher: 'ghost',
    trust: '',
    publisher_status: 'missing',
    repo: 'ghost/orphan',
    builtin: false,
    versions: [v('ghost/orphan', '1.0.0', '>=0.1.0')]
  }
]

/** The store listing of key. */
export function marketEntry(key: string): Any | undefined {
  return market.find(x => x.key === key)
}

/** Newest compatible version that is not deprecated, as the core computes latest_version. */
function latestOf(versions: Any[]): string {
  const ok = versions.filter(x => x.compatible && !x.deprecated).map(x => x.version)
  return ok.sort((a, b) => cmp(b, a))[0] ?? ''
}

function listed(m: Any): Any {
  const versions = [...m.versions]
    .sort((a, b) => cmp(b.version, a.version))
    .map((x: Any) => ({ ...x, compatible: satisfies(HOST_VERSION, x.host_compat) }))
  const installed = plugins.get(m.key)?.active_version ?? undefined
  const latest = latestOf(versions)
  return {
    ...m,
    versions,
    installed_version: installed,
    latest_version: latest,
    update_available: !!installed && !!latest && cmp(latest, installed) > 0
  }
}

on('GET', '/market/sources', () => sources)

// host_version sits next to data (CONTRACTS §14.3); the listing is not paged.
on('GET', '/market/plugins', () => ({ data: market.map(listed), host_version: HOST_VERSION }))

function cmp(a: string, b: string): number {
  const pa = a.split('.').map(Number)
  const pb = b.split('.').map(Number)
  for (let i = 0; i < 3; i++) if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) - (pb[i] || 0)
  return 0
}

/** Minimal semver range check: space-separated >=, >, <=, <, = comparators. */
function satisfies(version: string, range: string | undefined): boolean {
  if (!range) return true
  const ops: Record<string, (d: number) => boolean> = { '>=': d => d >= 0, '>': d => d > 0, '<=': d => d <= 0, '<': d => d < 0 }
  return range
    .trim()
    .split(/\s+/)
    .every(c => {
      const m = /^(>=|<=|>|<|=)?v?(\d+(?:\.\d+){0,2})$/.exec(c)
      if (!m) return true
      const d = cmp(version, m[2])
      return (ops[m[1]] ?? ((x: number) => x === 0))(d)
    })
}
