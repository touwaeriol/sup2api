import type { GroupServedProtocol, Platform } from '@/api/types'
import type { ListedEndpoint } from '@/views/platforms/listedEndpoint'

/**
 * The endpoints of the served protocols, by platform in the catalog's order.
 * A protocol no platform of the catalog declares (the catalog is the built-in
 * fallback, or its plugin is gone) is listed by its id alone, under the
 * platform its id names.
 */
export function servedEndpoints(catalog: readonly Platform[], served: readonly GroupServedProtocol[]): Array<{ id: string; endpoints: ListedEndpoint[] }> {
  const wanted = new Set(served.map(s => s.protocol))
  const out: Array<{ id: string; endpoints: ListedEndpoint[] }> = catalog.map(p => ({ id: p.id, endpoints: p.endpoints.filter(e => wanted.has(e.protocol)) }))
  const found = new Set(out.flatMap(g => g.endpoints.map(e => e.protocol)))
  for (const protocol of wanted) {
    if (found.has(protocol)) continue
    const id = protocol.split('.')[0]
    let group = out.find(g => g.id === id)
    if (!group) out.push((group = { id, endpoints: [] }))
    group.endpoints.push({ protocol })
  }
  return out.filter(g => g.endpoints.length)
}
