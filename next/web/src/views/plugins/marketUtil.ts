// Helpers of the plugin market page (GET /market/plugins, CONTRACTS §11.1, §136).
import type { MarketPlugin, MarketVersion } from '@/api/types'
import { lt } from '@/i18n'
import { compareVersions } from './pluginUtil'

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

/** One option per publisher of items, by name. */
export function publisherOptions(items: MarketPlugin[]): { value: string; label: string }[] {
  const byId = new Map(items.map(m => [m.publisher, publisherLabel(m)]))
  return [...byId].map(([value, label]) => ({ value, label })).sort((a, b) => a.label.localeCompare(b.label))
}

/** GitHub page of the repository m is released from ('' when the index names none). */
export function repoURL(m: MarketPlugin): string {
  return m.repo ? `https://github.com/${m.repo}` : ''
}

/** Whether m matches the search keyword kw (lower case). */
export function matches(m: MarketPlugin, kw: string): boolean {
  const texts = [m.key, lt(m.name), lt(m.description), m.publisher, publisherLabel(m)]
  return texts.some(s => (s || '').toLowerCase().includes(kw))
}

/** The newest version when it is newer than the latest one (latest_version, as the core reports it) only because this core cannot run it ('' otherwise). */
export function newerIncompatible(m: MarketPlugin): string {
  const newest = newestOf(m)
  const latest = m.latest_version
  return newest && latest && newest !== latest && incompatible(versionOf(m, newest)) ? newest : ''
}
