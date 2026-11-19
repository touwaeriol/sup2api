<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SField, SGrid, SHint, SIcon, SInput, SKeyValue, SSpinner } from '@sub2api/ui'
import type { PricePreviewResult } from '@/api/types'
import GroupPicker from '@/components/GroupPicker.vue'
import { useAuthStore } from '@/stores/auth'
import { formatMoney, formatNumber } from '@/utils/format'
import { errorMessage } from '@/utils/errors'
import { TOKEN_VARS } from './priceExpr'
import BillingBreakdown from './BillingBreakdown.vue'

// Trial calculation (POST /prices/preview) for the price being edited.
const props = defineProps<{
  /** {mode, config, expression} of the current editor state, or {price_id}. null = not previewable. */
  source: Record<string, unknown> | null
}>()
const { t } = useI18n()
const auth = useAuthStore()
const metrics = ref<Record<string, string>>({})
// The token variables of the price, and the image tokens an expression may price apart (img, img_o; CONTRACTS §118).
const TRIAL_VARS = [...TOKEN_VARS, 'img', 'img_o'] as const
function videoSample(estimated: boolean) {
  Object.assign(usage, { p: 0, c: 100000, cr: 0, cc: 0, cc1h: 0, img: 0, img_o: 0 })
  metrics.value = { video_seconds: '5', video_width: '1280', video_height: '720', video_pixels: '921600', video_input: 'false', video_estimated: String(estimated) }
}

const usage = reactive<Record<string, number | null>>({ p: 100000, c: 2000, cr: 80000, cc: 0, cc1h: 0, img: 0, img_o: 0 })
const len = ref<number | null>(null)
const headers = ref<Record<string, string>>({})
const params = ref<Record<string, string>>({})
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

async function run() {
  clearTimeout(timer)
  if (!props.source) return
  const my = ++seq
  loading.value = true
  error.value = ''
  const u: Record<string, number> = {}
  for (const k of TRIAL_VARS) u[k] = Number(usage[k]) || 0
  if (len.value !== null) u.len = len.value
  const body: Record<string, unknown> = {
    ...props.source,
    usage: u,
    metrics: Object.fromEntries(Object.entries(metrics.value).map(([k, v]) => [k, parseParam(v)])),
    headers: headers.value,
    params: Object.fromEntries(Object.entries(params.value).map(([k, v]) => [k, parseParam(v)]))
  }
  if (at.value) {
    const d = new Date(at.value)
    if (!Number.isNaN(d.getTime())) body.at = d.toISOString()
  }
  if (groupId.value) body.group_id = groupId.value
  try {
    const r = await api.post<PricePreviewResult>('/prices/preview', body)
    if (my !== seq) return
    result.value = r
  } catch (e) {
    if (my !== seq) return
    result.value = null
    error.value = errorMessage(e)
  } finally {
    if (my === seq) loading.value = false
  }
}

function schedule() {
  clearTimeout(timer)
  timer = setTimeout(run, 700)
}

