import { currentLocale } from '@/i18n'

/** Formats a decimal money string/number as USD with up to `digits` decimals. */
export function formatMoney(v: string | number | null | undefined, digits = 4): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = typeof v === 'number' ? v : Number(v)
  if (!Number.isFinite(n)) return String(v)
  const abs = Math.abs(n)
  const d = abs !== 0 && abs < 0.01 ? Math.max(digits, 6) : digits
  const s = abs.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: d })
  return (n < 0 ? '-$' : '$') + s
}

/**
 * An amount in a payment currency (ISO 4217) with its narrow symbol in the
 * console language ("¥73.00", not "CN¥"); an unknown code is written before
 * the number, '—' when missing.
 */
export function formatCurrency(v: string | number | null | undefined, currency: string): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return String(v)
  try {
    return new Intl.NumberFormat(currentLocale() === 'zh' ? 'zh-CN' : 'en-US', { style: 'currency', currency, currencyDisplay: 'narrowSymbol' }).format(n)
  } catch {
    return `${currency} ${n.toFixed(2)}`
  }
}

/**
 * An account balance as the upstream reported it (CONTRACTS §52): the
 * decimal amount with thousands separators and without trailing zeros
 * (nothing rounded off, the string is never turned into a float), then the
 * upstream's unit as it gave it, if any: "7.5 USD", "1,250,000". A unit is
 * never turned into a symbol: we do not interpret the upstream's units.
 */
export function formatBalance(amount: string, unit?: string): string {
  const m = /^(-?)(\d+)(?:\.(\d*?)0*)?$/.exec(amount)
  const n = m ? m[1] + m[2].replace(/\B(?=(\d{3})+$)/g, ',') + (m[3] ? '.' + m[3] : '') : amount
  return unit ? `${n} ${unit}` : n
}

/** Signed amount for ledger deltas: +12.3400 / -0.0158. */
export function formatDelta(v: string | number | null | undefined, digits = 4): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return String(v)
  return (n > 0 ? '+' : '') + n.toFixed(digits)
}

export function formatNumber(v: string | number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return String(v)
  return n.toLocaleString(currentLocale() === 'zh' ? 'zh-CN' : 'en-US', { maximumFractionDigits: digits })
}

/**
 * "12.3%" of part / whole, '—' when whole is 0 or part is missing. `floor`
 * rounds down, so a share just under 100 % never reads "100.0%".
 */
export function formatPercent(part: string | number | null | undefined, whole: string | number | null | undefined, opts: { floor?: boolean } = {}): string {
  const w = Number(whole) || 0
  if (!w || part === null || part === undefined) return '—'
  const p = (Number(part) * 100) / w
  return (opts.floor ? Math.floor(p * 10 + 1e-9) / 10 : p).toFixed(1) + '%'
}

/** 1234 -> "1.2K", 3_200_000 -> "3.2M"; small numbers stay as they are. */
export function formatCompact(n: number): string {
  if (!Number.isFinite(n)) return '—'
  const a = Math.abs(n)
  const trimZero = (v: number) => v.toFixed(1).replace(/\.0$/, '')
  if (a >= 1_000_000) return `${trimZero(n / 1_000_000)}M`
  if (a >= 1_000) return `${trimZero(n / 1_000)}K`
  return String(n)
}

export function formatDateTime(v: string | number | Date | null | undefined): string {
  if (!v) return '—'
  const d = v instanceof Date ? v : new Date(v)
  if (Number.isNaN(d.getTime())) return String(v)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export function formatTime(v: string | number | Date | null | undefined): string {
  if (!v) return '—'
  const d = v instanceof Date ? v : new Date(v)
  if (Number.isNaN(d.getTime())) return String(v)
  const today = new Date()
  const p = (x: number) => String(x).padStart(2, '0')
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  if (d.toDateString() === today.toDateString()) return hm
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${hm}`
}

/** "3s ago" style; t is the i18n translate function. */
export function formatRelative(v: string | number | Date | null | undefined, t: (k: string, p?: any) => string): string {
  if (!v) return '—'
  const d = v instanceof Date ? v : new Date(v)
  const s = Math.round((Date.now() - d.getTime()) / 1000)
  if (Number.isNaN(s)) return String(v)
  if (s < 60) return t('common.secondsAgo', { n: Math.max(0, s) })
  if (s < 3600) return t('common.minutesAgo', { n: Math.floor(s / 60) })
  if (s < 86400) return t('common.hoursAgo', { n: Math.floor(s / 3600) })
  return t('common.daysAgo', { n: Math.floor(s / 86400) })
}

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let x = n
  while (x >= 1024 && i < u.length - 1) {
    x /= 1024
    i++
  }
  return `${x.toFixed(x < 10 && i > 0 ? 1 : 0)}${u[i]}`
}

/** Start of today (local) as RFC 3339. */
export function startOfToday(): Date {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return d
}

export function daysAgo(n: number): Date {
  const d = startOfToday()
  d.setDate(d.getDate() - n)
  return d
}

// Re-export to preserve the shared helper used throughout the console.
export { copyText } from './clipboard'
