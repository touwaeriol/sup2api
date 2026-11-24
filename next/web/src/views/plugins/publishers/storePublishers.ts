// The store publishers tab's rows: the publishers of the store's listing
// (default channel), with how many plugins each has in the store and how many of those are installed here.
import type { MarketPlugin } from '@/api/marketTypes'
import type { PluginSummary, Trust } from '@/api/types'
import { OFFICIAL_PUBLISHER } from '../pluginUtil'
import { publisherLabel } from '../marketUtil'

export interface StorePublisher {
  id: string
  name: string
  trust: Trust
  status: MarketPlugin['publisher_status']
  homepage?: string
  github: string[]
  plugins: number
  installed: number
}

function rowOf(m: MarketPlugin): StorePublisher {
  const { publisher: id, trust, publisher_status: status, publisher_homepage: homepage } = m
  return { id, name: publisherLabel(m), trust, status, homepage, github: m.publisher_github ?? [], plugins: 0, installed: 0 }
}

/** One row per publisher of items, the official one first; installed counts its store plugins installed here. */
export function storePublishers(items: MarketPlugin[], installed: PluginSummary[]): StorePublisher[] {
  const rows = new Map<string, StorePublisher>()
  for (const m of items) {
    const row = rows.get(m.publisher) ?? rows.set(m.publisher, rowOf(m)).get(m.publisher)!
    row.plugins++
  }
  const listed = new Map(items.map(m => [m.id, m.publisher]))
  for (const p of installed) {
    const row = rows.get(listed.get(p.key) ?? '')
    if (row) row.installed++
  }
  const rank = (r: StorePublisher) => (r.id === OFFICIAL_PUBLISHER ? 0 : 1)
  return [...rows.values()].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id))
}
