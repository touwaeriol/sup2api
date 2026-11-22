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

export function useUsageLimitEdit(accountId: () => number | undefined, shown: () => boolean) {
  const { t } = useI18n()
  const value = ref<AccountUsageLimit>(defaultUsageLimit())
  const saved = ref('')
  const status = ref<UsageLimitStatus>('ready')
  const loadError = ref('')
  const errors = ref<Record<string, string>>({})
  let forId: number | undefined | null = null
  let seq = 0

  /** The card's model: an edit of the periods takes the browser's time zone, the one saved. */
  const limit = computed({
    get: () => value.value,
    set: (v: AccountUsageLimit) => (value.value = PERIOD_FIELDS.some((k) => v[k] !== value.value[k]) ? { ...v, timezone: browserTZ() } : v)
  })

  function reset(v: AccountUsageLimit, next: UsageLimitStatus) {
    value.value = v
    saved.value = fingerprint(v)
    status.value = next
    errors.value = {}
  }

  async function load() {
    const id = accountId()
    if (!id || status.value === 'loading' || status.value === 'ready') return
    const my = ++seq
    status.value = 'loading'
    try {
      const loaded = loadedUsageLimit(await fetchUsageLimit(id))
      if (my === seq) reset(loaded, 'ready')
    } catch (e) {
      if (my !== seq) return
      loadError.value = errorMessage(e)
      status.value = 'failed'
    }
  }

  watch(
    () => [accountId(), shown()] as const,
    ([id, visible]) => {
      if (id !== forId) {
        forId = id
        seq++
        reset(defaultUsageLimit(), id ? 'idle' : 'ready')
      }
      if (visible) void load()
    },
    { immediate: true }
  )

  /** Shows a value (a declared default) as the unchanged one: saving sends it only once edited. */
  function prefill(v: Partial<AccountUsageLimit>) {
    reset({ ...defaultUsageLimit(), ...v }, status.value)
  }

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
