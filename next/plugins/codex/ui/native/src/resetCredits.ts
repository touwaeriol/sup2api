import type { ResetCredit, ResetCredits } from './host'

// Pure helpers of the official usage reset tab (CodexResetTab).

/** The usage windows the tab shows, in this order (the plugin's quota keys). */
const USAGE_KEYS = ['5h', '7d'] as const

/** The part of the core's quota snapshot the tab reads (GET /accounts/:id/quota). */
export interface QuotaLike {
  windows?: { key: string; utilization: number; utilization_unknown?: boolean; resets_at: string | null; expired?: boolean }[]
}

export interface UsageWindow {
  key: string
  /** Rounded percent; null when unknown. */
  percent: number | null
  resetsAt: string | null
  full: boolean
}

const isAvailable = (c: ResetCredit) => c.status === 'available'

/** The credits that can be used now, soonest expiry first (the list is sorted by the plugin). */
export function availableCredits(r: ResetCredits | null): ResetCredit[] {
  return (r?.credits || []).filter(isAvailable)
}

/** Every credit of the account: the usable ones first, then the others (being used, used), each keeping the plugin's expiry order. */
export function allCredits(r: ResetCredits | null): ResetCredit[] {
  const list = r?.credits || []
  return [...list.filter(isAvailable), ...list.filter((c) => !isAvailable(c))]
}

/** The 5 h and weekly windows of a snapshot, in USAGE_KEYS order; missing ones are left out. */
export function usageWindows(q: QuotaLike | null | undefined): UsageWindow[] {
  const out: UsageWindow[] = []
  for (const key of USAGE_KEYS) {
    const w = q?.windows?.find((x) => x.key === key)
    if (!w) continue
    const known = !w.utilization_unknown && Number.isFinite(w.utilization)
    out.push({ key, percent: known ? Math.round(w.utilization) : null, resetsAt: w.resets_at, full: known && !w.expired && w.utilization >= 100 })
  }
  return out
}

/** Whether any shown window is used up: only then does a reset free the account. */
export const anyFull = (ws: UsageWindow[]) => ws.some((w) => w.full)

/** Unix seconds → milliseconds for SDateTime; null stays null. */
export const unixMs = (s: number | null | undefined) => (s && s > 0 ? s * 1000 : null)
