// A tier condition as the console edits and reads it (TierCondTree, the tier
// cards and the price summaries): a new comparison, a variable change that
// keeps the zone right, whether a change stays within the depth limit, and
// the condition in words ("len > 200,000 且 小时(Asia/Shanghai) ≥ 22").
import { currentLocale } from '@/i18n'
import { formatNumber } from '@/utils/format'
import { condChildren, condDepth, condKind, isCompound, isSet, isTimeVar, num, MAX_COND_DEPTH, type CmpOp, type CondLeaf, type CondVar, type TierCond } from './priceExpr'

type Translate = (key: string, params?: Record<string, unknown>) => string

/** The zone a time comparison starts in, as a time rule's does. */
export const DEFAULT_TZ = 'Asia/Shanghai'

/** A comparison to fill in: on the input length, its value blank. */
export const newLeaf = (): CondLeaf => ({ var: 'len', op: '>', value: null })

/** Changes what a comparison reads: a time keeps its zone (DEFAULT_TZ for the first), any other has none. */
export function setLeafVar(leaf: CondLeaf, v: CondVar) {
  leaf.var = v
  if (isTimeVar(v)) leaf.tz ??= DEFAULT_TZ
  else delete leaf.tz
}

/** Whether a node at `level` (the root is 1) may grow `extra` levels deeper and stay within MAX_COND_DEPTH. */
export const roomFor = (n: TierCond, level: number, extra: number) => level - 1 + condDepth(n) + extra <= MAX_COND_DEPTH

const intlLocale = () => (currentLocale() === 'zh' ? 'zh-CN' : 'en-US')

/** The weekday names from Sunday (0), in the console's language. */
export function weekdayNames(): string[] {
  const f = new Intl.DateTimeFormat(intlLocale(), { weekday: 'long', timeZone: 'UTC' })
  // 2026-01-04 is a Sunday.
  return Array.from({ length: 7 }, (_, d) => f.format(new Date(Date.UTC(2026, 0, 4 + d))))
}

const OP_TEXT: Record<CmpOp, string> = { '<': '<', '<=': '≤', '>': '>', '>=': '≥', '==': '=', '!=': '≠' }

function leafText(t: Translate, n: CondLeaf): string {
  const v = isSet(n.value) ? num(n.value) : null
  const op = OP_TEXT[n.op] ?? n.op
  if (!isTimeVar(n.var)) return `${n.var} ${op} ${v === null ? '…' : formatNumber(v, 6)}`
  const value = v === null ? '…' : n.var === 'weekday' ? (weekdayNames()[v] ?? String(v)) : String(v)
  return `${t(`prices.cond.var.${n.var}`)}(${n.tz || 'UTC'}) ${op} ${value}`
}

/** A condition in words, grouped as its expression is (whenExpr): a group of one reads as its child. */
export function condText(t: Translate, n: TierCond): string {
  const kind = condKind(n)
  if (kind === 'leaf') return leafText(t, n as CondLeaf)
  if (kind === 'not') return t('prices.cond.notText', { cond: condText(t, condChildren(n)[0]) })
  const kids = kind ? condChildren(n) : []
  const sep = ` ${t(kind === 'or' ? 'prices.cond.or' : 'prices.cond.and')} `
  return kids.map(k => (kids.length > 1 && isCompound(k) ? `(${condText(t, k)})` : condText(t, k))).join(sep)
}

const COMMON_TZ = ['UTC', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Asia/Singapore', 'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Los_Angeles']
const ALL_TZ = Intl.supportedValuesOf('timeZone').filter(z => !COMMON_TZ.includes(z))
const zoneOption = (value: string) => ({ value, label: value })

/** The zones a time comparison picks from, in two groups: the common ones and every other IANA zone; a zone neither lists heads them. */
export function tzOptions(t: Translate, current: string | undefined) {
  const listed = !current || COMMON_TZ.includes(current) || ALL_TZ.includes(current)
  return [
    ...(listed ? [] : [zoneOption(current)]),
    { value: null, label: t('prices.cond.commonTz'), options: COMMON_TZ.map(zoneOption) },
    { value: null, label: t('prices.cond.allTz'), options: ALL_TZ.map(zoneOption) }
  ]
}
