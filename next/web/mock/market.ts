// Mock handlers of the plugin market: GET /market/plugins of a store's channel
// as the core lists index.json v2 (CONTRACTS §11.1, §136, §137): the official
// store's index, or the team store's (community trust at most, no sub2api
// publisher); each plugin's id, its
// publisher with its trust level and status, repo, source, built-in and
// deprecation; latest_version and update_available skip deprecated versions.
import { fail, on } from './router'
import { channelOf } from './marketStores'
import { HOST_VERSION, plugins, type Any } from './plugins'
import { displayId, pluginId } from './pluginIds'


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
const first = (key: string, name: Any, description: Any, versions: Any[], extra: Any = {}): Any => ({
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

// Store entries by publisher: first-party plugins (an upgrade of a built-in
// one, the latest with a deprecated older version), and community ones
// (installed from the store, not installed, deprecated as a whole, a revoked
// publisher and one the index does not list).
const community = (publisher: string, name: Any | undefined, trust: string, status = 'active') => ({
  publisher,
  publisher_name: name,
  trust,
  publisher_status: status,
  builtin: false
})
const third = (publisher: Any, key: string, name: Any, description: Any, repo: string, versions: Any[], extra: Any = {}): Any => ({
  key,
  name,
  description,
  ...publisher,
  repo,
  versions,
  ...extra
})
const erio = community('erio', L('Erio', 'Erio'), 'community')
const barlabs = community('barlabs', L('Bar Labs', 'Bar 实验室'), 'community')
const foolabs = community('foolabs', L('Foo Labs', 'Foo 实验室'), 'verified')
const acme = community('acme', L('Acme', 'Acme'), 'community')

const market: Any[] = [
  first(
    'anthropic',
    L('Anthropic', 'Anthropic'),
    L('Claude API keys and OAuth accounts.', 'Claude API Key 与 OAuth 账号。'),
    [
      v('touwaeriol/sup2api', '0.0.9', '>=0.0.1 <0.2.0', 9_437_184),
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
    v('touwaeriol/sup2api', '0.0.9', '>=0.0.1 <0.2.0', 7_077_888, L('Rules flapped on error rates; upgrade to 0.1.0.', '错误率规则来回抖动，请升级到 0.1.0。')),
    v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 7_340_032)
  ]),
  first('openai', L('OpenAI', 'OpenAI'), L('OpenAI API keys for the built-in openai platform.', '内置 openai 平台的 OpenAI API Key。'), [
    v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 8_912_896)
  ], { builtin: true }),
  first(
    'gemini',
    L('Gemini', 'Gemini'),
    L('Google AI Studio API keys for the built-in gemini platform.', '内置 gemini 平台的 Google AI Studio API Key。'),
    [v('touwaeriol/sup2api', '0.1.0', '>=0.1.0 <0.2.0', 8_650_752)],
    { builtin: true }
  ),
  third(erio, 'codex', L('Codex Plus', 'Codex 增强版'), L('A community Codex plugin: not the official codex, both can be installed.', '第三方 Codex 插件，与官方 codex 是两个不同的插件，可以同时安装。'), 'erio/s2-codex', [
    v('erio/s2-codex', '1.2.0', '>=0.1.4', 5_242_880)
  ]),
  third(barlabs, 'bar_platform', L('Bar Platform', 'Bar 平台'), L('Community adapter for the Bar API.', 'Bar API 的社区适配器。'), 'barlabs/s2a-bar', [
    v('barlabs/s2a-bar', '1.0.0', '>=0.1.0', 6_291_456)
  ]),
  third(foolabs, 'foo_platform', L('Foo Platform', 'Foo 平台'), L('Community adapter for the Foo API.', 'Foo API 的社区适配器。'), 'foolabs/s2a-foo', [
    v('foolabs/s2a-foo', '1.0.0', '>=0.1.0')
  ], { deprecated: { reason: L('Superseded by barlabs/bar_platform.', '已由 barlabs/bar_platform 取代。') } }),
  third(acme, 'log_export', L('Legacy log export', '旧版日志导出'), L('Exports usage logs as CSV.', '把使用日志导出为 CSV。'), 'acme/s2a-logs', [
    v('acme/s2a-logs', '0.3.0', '>=0.1.0', 3_145_728)
  ], { deprecated: { reason: L('Replaced by the usage log export of the console.', '已由控制台“使用日志”的导出功能取代。') } }),
  third(community('someone', L('Someone', 'Someone'), 'community', 'revoked'), 'legacy_tool', L('Legacy tool', '旧版工具'), L('Its publisher was revoked.', '发布者已被吊销。'), 'someone/legacy', [
    v('someone/legacy', '0.0.3', '>=0.0.1 <0.1.0')
  ]),
  third(community('ghost', undefined, '', 'missing'), 'orphan', L('Orphan', '无主插件'), L('Its publisher is not in the index.', '索引中没有它的发布者。'), 'ghost/orphan', [
    v('ghost/orphan', '1.0.0', '>=0.1.0')
  ])
].map(withId)

/** The team store's index (a third-party store): its own publisher, trusted as community. */
const ourteam = community('ourteam', L('Our Team', '我们团队'), 'community')
const teamStore: Any[] = [
  third(ourteam, 'billing_bridge', L('Billing bridge', '内部计费桥'), L('Sends usage to the internal billing system.', '把用量同步到内部计费系统。'), 'ourteam/billing-bridge', [
    v('ourteam/billing-bridge', '0.1.0', '>=0.1.0', 2_097_152),
    v('ourteam/billing-bridge', '0.2.0', '>=0.1.0', 2_228_224)
  ]),
  third(ourteam, 'quota_report', L('Quota report', '额度周报'), L('Weekly quota report by group.', '按分组生成额度周报。'), 'ourteam/quota-report', [
    v('ourteam/quota-report', '1.0.0', '>=0.1.0', 1_572_864)
  ])
].map(withId)

function withId(m: Any): Any {
  return { ...m, id: pluginId(m.publisher, m.key), display_id: displayId(pluginId(m.publisher, m.key)) }
}

/** The store listing of a plugin id, in any store. */
export function marketEntry(id: string): Any | undefined {
  return [...market, ...teamStore].find(x => x.id === id)
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
  const installed = plugins.get(m.id)?.active_version ?? undefined
  const latest = latestOf(versions)
  return {
    ...m,
    versions,
    installed_version: installed,
    latest_version: latest,
    update_available: !!installed && !!latest && cmp(latest, installed) > 0
  }
}

/** Versions only the develop-built channels list yet (testing before main). */
const testing: Record<string, Any[]> = {
  anthropic: [v('touwaeriol/sup2api', '0.2.1', '>=0.1.0 <0.2.0', 10_354_688)],
  _erio__codex: [v('erio/s2-codex', '1.3.0', '>=0.1.4', 5_373_952)]
}

/** The store as a channel lists it: the main branch's index, or that plus the versions in testing; the team store has one index. */
function ofChannel(official: boolean, branch: string): Any[] {
  if (!official) return teamStore
  return branch === 'main' ? market : market.map(m => ({ ...m, versions: [...m.versions, ...(testing[m.id] ?? [])] }))
}

// host_version sits next to data (CONTRACTS §14.3); the listing is not paged.
on('GET', '/market/plugins', req => {
  const c = channelOf(req.query.source_id)
  if (!c?.enabled) return fail(404, 'not_found', 'no such enabled channel')
  return { data: ofChannel(c.store.official, c.branch).map(listed), host_version: HOST_VERSION }
})

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
