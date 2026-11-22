// The usage limit card of the account editor (CONTRACTS §73). The limit has
// its own endpoint: it is loaded the first time the card is shown for a saved
// account and saved after the account when it changed. The PUT replaces the
// whole config, so until the GET succeeded the card is read-only and nothing
// is saved. Periods follow the browser's time zone once the period, its
// length or its start is edited; other edits keep the saved zone. A limit the
// account never saved (loadedUsageLimit) starts in the browser's zone.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AccountUsageLimit } from '@/api/types'
import { browserTZ } from '@/components/stats/stats'
import { errorMessage } from '@/utils/errors'
import { formatMoney } from '@/utils/format'
import { defaultUsageLimit, fetchUsageLimit, loadedUsageLimit, PERIOD_COUNT_MAX, saveUsageLimit, usageLimitBody, usageLimitError, usagePeriodLabel } from './accountUsage'

/** idle: a saved account's limit not asked for yet; only `ready` is editable and saved. */
export type UsageLimitStatus = 'idle' | 'loading' | 'ready' | 'failed'

const MESSAGES = { amount: 'accounts.usageLimit.amountInvalid', period_count: 'accounts.usageLimit.periodCountInvalid' } as const
/** The fields that place the periods on a wall clock: editing one takes the browser's time zone. */
const PERIOD_FIELDS = ['period', 'period_count', 'anchor'] as const
/** What decides whether the limit changed: the zone alone is not an edit. */
const fingerprint = (v: AccountUsageLimit) => JSON.stringify({ ...v, timezone: undefined })

/** The limit being edited, the fingerprint it was loaded or saved with, the card's state and its model. */
function useLimitState() {
  const value = ref<AccountUsageLimit>(defaultUsageLimit())
  const saved = ref('')
  const status = ref<UsageLimitStatus>('ready')
  const errors = ref<Record<string, string>>({})
  function reset(v: AccountUsageLimit, next: UsageLimitStatus) {
    value.value = v
    saved.value = fingerprint(v)
    status.value = next
    errors.value = {}
  }
  /** The card's model: an edit of the periods takes the browser's time zone, the one saved. */
  const limit = computed({
    get: () => value.value,
    set: (v: AccountUsageLimit) => (value.value = PERIOD_FIELDS.some((k) => v[k] !== value.value[k]) ? { ...v, timezone: browserTZ() } : v)
  })
  return { value, saved, status, errors, reset, limit }
}
type LimitState = ReturnType<typeof useLimitState>

/** Loads the limit of the account the first time the card is shown; a stale answer is dropped. */
function useLimitLoader(s: LimitState, accountId: () => number | undefined, shown: () => boolean) {
  const loadError = ref('')
  let forId: number | undefined | null = null
  let seq = 0
  async function load() {
    const id = accountId()
    if (!id || s.status.value === 'loading' || s.status.value === 'ready') return
    const my = ++seq
    s.status.value = 'loading'
    try {
      const loaded = loadedUsageLimit(await fetchUsageLimit(id))
      if (my === seq) s.reset(loaded, 'ready')
    } catch (e) {
      if (my !== seq) return
      loadError.value = errorMessage(e)
      s.status.value = 'failed'
    }
  }
  function follow([id, visible]: readonly [number | undefined, boolean]) {
    if (id !== forId) {
      forId = id
      seq++
      s.reset(defaultUsageLimit(), id ? 'idle' : 'ready')
    }
    if (visible) void load()
  }
  watch(() => [accountId(), shown()] as const, follow, { immediate: true })
  return { loadError, load }
}

export function useUsageLimitEdit(accountId: () => number | undefined, shown: () => boolean) {
  const { t } = useI18n()
  const state = useLimitState()
  const { value, saved, status, errors, reset, limit } = state
  const { loadError, load } = useLimitLoader(state, accountId, shown)
  /** Shows a value (a declared default) as the unchanged one: saving sends it only once edited. */
  const prefill = (v: Partial<AccountUsageLimit>) => reset({ ...defaultUsageLimit(), ...v }, status.value)
  /** Shows the card's errors and reports whether they block saving; a limit that never loaded is not checked. */
  function invalid(): boolean {
    const field = status.value === 'ready' ? usageLimitError(value.value) : null
    errors.value = field ? { [field]: t(MESSAGES[field], { max: PERIOD_COUNT_MAX[value.value.period] }) } : {}
    return field !== null
  }
  /** Saves the limit of the saved account when it loaded and changed. */
  async function save(a: Account) {
    if (status.value !== 'ready' || fingerprint(value.value) === saved.value) return
    const body = usageLimitBody(value.value)
    await saveUsageLimit(a.id, body)
    reset(body, 'ready')
  }
  /** "Usage limit $200.00 per week" of the section navigation, empty when off. */
  const summary = computed(() => {
    const l = value.value
    return l.enabled ? t('accounts.usageLimit.summary', { amount: formatMoney(l.amount, 2), period: usagePeriodLabel(t, l.period, l.period_count) }) : ''
  })
  return { limit, status, loadError, errors, summary, load, prefill, invalid, save }
}

export type UsageLimitEditor = ReturnType<typeof useUsageLimitEdit>
