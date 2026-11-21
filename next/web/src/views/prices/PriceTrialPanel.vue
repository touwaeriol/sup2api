<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SDisclosure, SField, SGrid, SHint, SIcon, SInput, SKeyValue, SSpinner } from '@sub2api/ui'
import type { PricePreviewResult } from '@/api/types'
import GroupPicker from '@/components/GroupPicker.vue'
import { useAuthStore } from '@/stores/auth'
import { formatMoney, formatNumber } from '@/utils/format'
import { errorMessage } from '@/utils/errors'
import { PRICE_KEYS } from './priceExpr'
import BillingBreakdown from './BillingBreakdown.vue'

// Trial calculation (POST /prices/preview) for the price being edited,
// recalculated 700 ms after each change. The request context (headers,
// parameters, usage fields, time, group) sits behind a disclosure.
const props = defineProps<{
  /** {mode, config, expression} of the current editor state, or {price_id}. null = not previewable. */
  source: Record<string, unknown> | null
}>()
const { t } = useI18n()
const auth = useAuthStore()

const usage = reactive<Record<string, number | null>>({ p: 100000, c: 2000, cr: 80000, cc: 0, cc1h: 0, img: 0, img_o: 0 })
const len = ref<number | null>(null)
const headers = ref<Record<string, string>>({})
const params = ref<Record<string, string>>({})
const metrics = ref<Record<string, string>>({})
const at = ref('')
const groupId = ref<number | null>(null)

const result = ref<PricePreviewResult | null>(null)
const error = ref('')
const loading = ref(false)
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

const defaultLen = computed(() => ['p', 'cr', 'cc', 'cc1h', 'img'].reduce((s, k) => s + (Number(usage[k]) || 0), 0))

function parseParam(v: string): unknown {
  try {
    return JSON.parse(v)
  } catch {
    return v
  }
}
const parsed = (m: Record<string, string>) => Object.fromEntries(Object.entries(m).map(([k, v]) => [k, parseParam(v)]))

async function run() {
  clearTimeout(timer)
  if (!props.source) return
  const my = ++seq
  loading.value = true
  error.value = ''
  const u: Record<string, number> = Object.fromEntries(PRICE_KEYS.map(k => [k, Number(usage[k]) || 0]))
  if (len.value !== null) u.len = len.value
  const body: Record<string, unknown> = { ...props.source, usage: u, metrics: parsed(metrics.value), headers: headers.value, params: parsed(params.value) }
  const when = at.value ? new Date(at.value) : null
  if (when && !Number.isNaN(when.getTime())) body.at = when.toISOString()
  if (groupId.value) body.group_id = groupId.value
  try {
    const r = await api.post<PricePreviewResult>('/prices/preview', body)
    if (my === seq) result.value = r
  } catch (e) {
    if (my !== seq) return
    result.value = null
    error.value = errorMessage(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

watch(
  () => [props.source, { ...usage }, len.value, headers.value, params.value, metrics.value, at.value, groupId.value],
  () => {
    clearTimeout(timer)
    timer = setTimeout(run, 700)
  },
  { deep: true, immediate: true }
)
onBeforeUnmount(() => clearTimeout(timer))

// Extra response fields: base_cost, rate_multiplier.
const extra = computed(() => (result.value || {}) as PricePreviewResult & { rate_multiplier?: string | number })
const resultLen = computed(() => (result.value?.breakdown as { vars?: { len?: number } } | undefined)?.vars?.len ?? len.value ?? defaultLen.value)
</script>

<template>
  <div class="space-y-4">
    <SGrid :cols-base="2" :cols="2" :gap="3">
      <SField v-for="k in PRICE_KEYS" :key="k" size="sm" :label="t(`prices.vars.${k}`)">
        <SInput v-model="usage[k]" type="number" min="0" />
      </SField>
      <SField size="sm" :label="t('prices.trial.len')">
        <SInput v-model="len" type="number" min="0" :placeholder="String(defaultLen)" />
      </SField>
    </SGrid>
    <SDisclosure :label="t('prices.trial.more')">
      <div class="mt-3 space-y-3">
        <SField :label="t('prices.trial.headers')">
          <SKeyValue v-model="headers" key-placeholder="anthropic-beta" value-placeholder="fast-mode" />
        </SField>
        <SField :label="t('prices.trial.params')" :hint="t('prices.trial.paramsHint')">
          <SKeyValue v-model="params" key-placeholder="service_tier" value-placeholder="priority" />
        </SField>
        <SField :label="t('prices.trial.metrics')" :hint="t('prices.trial.paramsHint')">
          <SKeyValue v-model="metrics" key-placeholder="service_tier" value-placeholder="priority" />
        </SField>
        <SField :label="t('prices.trial.at')">
          <SInput v-model="at" type="datetime-local" />
        </SField>
        <SField v-if="auth.has('group:read')" :label="t('prices.trial.group')">
          <GroupPicker :model-value="groupId" @update:model-value="groupId = typeof $event === 'number' ? $event : null" />
        </SField>
      </div>
    </SDisclosure>

    <div class="rounded-xl bg-surface-2 p-4 ring-1 ring-line" data-testid="price-trial-result">
      <SHint v-if="!source">{{ t('prices.trial.fixFirst') }}</SHint>
      <SHint v-else-if="error" tone="danger">{{ error }}</SHint>
      <div v-else-if="result" class="space-y-3 text-sm">
        <div class="flex items-baseline justify-between gap-3">
          <span class="text-2xl font-semibold text-fg">{{ formatMoney(result.cost, 6) }}</span>
          <span class="text-right">
            <SHint inline size="xs">{{ t('prices.trial.tier') }}</SHint>
            <span class="ml-1 font-mono text-xs">{{ result.tier || '—' }}</span>
            <SHint size="xs">len = {{ formatNumber(resultLen) }}</SHint>
          </span>
        </div>
        <ul v-if="result.rules?.length" class="space-y-1">
          <li v-for="(r, i) in result.rules" :key="i" class="flex items-center gap-2">
            <SBadge :tone="r.matched ? 'success' : 'gray'" :aria-label="r.matched ? t('usage.billing.matched') : t('usage.billing.notMatched')">
              <SIcon :name="r.matched ? 'check' : 'x'" class="h-3 w-3" />
            </SBadge>
            <code class="min-w-0 truncate font-mono text-xs" :title="r.cond">{{ r.cond }}</code>
            <SHint inline>×{{ r.multiplier }}</SHint>
          </li>
        </ul>
        <BillingBreakdown :breakdown="result.breakdown" :rate-multiplier="extra.rate_multiplier" :total="result.cost" :rules="result.rules" />
      </div>
      <div v-else-if="loading" class="text-center"><SSpinner /></div>
      <SHint v-else>{{ t('prices.trial.empty') }}</SHint>
    </div>
  </div>
</template>
