<script setup lang="ts">
// account.detail.tabs slot (both account types): OpenAI's official Codex
// usage resets. Shows how many the account has, every credit (usable first,
// then being used and used ones, dimmed) with its status, wording and
// expiry, and the account's 5 h / weekly usage from the core's snapshot;
// "Use" spends one (the header button the soonest to expire, a row's button
// that row's), then reloads the list. The snapshot only changes with the
// account's next upstream response, so after windows were reset the usage
// is marked as awaiting that request instead of still showing "used up". A
// refusal (nothing to reset, no reset left) is a warning, not an error:
// nothing was spent.
import { computed, ref, watch } from 'vue'
import { isApiError } from '@sub2api/host'
import { SAlert, SBadge, SButton, SDateTime, SEmpty, SHint, SSkeleton, SStatusBadge, type Tone } from '@sub2api/ui'
import { errorMessage, listResetCredits, redeemResetCredit, useCodexHost, type ResetCredit, type ResetCredits } from './host'
import { allCredits, anyFull, availableCredits, unixMs, usageWindows, type QuotaLike } from './resetCredits'
import { setResetCount } from './resetCount'

const props = defineProps<{ account: { id: number; name?: string; quota?: QuotaLike | null } }>()
const host = useCodexHost()
const t = host.t
// POST /reset-credits/list and /consume act on the account (RouteAccounts
// update): without account:update at any scope both are refused, so the tab
// says so instead of failing to load. An account outside the scope still
// gets the core's 404.
const mayUpdate = host.permissions.hasAction?.('account:update') === true
const canUse = computed(() => mayUpdate && host.can('codex:reset'))

/** Refusals that spent nothing: shown as a warning with the plugin's message. */
const REFUSALS = new Set(['nothing_to_reset', 'no_credit'])
/** Credit status tones; redeemed and unknown statuses are gray (an unknown one shows as sent). */
const STATUS_TONES: Record<string, Tone> = { available: 'success', redeeming: 'info' }
const statusLabels = computed(() => ({
  available: t('reset.status.available'),
  redeeming: t('reset.status.redeeming'),
  redeemed: t('reset.status.redeemed')
}))

const data = ref<ResetCredits | null>(null)
/** Set once a reset cleared windows: the snapshot is stale until the account's next request. */
const awaitingUsage = ref(false)
const loading = ref(false)
const error = ref('')
/** The id of the credit being used, '' when idle. */
const busy = ref('')

const credits = computed(() => availableCredits(data.value))
const listed = computed(() => allCredits(data.value))
const count = computed(() => data.value?.available_count ?? 0)
const usage = computed(() => {
  const ws = usageWindows(props.account.quota)
  return awaitingUsage.value ? ws.map((w) => ({ ...w, full: false })) : ws
})
const full = computed(() => anyFull(usage.value))

async function load() {
  if (!mayUpdate) return
  const id = props.account.id
  loading.value = true
  error.value = ''
  try {
    const r = await listResetCredits(id)
    setResetCount(id, r.available_count)
    if (id === props.account.id) data.value = r
  } catch (e) {
    if (id === props.account.id) error.value = errorMessage(e, t('reset.loadFailed'))
  } finally {
    if (id === props.account.id) loading.value = false
  }
}

function confirmText(c: ResetCredit) {
  const usageText = usage.value.map((w) => `${t(`reset.window${w.key}`)} ${w.percent ?? '?'}%`).join(' · ')
  const parts = [t('reset.confirm.body', { name: props.account.name || `#${props.account.id}`, title: c.title || t('reset.defaultTitle') })]
  if (usageText) parts.push(t('reset.confirm.usage', { usage: usageText }))
  if (!full.value) parts.push(t('reset.confirm.notFull'))
  parts.push(t('reset.confirm.final'))
  return parts.join('\n')
}

async function use(c: ResetCredit) {
  const id = props.account.id
  const ok = await host.confirm({ title: t('reset.confirm.title'), message: confirmText(c), confirmText: t('reset.use'), danger: true })
  if (!ok) return
  busy.value = c.id
  try {
    const r = await redeemResetCredit(id, c.id)
    host.toast(r.account_restored ? t('reset.done', { n: r.windows_reset }) : t('reset.doneNotRestored'), r.account_restored ? 'success' : 'warning')
    if (r.windows_reset > 0 && id === props.account.id) awaitingUsage.value = true
  } catch (e) {
    const refused = isApiError(e) && REFUSALS.has(e.code)
    host.toast(errorMessage(e, t('reset.failed')), refused ? 'warning' : 'error')
  } finally {
    busy.value = ''
  }
  await load()
}

