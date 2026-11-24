// The egress domains of a review: the net host permission's scope, against
// what the plugin holds now (its current grant, else the diff's previous scope).
import type { HostPermissionReview, PluginReview, ReviewDiffItem } from '@/api/types'
import { asArray } from '../pluginUtil'

const NET = 'net'
const domainsOf = (scope: Record<string, unknown> | null | undefined) => asArray<string>(scope?.domains)

/** The domains held before this version; null when the plugin did not hold net (every domain is new). */
function previousDomains(r: PluginReview, net: HostPermissionReview): Set<string> | null {
  if (!r.diff || asArray<ReviewDiffItem>(r.diff.added).some(d => d.id === NET)) return null
  const widened = asArray<ReviewDiffItem>(r.diff.widened).find(d => d.id === NET)
  return new Set(net.current_scope ? domainsOf(net.current_scope) : widened ? domainsOf(widened.previous_scope) : domainsOf(net.scope))
}

/** Each domain the review's net permission names, and whether this version adds it (an install adds none: nothing is held). */
export function egressDomains(r: PluginReview): Array<{ host: string; added: boolean }> {
  const net = asArray<HostPermissionReview>(r.host_permissions).find(hp => hp.id === NET)
  if (!net) return []
  const before = previousDomains(r, net)
  return domainsOf(net.scope).map(host => ({ host, added: !!r.diff && (before === null || !before.has(host)) }))
}
