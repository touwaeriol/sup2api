// The console shows an account in one of three statuses: enabled, disabled
// (by an operator) or auto-disabled. An active account taken out of
// scheduling reads as auto-disabled; a cooldown or an
// orphaned plugin is a note on the status, not a status of its own.
import type { Account } from '@/api/types'
import { formatDateTime, formatMoney, formatTime } from '@/utils/format'
import { usagePeriodLabel } from './accountUsage'

type Translate = (key: string, named?: Record<string, unknown>) => string

export type AccountStatusKey = 'active' | 'disabled' | 'auto_disabled'

/** Filter order of the accounts list. */
export const accountStatusKeys: AccountStatusKey[] = ['active', 'auto_disabled', 'disabled']

export function accountStatusKey(a: Pick<Account, 'status' | 'schedulable'>): AccountStatusKey {
  if (a.status === 'disabled') return 'disabled'
  return a.status === 'active' && a.schedulable ? 'active' : 'auto_disabled'
}

/** The list's enable switch is on only for an enabled account: an automatic disable or scheduling turned off reads as off. */
export const accountEnabled = (a: Pick<Account, 'status' | 'schedulable'>) => accountStatusKey(a) === 'active'

/**
 * PATCH body of the enable switch and the bulk enable / disable. Enabling is
 * the manual enable of the core: status active clears an automatic disable,
 * its reason and release time (a usage limit lets the account through for that
 * period, CONTRACTS §73.4) and turns scheduling back on; a cooldown stays.
 * Disabling is the manual disable, with scheduling off.
 */
export const enabledPatch = (on: boolean) => ({ status: on ? 'active' : 'disabled', schedulable: on }) as const

/**
 * Enabled is green; auto-disabled with a release time yellow (it comes back by
 * itself); red what needs a person: auto-disabled without a release time,
 * disabled, or an active account taken out of scheduling by hand, which files
 * under auto-disabled (CONTRACTS §73.4).
 */
export function accountStatusTone(a: Pick<Account, 'status' | 'schedulable' | 'orphaned' | 'auto_disabled_until'>) {
  if (a.orphaned) return 'gray'
  const key = accountStatusKey(a)
  if (key === 'active') return 'success'
  return key === 'auto_disabled' && a.status !== 'active' && a.auto_disabled_until ? 'warning' : 'danger'
}

/** Whether an account is cooling down now. */
export const coolingDown = (a: Pick<Account, 'cooldown_until'>) => !!a.cooldown_until && new Date(a.cooldown_until).getTime() > Date.now()

/** How the status of an account reads: detail is the full explanation (the badge's tooltip, the detail dialog), note the short recovery time under the badge. */
export interface StatusView {
  tone: ReturnType<typeof accountStatusTone>
  label: string
  detail?: string
  note?: string
}

/** Why an account is not running: its plugin is gone, its cooldown, its usage limit (in local words) or the server's reason. */
function statusReason(t: Translate, a: Account, cooling: boolean): string {
  if (a.orphaned) return t('accounts.orphanedHint', { plugin: a.plugin_key })
  if (cooling && a.cooldown_reason) return a.cooldown_reason
  if (!a.usage?.limit_disabled) return a.status_reason ?? ''
  const limit = a.usage.limit
  if (!limit) return t('accounts.usage.limitReachedPlain')
  return t('accounts.usage.limitReached', { used: formatMoney(limit.used, 2), amount: formatMoney(limit.amount, 2), period: usagePeriodLabel(t, limit.period, limit.period_count) })
}

/**
 * The status of an account as the list and the detail show it. Auto-disabled
 * reads as one badge, yellow with a release time and red without; why, and
 * when it comes back, go in its detail.
 */
export function statusView(t: Translate, a: Account): StatusView {
  const key = accountStatusKey(a)
  const cooling = key === 'active' && coolingDown(a)
  const view: StatusView = { tone: accountStatusTone(a), label: t(`accounts.status.${key}`), detail: statusReason(t, a, cooling) || undefined }
  if (key === 'auto_disabled' && a.auto_disabled_until) {
    const release = t('accounts.autoReleaseAt', { time: formatDateTime(a.auto_disabled_until) })
    view.detail = view.detail ? `${view.detail}\n${release}` : release
  }
  if (cooling) view.note = t('accounts.cooldownUntil', { time: formatTime(a.cooldown_until) })
  return view
}