watch(
  () => props.account.id,
  () => {
    data.value = null
    awaitingUsage.value = false
    void load()
  },
  { immediate: true }
)
</script>

<template>
  <div class="cx-reset" data-testid="codex-reset-tab">
    <div class="cx-reset-head">
      <div class="cx-reset-count">
        <span class="cx-reset-num cx-num" data-testid="codex-reset-count">{{ loading && !data ? '…' : count }}</span>
        <span class="cx-muted">{{ t('reset.available') }}</span>
      </div>
      <div class="cx-reset-usage">
        <span v-for="w in usage" :key="w.key" class="cx-reset-window">
          <span class="cx-muted">{{ t(`reset.window${w.key}`) }}</span>
          <SBadge size="xs" :tone="w.full ? 'danger' : 'gray'" class="cx-num">{{ w.percent ?? '?' }}%</SBadge>
          <span v-if="w.resetsAt" class="cx-muted">
            {{ t('reset.resetsAt') }} <SDateTime :value="w.resetsAt" mode="absolute" />
          </span>
        </span>
        <span v-if="!usage.length" class="cx-muted">{{ t('reset.noUsage') }}</span>
        <span v-else-if="awaitingUsage" class="cx-muted" data-testid="codex-reset-usage-awaiting">{{ t('reset.usageAwaiting') }}</span>
      </div>
      <SButton
        v-if="canUse"
        variant="primary"
        size="sm"
        icon="refresh"
        :loading="!!credits[0] && busy === credits[0].id"
        :disabled="!credits.length || !!busy"
        data-testid="codex-reset-use-next"
        @click="use(credits[0])"
        >{{ t('reset.useNext') }}</SButton
      >
    </div>
    <SHint size="xs">{{ t('reset.hint') }}</SHint>
    <SAlert v-if="full && count" tone="warning" dense>{{ t('reset.fullHint') }}</SAlert>
    <SAlert v-if="error" tone="danger" dense>{{ error }}</SAlert>
    <SAlert v-if="!mayUpdate" tone="info" dense data-testid="codex-reset-needs-update">{{ t('reset.needsUpdate') }}</SAlert>

    <SSkeleton v-if="loading && !data" :lines="3" height="2.5rem" />
    <SEmpty v-else-if="mayUpdate && !listed.length && !error" size="sm" icon="inbox" :title="t('reset.emptyTitle')" :text="t('reset.emptyText')" />
    <ul v-else-if="listed.length" class="cx-reset-list">
      <li
        v-for="c in listed"
        :key="c.id"
        class="cx-reset-item"
        :class="{ 'cx-reset-item--spent': c.status !== 'available' }"
        data-testid="codex-reset-credit"
        :data-status="c.status"
      >
        <div class="cx-grow">
          <div class="cx-reset-title">
            {{ c.title || t('reset.defaultTitle') }}
            <SStatusBadge :status="c.status" :labels="statusLabels" :tones="STATUS_TONES" size="xs" />
          </div>
          <div class="cx-muted cx-reset-desc">{{ c.description || t('reset.defaultDescription') }}</div>
          <div class="cx-muted cx-reset-meta">
            <span v-if="c.expires_at">{{ t('reset.expires') }} <SDateTime :value="unixMs(c.expires_at)" mode="absolute" /></span>
            <span v-else>{{ t('reset.noExpiry') }}</span>
            <span v-if="c.granted_at" class="cx-reset-sep">·</span>
            <span v-if="c.granted_at">{{ t('reset.granted') }} <SDateTime :value="unixMs(c.granted_at)" mode="absolute" date-only /></span>
          </div>
        </div>
        <SButton v-if="canUse && c.status === 'available'" size="sm" :loading="busy === c.id" :disabled="!!busy" @click="use(c)">{{ t('reset.use') }}</SButton>
      </li>
    </ul>
  </div>
</template>
