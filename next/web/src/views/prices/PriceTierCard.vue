<script setup lang="ts">
import { computed, ref } from 'vue'
import { SButton, SCheckbox, SDropdown, SIcon, SInput, SSelect } from '@sub2api/ui'
import { formatNumber } from '@/utils/format'
import { isSet, num, type PriceKey, type TokenTier } from './priceExpr'

// One tier of a per_token price as a numbered card: the header reads back
// when it matches and what it charges; the body edits the len bound, the
// name, the unit and the prices. A price is on when its box is ticked (the
// key is in the tier); a ticked price left blank is not charged.
// PROTOTYPE: new texts are Chinese literals until adopted.
const tier = defineModel<TokenTier>({ required: true })
const props = defineProps<{ index: number; count: number; lower: number; disabled?: boolean }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ (e: 'remove'): void }>()

const isRest = computed(() => props.index === props.count - 1)
const only = computed(() => props.count === 1)

const condition = computed(() => {
  if (only.value) return '始终匹配（只有这一个档位）'
  if (isRest.value) return `前面的档位都没命中（len > ${formatNumber(props.lower)}）`
  const max = isSet(tier.value.max_len) ? formatNumber(num(tier.value.max_len)) : '…'
  return props.index === 0 ? `完整输入长度 len ≤ ${max}` : `len > ${formatNumber(props.lower)} 且 ≤ ${max}`
})

const LABEL: Record<PriceKey, string> = { p: '输入', c: '输出', cr: '缓存读', cc: '缓存写', cc1h: '缓存写 1h', img: '图片输入', img_o: '图片输出' }
const usd = (v: unknown) => `$${+num(v).toFixed(6)}`
const summary = computed(() => {
  const t = tier.value
  if (t.unit === 'request') return [{ k: '每次', v: usd(t.price) }]
  return (Object.keys(LABEL) as PriceKey[]).filter(k => k === 'p' || k === 'c' || isSet(t[k])).map(k => ({ k: LABEL[k], v: usd(t[k]) }))
})

const unitOptions = [
  { value: 'token', label: '按 token' },
  { value: 'request', label: '按次' }
]
const menu = computed(() => [{ key: 'remove', label: '删除这个档位', tone: 'danger' as const, disabled: only.value }])
function onMenu(key: string) {
  if (key === 'remove') emit('remove')
}

type Field = { key: PriceKey; label: string; fixed?: boolean; hint?: string }
const TOKEN_ROWS: Field[][] = [
  [
    { key: 'p', label: '输入价格', fixed: true },
    { key: 'c', label: '输出价格', fixed: true }
  ],
  [
    { key: 'cr', label: '缓存读取价格' },
    { key: 'cc', label: '缓存写入价格' },
    { key: 'cc1h', label: '1 小时缓存写入价格', hint: '不勾选按缓存写入价' }
  ]
]
const MEDIA_FIELDS: Field[] = [
  { key: 'img', label: '图片输入价格', hint: '不勾选按输入价' },
  { key: 'img_o', label: '图片输出价格', hint: '不勾选按输出价' }
]
const on = (k: PriceKey) => k === 'p' || k === 'c' || k in tier.value
/** Input and output are always charged: their boxes stay ticked. */
function toggle(f: Field, v: boolean) {
  if (f.fixed) return
  if (v) tier.value[f.key] ??= null
  else delete tier.value[f.key]
}
const mediaOpen = ref(MEDIA_FIELDS.some(f => f.key in tier.value))
</script>

