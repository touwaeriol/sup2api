<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { api } from '@sub2api/host'
import { SField, SIcon, SInput, SKeyValue, SSpinner } from '@sub2api/ui'
import type { PricePreviewResult } from '@/api/types'
import { formatMoney, formatNumber } from '@/utils/format'
import { errorMessage } from '@/utils/errors'
import { PRICE_KEYS } from './priceExpr'
import BillingBreakdown from './BillingBreakdown.vue'

// Token 估算器: token counts in, the cost of the price being edited out
// (POST /prices/preview, 600 ms after each change), with the tier it hit and
// the rule groups that matched. 请求模拟 adds request parameters, headers,
// usage fields and a time, for the rules that read them.
// PROTOTYPE: new texts are Chinese literals until adopted.
const props = defineProps<{ source: Record<string, unknown> | null }>()

const usage = reactive<Record<string, number | null>>({ p: 100000, c: 2000, cr: 0, cc: 0, cc1h: 0, img: 0, img_o: 0 })
const len = ref<number | null>(null)
const moreOpen = ref(false)
const simOpen = ref(false)
const detailOpen = ref(false)
const headers = ref<Record<string, string>>({})
const params = ref<Record<string, string>>({})
const metrics = ref<Record<string, string>>({})
const at = ref('')

const result = ref<PricePreviewResult | null>(null)
const error = ref('')
const loading = ref(false)
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

const defaultLen = computed(() => ['p', 'cr', 'cc', 'cc1h', 'img'].reduce((s, k) => s + (Number(usage[k]) || 0), 0))
const parseValue = (v: string): unknown => {
  try {
    return JSON.parse(v)
  } catch {
    return v
  }
}
const parsed = (m: Record<string, string>) => Object.fromEntries(Object.entries(m).map(([k, v]) => [k, parseValue(v)]))

async function run() {
  if (!props.source) return
  const my = ++seq
  loading.value = true
  error.value = ''
  const u: Record<string, number> = Object.fromEntries(PRICE_KEYS.map(k => [k, Number(usage[k]) || 0]))
  if (len.value !== null && String(len.value) !== '') u.len = Number(len.value)
  const body: Record<string, unknown> = { ...props.source, usage: u, metrics: parsed(metrics.value), headers: headers.value, params: parsed(params.value) }
  const when = at.value ? new Date(at.value) : null
  if (when && !Number.isNaN(when.getTime())) body.at = when.toISOString()
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
  () => [props.source, { ...usage }, len.value, headers.value, params.value, metrics.value, at.value],
  () => {
    clearTimeout(timer)
    timer = setTimeout(run, 600)
  },
  { deep: true, immediate: true }
)
onBeforeUnmount(() => clearTimeout(timer))

const matched = computed(() => (result.value?.rules || []).filter(r => r.matched))
const MORE = [
  { k: 'cc1h', label: '缓存写入（1 小时）' },
  { k: 'img', label: '图片输入' },
  { k: 'img_o', label: '图片输出' }
]
</script>

<template>
  <section class="rounded-xl border border-line p-3 sm:p-4" data-testid="price-estimator">
    <div class="text-sm font-medium text-fg">Token 估算器</div>
    <p class="mt-0.5 text-xs text-fg-muted">输入 token 数量预览费用（不含分组倍率）。</p>

    <div class="mt-3 grid grid-cols-2 gap-x-3 gap-y-2.5">
      <SField size="sm" label="输入 token"><SInput v-model="usage.p" size="sm" type="number" min="0" /></SField>
      <SField size="sm" label="输出 token"><SInput v-model="usage.c" size="sm" type="number" min="0" /></SField>
      <SField size="sm" label="缓存读取"><SInput v-model="usage.cr" size="sm" type="number" min="0" /></SField>
      <SField size="sm" label="缓存写入"><SInput v-model="usage.cc" size="sm" type="number" min="0" /></SField>
      <template v-if="moreOpen">
        <SField v-for="m in MORE" :key="m.k" size="sm" :label="m.label"><SInput v-model="usage[m.k]" size="sm" type="number" min="0" /></SField>
      </template>
    </div>
    <button type="button" class="mt-2 text-xs text-fg-muted hover:text-fg" @click="moreOpen = !moreOpen">{{ moreOpen ? '收起' : '更多用量（1 小时缓存、图片）' }}</button>

    <SField size="sm" label="指定输入上下文总长度" class="mt-2.5">
      <SInput v-model="len" size="sm" type="number" min="0" :placeholder="`使用现有 token 总长度（${formatNumber(defaultLen)}）`" />
    </SField>

    <div class="mt-3 rounded-lg border border-info-200 bg-info-50/70 px-3 py-2.5 dark:border-info-900 dark:bg-info-950/30" data-testid="price-trial-result">
      <div v-if="!source" class="text-xs text-fg-muted">先修好上面的问题再估算。</div>
      <div v-else-if="error" class="text-xs text-danger-700 dark:text-danger-300">{{ error }}</div>
      <template v-else>
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="text-sm text-fg">预计成本：<b class="font-mono text-base">{{ result ? formatMoney(result.cost, 6) : '—' }}</b></span>
          <span v-if="result?.tier" class="rounded-md bg-surface/80 px-1.5 py-0.5 text-xs text-fg-muted ring-1 ring-line">命中档位：<span class="font-mono text-fg">{{ result.tier }}</span></span>
          <span v-for="(r, i) in matched" :key="i" class="rounded-md bg-surface/80 px-1.5 py-0.5 text-xs text-fg-muted ring-1 ring-line">规则 ×{{ r.multiplier }}</span>
          <SSpinner v-if="loading" size="sm" class="ml-auto" />
          <button v-else-if="result" type="button" class="ml-auto text-xs text-fg-muted hover:text-fg" @click="detailOpen = !detailOpen">{{ detailOpen ? '收起明细' : '明细' }}</button>
        </div>
        <BillingBreakdown v-if="result && detailOpen" class="mt-2" :breakdown="result.breakdown" :total="result.cost" :rules="result.rules" />
      </template>
    </div>

    <div class="mt-3 rounded-lg border border-line">
      <button type="button" class="flex w-full items-center gap-1.5 px-3 py-2 text-left text-sm text-fg" :aria-expanded="simOpen" @click="simOpen = !simOpen">
        <SIcon :name="simOpen ? 'chevron-down' : 'chevron-right'" class="h-3.5 w-3.5 text-fg-muted" />请求模拟
        <span class="text-xs text-fg-muted">请求体参数、请求头、用量字段、时间</span>
      </button>
      <div v-if="simOpen" class="space-y-3 border-t border-line px-3 py-3">
        <SField size="sm" label="请求体参数" hint="值按 JSON 读，读不了按文本"><SKeyValue v-model="params" key-placeholder="service_tier" value-placeholder="priority" /></SField>
        <SField size="sm" label="请求头"><SKeyValue v-model="headers" key-placeholder="anthropic-beta" value-placeholder="fast-mode" /></SField>
        <SField size="sm" label="用量字段"><SKeyValue v-model="metrics" key-placeholder="service_tier" value-placeholder="priority" /></SField>
        <SField size="sm" label="请求时间" hint="空着按现在"><SInput v-model="at" size="sm" type="datetime-local" /></SField>
      </div>
    </div>
  </section>
</template>