watch(() => [props.source, { ...usage }, len.value, headers.value, params.value, metrics.value, at.value, groupId.value], schedule, { deep: true, immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

// Extra response fields (B): base_cost, rate_multiplier, expression, expr_hash.
const extra = computed(() => (result.value || {}) as PricePreviewResult & { base_cost?: string; rate_multiplier?: string | number })
const resultLen = computed(() => (result.value?.breakdown as { vars?: { len?: number } } | undefined)?.vars?.len ?? len.value ?? defaultLen.value)
</script>

<template>
  <div class="space-y-4">
    <SGrid :cols-base="2" :cols="4" :gap="3">
      <label v-for="k in TRIAL_VARS" :key="k" class="text-xs">
        <SHint inline size="xs">{{ t(`prices.vars.${k}`) }}</SHint>
        <SInput v-model.number="usage[k]" size="sm" type="number" min="0" class="mt-1" />
      </label>
      <label class="text-xs">
        <SHint inline size="xs">{{ t('prices.trial.len') }}</SHint>
        <SInput v-model.number="len" size="sm" type="number" min="0" class="mt-1" :placeholder="String(defaultLen)" />
      </label>
    </SGrid>
    <SGrid :cols="1" :lg-cols="2">
      <SField :label="t('prices.trial.headers')">
        <SKeyValue v-model="headers" key-placeholder="anthropic-beta" value-placeholder="fast-mode" />
      </SField>
      <SField :label="t('prices.trial.params')" :hint="t('prices.trial.paramsHint')">
        <SKeyValue v-model="params" key-placeholder="service_tier" value-placeholder="priority" />
      </SField>
    </SGrid>
    <SField :label="t('prices.video.metrics')" :hint="t('prices.video.metricsHint')">
      <div class="mb-2 flex flex-wrap gap-2">
        <SButton size="sm" @click="videoSample(true)">{{ t('prices.video.reserveSample') }}</SButton>
        <SButton size="sm" @click="videoSample(false)">{{ t('prices.video.settleSample') }}</SButton>
      </div>
      <SKeyValue v-model="metrics" key-placeholder="video_seconds" value-placeholder="5" />
    </SField>
    <div class="flex flex-wrap items-end gap-4">
      <SField :label="t('prices.trial.at')">
        <SInput v-model="at" type="datetime-local" class="w-56" />
      </SField>
      <SField v-if="auth.has('group:read')" :label="t('prices.trial.group')">
        <div class="w-64"><GroupPicker :model-value="groupId" @update:model-value="groupId = typeof $event === 'number' ? $event : null" /></div>
      </SField>
      <SButton size="sm" variant="primary" :loading="loading" :disabled="!source" @click="run">{{ t('prices.trial.calculate') }}</SButton>
    </div>

    <div class="rounded-xl border border-line p-4">
      <SHint v-if="!source">{{ t('prices.trial.fixFirst') }}</SHint>
      <SHint v-else-if="error" tone="danger">{{ error }}</SHint>
      <div v-else-if="result" class="space-y-3 text-sm">
        <div class="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <span>
            <SHint inline>{{ t('prices.trial.cost') }}</SHint>
            <span class="ml-2 text-lg font-semibold text-fg">{{ formatMoney(result.cost, 6) }}</span>
          </span>
          <span>
            <SHint inline>{{ t('prices.trial.tier') }}</SHint>
            <span class="ml-2 font-mono">{{ result.tier || '—' }}</span>
            <SHint inline class="ml-1">(len = {{ formatNumber(resultLen) }})</SHint>
          </span>
          <SSpinner v-if="loading" size="sm" />
        </div>
        <div v-if="result.rules?.length">
          <SHint class="mb-1">{{ t('prices.trial.rules') }}</SHint>
          <ul class="space-y-1">
            <li v-for="(r, i) in result.rules" :key="i" class="flex items-center gap-2">
              <SBadge :tone="r.matched ? 'success' : 'gray'" :aria-label="r.matched ? t('usage.billing.matched') : t('usage.billing.notMatched')"><SIcon :name="r.matched ? 'check' : 'x'" class="h-3 w-3" /></SBadge>
              <code class="font-mono text-xs">{{ r.cond }}</code>
              <SHint inline>×{{ r.multiplier }}</SHint>
            </li>
          </ul>
        </div>
        <div>
          <SHint class="mb-1">{{ t('prices.trial.breakdown') }}</SHint>
          <BillingBreakdown :breakdown="result.breakdown" :rate-multiplier="extra.rate_multiplier" :total="result.cost" :rules="result.rules" />
        </div>
      </div>
      <div v-else-if="loading" class="text-center"><SSpinner /></div>
      <SHint v-else>{{ t('prices.trial.empty') }}</SHint>
    </div>
  </div>
</template>
