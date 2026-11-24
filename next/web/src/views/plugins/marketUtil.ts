// Helpers of the plugin store pages (GET /market/plugins, CONTRACTS §11.1, §136, §137).
import type { MarketPlugin, MarketVersion } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'
import { lt } from '@/i18n'
import { OFFICIAL_PUBLISHER, compareVersions, displayPluginId } from './pluginUtil'

/** Where a store plugin stands here: not installed, an upgrade offered, installed and up to date. */
export type MarketState = 'install' | 'upgrade' | 'installed'

/** The market list's status filter ('' = all). */
export type MarketStatusFilter = '' | 'upgrade' | 'installed' | 'notInstalled'

/** The version cannot run on this core (CONTRACTS §14.3). */
export function incompatible(v: MarketVersion | undefined | null): boolean {
  return v?.compatible === false
}

export function versionOf(m: MarketPlugin, version: string): MarketVersion | undefined {
  return (m.versions || []).find(v => v.version === version)
}

/** The versions of m, newest first. */
export function sortedVersions(m: MarketPlugin): MarketVersion[] {
  return [...(m.versions || [])].sort((a, b) => compareVersions(b.version, a.version))
}

/** Newest version that is not deprecated, compatible or not ('' when none). */
export function newestOf(m: MarketPlugin): string {
  return sortedVersions(m).find(v => !v.deprecated)?.version || ''
}

/** The newest deprecated version of m, whose reason the list shows (none for a plugin deprecated as a whole). */
export function deprecatedVersion(m: MarketPlugin): MarketVersion | undefined {
  return m.deprecated ? undefined : sortedVersions(m).find(v => v.deprecated)
}

/** The listing has versions but none runs on this core. */
export function noCompatible(m: MarketPlugin): boolean {
  return (m.versions || []).length > 0 && (m.versions || []).every(incompatible)
}

/** Only an active publisher's plugins install: a revoked one or one the index does not list is refused. */
export function publisherBlocked(m: MarketPlugin): boolean {
  return m.publisher_status !== 'active'
}

/** Display name of the publisher of m. */
export function publisherLabel(m: MarketPlugin): string {
  return lt(m.publisher_name) || m.publisher
}

/** One option per publisher of items, the official one first, then by name. */
export function publisherOptions(items: MarketPlugin[]): { value: string; label: string }[] {
  const byId = new Map(items.map(m => [m.publisher, publisherLabel(m)]))
  const rank = (id: string) => (id === OFFICIAL_PUBLISHER ? 0 : 1)
  return [...byId].map(([value, label]) => ({ value, label })).sort((a, b) => rank(a.value) - rank(b.value) || a.label.localeCompare(b.label))
}

/** GitHub page of the repository m is released from ('' when the index names none). */
export function repoURL(m: MarketPlugin): string {
  return m.repo ? `https://github.com/${m.repo}` : ''
}

/** The release's tag, the last part of its GitHub page ('' without one). */
export function releaseTag(url: string | undefined): string {
  return (url || '').replace(/\/+$/, '').split('/').pop() || ''
}

/** A sha256 shortened for a table: its first and last four characters. */
export function shortSha(sha: string): string {
  return sha.length > 12 ? `${sha.slice(0, 4)}…${sha.slice(-4)}` : sha
}

/** Whether m matches the search keyword kw (lower case): name, id as shown, key, description or publisher. */
export function matches(m: MarketPlugin, kw: string): boolean {
  const texts = [m.key, displayPluginId(m.id), lt(m.name), lt(m.description), m.publisher, publisherLabel(m)]
  return texts.some(s => (s || '').toLowerCase().includes(kw))
}

/** The newest version when it is newer than the latest one (latest_version, as the core reports it) only because this core cannot run it ('' otherwise). */
export function newerIncompatible(m: MarketPlugin): string {
  const newest = newestOf(m)
  const latest = m.latest_version
  return newest && latest && newest !== latest && incompatible(versionOf(m, newest)) ? newest : ''
}

/** The version of m installed here ('' when none): the plugin's row first, the listing's otherwise. */
export function installedVersionOf(m: MarketPlugin, p: PluginSummary | undefined): string {
  return p?.active_version || p?.desired_version || m.installed_version || ''
}

/** Where m stands here; an upgrade is offered only to a version this core runs, never to a deprecated one (latest_version). */
export function marketStateOf(m: MarketPlugin, p: PluginSummary | undefined): MarketState {
  const iv = installedVersionOf(m, p)
  if (!iv) return p ? 'installed' : 'install'
  return m.latest_version && compareVersions(m.latest_version, iv) > 0 ? 'upgrade' : 'installed'
}

/** The status filter lets a plugin in that state through. */
export function stateMatches(state: MarketState, filter: MarketStatusFilter): boolean {
  if (filter === 'notInstalled') return state === 'install'
  if (filter === 'installed') return state !== 'install'
  return !filter || state === filter
}

/** The store versions of m that can be installed here: compatible, not the installed one, and for a built-in plugin never below it. */
export function offersVersion(m: MarketPlugin, v: MarketVersion, installed: string): boolean {
  if (incompatible(v) || v.version === installed) return false
  return !(m.builtin && installed && compareVersions(v.version, installed) < 0)
}

/** The store's upgrade of each installed plugin, by plugin id (installed plugins the store has nothing newer for are left out). */
export function upgradesByPlugin(items: MarketPlugin[], installed: PluginSummary[]): Record<string, MarketPlugin> {
  const byId = new Map(items.map(m => [m.id, m]))
  const out: Record<string, MarketPlugin> = {}
  for (const p of installed) {
    const m = byId.get(p.key)
    if (m && !publisherBlocked(m) && marketStateOf(m, p) === 'upgrade') out[p.key] = m
  }
  return out
}

/** Where an installed plugin's current version came from: the store (indexed), the core image (built-in) or a manual upload. */
export type PluginSource = 'market' | 'builtin' | 'upload'

/**
 * The source of p's current version, from what the plugin list says: a store
 * install's signature status is indexed (pkg.SigIndexed); otherwise a
 * built-in plugin's version came with the core, any other was uploaded.
 */
export function pluginSource(p: Pick<PluginSummary, 'signature_status' | 'builtin'>): PluginSource {
  if (p.signature_status === 'indexed') return 'market'
  return p.builtin ? 'builtin' : 'upload'
}