<template>
  <li
    class="rounded-xl border bg-surface transition"
    :class="open ? 'border-primary-300 ring-1 ring-primary-200 dark:border-primary-700 dark:ring-primary-900' : 'border-line'"
    :data-tier="index"
  >
    <!-- header: what it matches and charges -->
    <div class="flex items-start gap-3 px-4 py-3">
      <span class="mt-0.5 grid h-5 min-w-5 place-items-center rounded-full bg-surface-2 px-1.5 text-[11px] font-medium text-fg-muted ring-1 ring-line">{{ index + 1 }}</span>
      <button type="button" class="min-w-0 flex-1 text-left" :aria-expanded="open" @click="open = !open">
        <span class="flex items-center gap-2">
          <span class="font-mono text-sm text-fg">{{ tier.name || '未命名' }}</span>
          <span v-if="isRest && !only" class="rounded-md bg-surface-2 px-1.5 py-px text-[11px] text-fg-muted ring-1 ring-line">兜底档位</span>
        </span>
        <span class="mt-0.5 block text-sm text-fg-muted">{{ condition }}</span>
        <span class="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-fg-muted">
          <span v-for="s in summary" :key="s.k">{{ s.k }}: <b class="font-medium text-fg">{{ s.v }}</b></span>
          <span class="text-fg-subtle">{{ tier.unit === 'request' ? '/次' : '/1M token' }}</span>
        </span>
      </button>
      <div class="flex items-center gap-1">
        <SButton variant="ghost" size="sm" :aria-label="open ? '收起' : '展开'" @click="open = !open">
          <SIcon name="chevron-down" class="h-4 w-4 transition" :class="open ? 'rotate-180' : ''" />
        </SButton>
        <SDropdown v-if="!disabled" :actions="menu" width="10rem" @select="onMenu">
          <template #trigger="{ toggle: t, attrs }">
            <SButton variant="ghost" size="sm" v-bind="attrs" aria-label="更多" @click.stop="t"><SIcon name="more" class="h-4 w-4" /></SButton>
          </template>
        </SDropdown>
      </div>
    </div>

    <!-- body -->
    <div v-if="open" class="space-y-4 border-t border-line px-4 pb-4 pt-3">
      <div>
        <div class="mb-1.5 text-sm font-medium text-fg">阶梯条件</div>
        <div v-if="only || isRest" class="rounded-lg bg-surface-2 px-3 py-2 text-xs text-fg-muted">
          {{ only ? '只有一个档位，所有请求都按它算。加档位后，这一档变成兜底。' : '兜底：前面的档位都没命中时按这一档算，不用设条件。' }}
        </div>
        <div v-else class="flex flex-wrap items-center gap-2 rounded-lg border border-line px-3 py-2">
          <span class="rounded-md bg-surface-2 px-2 py-1 text-sm text-fg ring-1 ring-line">完整输入长度 <span class="font-mono text-fg-muted">len</span></span>
          <span class="rounded-md bg-surface-2 px-2 py-1 font-mono text-sm text-fg ring-1 ring-line">&lt;=</span>
          <SInput v-model="tier.max_len" size="sm" type="number" min="1" class="w-36" aria-label="上下文长度上限" />
          <span class="text-xs text-fg-subtle">len = 输入 + 缓存读写 + 图片输入</span>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <span class="rounded-md bg-primary-50 px-2 py-1 text-xs text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">档位</span>
        <SInput v-model.trim="tier.name" size="sm" mono class="w-44" aria-label="档位名称" />
        <SSelect v-model="tier.unit" size="sm" class="ml-auto w-36" :options="unitOptions" aria-label="这一档的计价方式" />
      </div>

      <div v-if="tier.unit === 'request'">
        <div class="mb-2 flex items-center justify-between">
          <span class="text-sm font-semibold text-fg">每次价格</span>
          <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">$/次</span>
        </div>
        <SInput v-model="tier.price" size="sm" type="number" min="0" step="any" class="w-44" placeholder="0" aria-label="每次价格" />
        <p class="mt-1 text-xs text-fg-subtle">命中这一档的请求按次收费，不看 token 数。</p>
      </div>
      <div v-else>
        <div class="mb-2 flex items-center justify-between">
          <span class="text-sm font-semibold text-fg">Token 价格</span>
          <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">$/1M token</span>
        </div>
        <div v-for="(row, ri) in TOKEN_ROWS" :key="ri" class="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3" :class="ri ? 'mt-3' : ''">
          <div v-for="f in row" :key="f.key" class="min-w-0">
            <SCheckbox :model-value="on(f.key)" size="xs" :label="f.label" :disabled="disabled" class="mb-1" @update:model-value="toggle(f, $event)" />
            <SInput v-model="tier[f.key]" size="sm" type="number" min="0" step="any" :disabled="!on(f.key) || disabled" :placeholder="on(f.key) ? '0' : f.hint || '不收费'" :data-testid="`tier-${f.key}`" />
          </div>
        </div>
        <button type="button" class="mt-3 inline-flex items-center gap-1 text-xs text-fg-muted hover:text-fg" :aria-expanded="mediaOpen" @click="mediaOpen = !mediaOpen">
          <SIcon :name="mediaOpen ? 'chevron-down' : 'chevron-right'" class="h-3.5 w-3.5" />媒体定价
        </button>
        <div v-if="mediaOpen" class="mt-2 grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
          <div v-for="f in MEDIA_FIELDS" :key="f.key" class="min-w-0">
            <SCheckbox :model-value="on(f.key)" size="xs" :label="f.label" :disabled="disabled" class="mb-1" @update:model-value="toggle(f, $event)" />
            <SInput v-model="tier[f.key]" size="sm" type="number" min="0" step="any" :disabled="!on(f.key) || disabled" :placeholder="on(f.key) ? '0' : f.hint" :data-testid="`tier-${f.key}`" />
          </div>
        </div>
      </div>
    </div>
  </li>
</template>
