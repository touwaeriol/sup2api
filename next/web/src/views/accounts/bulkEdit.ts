// Bulk edit of account core properties: the PATCH body for one account from
// the ticked fields. Group changes are relative to the account's own groups
// (add / remove) or replace them; the session cap applies with its window. An
// account a change would leave as it is gets no request (null). The two
// protocol fields (DESIGN-2026-11-17-PROTOCOL-BINDING §4) replace the
// account's own.
import type { Account } from '@/api/types'
import { sameModelProtocols, sameProtocols, type ProtocolBinding } from './protocols/protocolBinding'
import { windowOf } from './spmWindow'

export type GroupMode = 'replace' | 'add' | 'remove'
export type NumberField = 'priority' | 'weight' | 'max_concurrency' | 'rpm_limit' | 'tpm_limit' | 'spm_limit'
export type BulkFields = Record<NumberField, number | null> & {
  /** Window of spm_limit, applied with it; null = the default. */
  spm_window_seconds: number | null
  group_ids: number[]
  groupMode: GroupMode
  proxy_id: number | null
  auto_disable: boolean
} & ProtocolBinding
export type BulkFlags = Record<NumberField | 'groups' | 'proxy' | 'auto_disable' | keyof ProtocolBinding, boolean>

/** [min, max] the core accepts (account input validation). */
export const BULK_LIMITS = {
  priority: [0, 1_000_000],
  weight: [1, 1000],
  max_concurrency: [0, 100_000],
  rpm_limit: [0, 10_000_000],
  tpm_limit: [0, 1_000_000_000_000],
  spm_limit: [0, 10_000_000]
} as const

/** Same ids, in any order. */
export const sameSet = (a: number[], b: number[]) => a.length === b.length && a.every((x) => b.includes(x))

export function buildBulkPatch(a: Account, v: BulkFields, on: BulkFlags): Record<string, unknown> | null {
  const body: Record<string, unknown> = {}
  const num = (key: NumberField, current: number | undefined) => {
    if (on[key] && v[key] !== null && Number(v[key]) !== current) body[key] = Number(v[key])
  }
  num('priority', a.priority ?? 0)
  num('weight', a.weight ?? 1)
  num('max_concurrency', a.max_concurrency ?? 0)
  num('tpm_limit', a.tpm_limit ?? 0)
  num('rpm_limit', a.rpm_limit ?? 0)
  num('spm_limit', a.spm_limit ?? 0)
  if (on.spm_limit && windowOf(v.spm_window_seconds) !== windowOf(a.spm_window_seconds)) body.spm_window_seconds = windowOf(v.spm_window_seconds)
  if (on.groups) {
    const current = a.group_ids ?? []
    let next: number[]
    if (v.groupMode === 'add') next = [...current, ...v.group_ids.filter((id) => !current.includes(id))]
    else if (v.groupMode === 'remove') next = current.filter((id) => !v.group_ids.includes(id))
    else next = [...v.group_ids]
    if (!sameSet(current, next)) body.group_ids = next
  }
  if (on.proxy && (a.proxy_id ?? null) !== v.proxy_id) body.proxy_id = v.proxy_id
  if (on.auto_disable && (a.auto_disable ?? true) !== v.auto_disable) body.auto_disable = v.auto_disable
  if (on.protocols && !sameProtocols(a.protocols, v.protocols)) body.protocols = v.protocols
  if (on.model_protocols && !sameModelProtocols(a.model_protocols, v.model_protocols)) body.model_protocols = v.model_protocols
  return Object.keys(body).length ? body : null
}
