<script setup lang="ts">
import { computed } from 'vue'
import { SButton, SCheckbox, SInput, SSegmented, SSelect } from '@sub2api/ui'
import { formatNumber } from '@/utils/format'
import { num, type PriceKey, type TokenTier } from './priceExpr'
import { addTier, hasImages, hasSplitCache, removeTier, setImages, setSplitCache } from './tokenPrice'

// The tiers of a per_token price as one table: a column per tier (name, the
// len it covers, its unit), a row per price. The cache-write split and image
// prices are switched for every tier at once (the config keeps them per tier;
// a mixed config shows the switch off and the cells it has).
// PROTOTYPE: new texts are Chinese literals until adopted.
const tiers = defineModel<TokenTier[]>({ required: true })
defineProps<{ disabled?: boolean }>()

const lower = (i: number) => (i === 0 ? 0 : num(tiers.value[i - 1].max_len))
const isRest = (i: number) => i === tiers.value.length - 1
const tokenTiers = computed(() => tiers.value.filter((x) => x.unit !== 'request'))
const split = computed(() => tokenTiers.value.length > 0 && tokenTiers.value.every(hasSplitCache))
const images = computed(() => tokenTiers.value.length > 0 && tokenTiers.value.every(hasImages))
const setAllSplit = (on: boolean) => tiers.value.forEach((x) => setSplitCache(x, on))
const setAllImages = (on: boolean) => tiers.value.forEach((x) => setImages(x, on))

type Row = { key: PriceKey; label: string; placeholder: string }
const rows = computed<Row[]>(() => {
  const anySplit = tiers.value.some(hasSplitCache)
  const anyImg = tiers.value.some(hasImages)
  return [
    { key: 'p', label: '输入', placeholder: '0' },
    { key: 'c', label: '输出', placeholder: '0' },
    { key: 'cr', label: '缓存读', placeholder: '不收费' },
    { key: 'cc', label: anySplit ? '缓存写 5 分钟' : '缓存写', placeholder: '不收费' },
    ...(anySplit ? [{ key: 'cc1h' as const, label: '缓存写 1 小时', placeholder: '不收费' }] : []),
    ...(anyImg
      ? [
          { key: 'img' as const, label: '图片输入', placeholder: '按输入价' },
          { key: 'img_o' as const, label: '图片输出', placeholder: '按输出价' }
        ]
      : [])
  ]
})
const has = (tier: TokenTier, k: PriceKey) => k in tier || k === 'p' || k === 'c' || k === 'cr' || k === 'cc'

const unitOptions = [
  { value: 'token', label: '按 token' },
  { value: 'request', label: '按次' }
]
const cacheOptions = [
  { value: false, label: '一种价' },
  { value: true, label: '分 5 分钟 / 1 小时' }
]

// The len ruler above the table: one segment per tier, widths by log scale so
// 32K and 1M tiers both stay readable; the rest gets a fixed share.
const short = (n: number) => (n >= 1e6 ? `${+(n / 1e6).toFixed(2)}M` : n >= 1e3 ? `${+(n / 1e3).toFixed(1)}K` : String(n))
const segments = computed(() => {
  const bounds = tiers.value.map((x, i) => (isRest(i) ? null : Math.max(num(x.max_len), 1)))
  const logs = bounds.map((b, i) => (b === null ? 0 : Math.log10(b) - (i === 0 ? 3 : Math.log10(Math.max(num(bounds[i - 1]), 1)))))
  const total = logs.reduce((s, x) => s + Math.max(x, 0.3), 0)
  return tiers.value.map((x, i) => ({
    name: x.name,
    range: tiers.value.length === 1 ? '所有长度' : isRest(i) ? `> ${short(lower(i))}` : `${i === 0 ? '0' : short(lower(i))} – ${short(num(x.max_len))}`,
    grow: isRest(i) ? (tiers.value.length === 1 ? 1 : Math.max(total * 0.35, 0.6)) : Math.max(logs[i], 0.3)
  }))
})
const TONES = ['bg-primary-500', 'bg-info-500', 'bg-accent-500', 'bg-warning-500', 'bg-success-600']
</script>

<template>
  <div class="min-w-0" data-testid="token-tier-table">
    <!-- whole-price switches -->
    <div class="mb-3 flex flex-wrap items-center gap-x-5 gap-y-2">
      <span class="inline-flex items-center gap-2">
        <span class="text-xs text-fg-muted">缓存写</span>
        <SSegmented :model-value="split" size="sm" :options="cacheOptions" :disabled="disabled" aria-label="缓存写" @update:model-value="setAllSplit($event === true)" />
      </span>
      <SCheckbox :model-value="images" size="xs" label="图片单独计价" :disabled="disabled" @update:model-value="setAllImages($event)" />
      <span class="ml-auto text-xs text-fg-subtle">单位：美元 / 百万 token</span>
    </div>

    <!-- len ruler -->
    <div class="mb-3" aria-hidden="true">
      <div class="flex h-2 overflow-hidden rounded-full">
        <div v-for="(s, i) in segments" :key="i" :class="TONES[i % TONES.length]" class="border-r-2 border-surface last:border-r-0" :style="{ flexGrow: s.grow, flexBasis: 0 }" />
      </div>
      <div class="mt-1 flex text-[11px] leading-4">
        <div v-for="(s, i) in segments" :key="i" class="min-w-0 truncate pr-2" :style="{ flexGrow: s.grow, flexBasis: 0 }">
          <span class="font-mono text-fg">{{ s.name }}</span> <span class="text-fg-muted">{{ s.range }}</span>
        </div>
      </div>
    </div>

    <div class="overflow-x-auto rounded-xl border border-line">
      <table class="w-full min-w-[34rem] table-fixed border-collapse text-sm">
        <thead>
          <tr class="bg-surface-2 align-top">
            <th class="w-32 px-3 py-2.5 text-left text-xs font-medium text-fg-muted">档位</th>
            <th v-for="(tier, i) in tiers" :key="i" class="border-l border-line px-3 py-2.5 text-left font-normal" :data-tier="i">
              <div class="flex items-center gap-1.5">
                <span class="h-2.5 w-2.5 shrink-0 rounded-full" :class="TONES[i % TONES.length]" />
                <SInput v-model.trim="tier.name" size="sm" mono class="min-w-0 flex-1" aria-label="档位名称" />
                <SButton v-if="tiers.length > 1" variant="ghost" size="sm" tone="danger" icon="trash" aria-label="删除档位" @click="removeTier(tiers, i)" />
              </div>
              <div class="mt-1.5 flex h-8 items-center gap-1.5 text-xs text-fg-muted">
                <template v-if="tiers.length === 1">所有请求</template>
                <template v-else-if="!isRest(i)">
                  <span class="whitespace-nowrap font-mono">len ≤</span>
                  <SInput v-model="tier.max_len" size="sm" type="number" min="1" class="min-w-0 flex-1" aria-label="上下文长度上限" />
                </template>
                <template v-else>
                  <span class="font-mono">len &gt; {{ formatNumber(lower(i)) }}</span>
                </template>
              </div>
              <SSelect v-model="tier.unit" size="sm" class="mt-1.5 w-full" :options="unitOptions" aria-label="这一档的计价方式" />
            </th>
            <th class="w-24 border-l border-line px-2 py-2.5 align-middle">
              <SButton size="sm" variant="ghost" icon="plus" class="w-full" :disabled="disabled" data-testid="token-add-tier" @click="addTier(tiers)">档位</SButton>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, ri) in rows" :key="r.key" class="border-t border-line">
            <th class="whitespace-nowrap px-3 py-1.5 text-left text-xs font-normal text-fg-muted">{{ r.label }}</th>
            <template v-for="(tier, i) in tiers" :key="i">
              <td v-if="tier.unit === 'request'" v-show="ri === 0" :rowspan="rows.length" class="border-l border-line bg-surface-2/50 px-3 py-2 align-middle">
                <div class="mb-1 text-xs text-fg-muted">每次价格</div>
                <SInput v-model="tier.price" size="sm" type="number" min="0" step="any" suffix="美元 / 次" />
                <div class="mt-1 text-[11px] text-fg-subtle">不看 token 数</div>
              </td>
              <td v-else class="border-l border-line px-3 py-1.5">
                <SInput
                  v-if="has(tier, r.key)"
                  v-model="tier[r.key]"
                  size="sm"
                  type="number"
                  min="0"
                  step="any"
                  :placeholder="r.placeholder"
                  :data-testid="`tier-${r.key}`"
                />
                <span v-else class="text-xs text-fg-subtle">—</span>
              </td>
            </template>
            <td class="border-l border-line" />
          </tr>
        </tbody>
      </table>
    </div>
    <p class="mt-2 text-xs text-fg-muted">
      len = 输入 + 缓存读写 + 图片输入。从左到右匹配，第一个满足的档位按整单计价；空着的缓存价表示不收费，空着的图片价按输入、输出价算。
    </p>
  </div>
</template>
