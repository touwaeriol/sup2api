<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCheckbox, SField, SHint, SInput, SSegmented, SSelect } from '@sub2api/ui'
import { formatNumber } from '@/utils/format'
import type { PriceKey, TokenTier } from './priceExpr'
import { hasImages, hasSplitCache, setImages, setSplitCache } from './tokenPrice'

// One tier of a per_token price: its name, the len it covers, its unit and
// prices. `index` numbers it; `lower` is the bound the tier before it ends at.
const tier = defineModel<TokenTier>({ required: true })
const props = defineProps<{ index: number; count: number; lower: number }>()
const emit = defineEmits<{ (e: 'remove'): void }>()
const { t } = useI18n()

const isRest = computed(() => props.index === props.count - 1)
const unitOptions = computed(() => (['token', 'request'] as const).map(value => ({ value, label: t(`prices.token.unit.${value}`) })))
const cacheOptions = computed(() => [
  { value: false, label: t('prices.token.cacheOne') },
  { value: true, label: t('prices.token.cacheSplit') }
])

const FREE = 'prices.unsetPlaceholder'
const fields = computed<Array<{ key: PriceKey; label: string; placeholder: string }>>(() => {
  const split = hasSplitCache(tier.value)
  return [
    { key: 'p' as const, label: t('prices.vars.p'), placeholder: '0' },
    { key: 'c' as const, label: t('prices.vars.c'), placeholder: '0' },
    { key: 'cr' as const, label: t('prices.vars.cr'), placeholder: t(FREE) },
    { key: 'cc' as const, label: split ? t('prices.vars.cc') : t('prices.token.cacheWrite'), placeholder: t(FREE) },
    ...(split ? [{ key: 'cc1h' as const, label: t('prices.vars.cc1h'), placeholder: t(FREE) }] : []),
    ...(hasImages(tier.value)
      ? [
          { key: 'img' as const, label: t('prices.vars.img'), placeholder: t('prices.token.byInput') },
          { key: 'img_o' as const, label: t('prices.vars.img_o'), placeholder: t('prices.token.byOutput') }
        ]
      : [])
  ]
})
</script>

<template>
  <li class="rounded-xl border border-line bg-surface" :data-tier="index">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-4 py-2.5">
      <span
        class="grid h-6 w-6 place-items-center rounded-full bg-primary-50 text-xs font-semibold text-primary-700 dark:bg-primary-900 dark:text-primary-300"
        >{{ index + 1 }}</span
      >
      <SInput v-model.trim="tier.name" size="sm" mono class="w-32" :aria-label="t('prices.token.tierName')" />
      <SHint v-if="count === 1" inline size="xs">{{ t('prices.token.all') }}</SHint>
      <template v-else>
        <span class="text-sm text-fg-muted">{{ t('prices.token.context') }}</span>
        <template v-if="!isRest">
          <SHint v-if="index > 0" inline size="xs" class="font-mono">{{ formatNumber(lower) }} &lt;</SHint>
          <span class="font-mono text-sm">len ≤</span>
          <SInput v-model="tier.max_len" size="sm" type="number" min="1" class="w-32" :aria-label="t('prices.token.maxLen')" />
        </template>
        <template v-else>
          <span class="font-mono text-sm">len &gt; {{ formatNumber(lower) }}</span>
          <SHint inline size="xs">{{ t('prices.token.otherwise') }}</SHint>
        </template>
      </template>
      <div class="ml-auto flex items-center gap-2">
        <SSelect v-model="tier.unit" size="sm" class="w-32" :options="unitOptions" :aria-label="t('prices.token.unitLabel')" />
        <SButton
          v-if="count > 1"
          variant="ghost"
          size="sm"
          tone="danger"
          icon="trash"
          :aria-label="t('prices.token.removeTier')"
          @click="emit('remove')"
        />
      </div>
    </div>

    <div v-if="tier.unit === 'request'" class="px-4 py-3">
      <SField size="sm" class="w-48" :label="t('prices.perRequestPrice')">
        <SInput v-model="tier.price" type="number" min="0" step="any" :suffix="t('prices.perRequestUnit')" />
      </SField>
    </div>
    <div v-else class="space-y-2.5 px-4 py-3">
      <div class="flex flex-wrap gap-x-3 gap-y-2">
        <SField v-for="f in fields" :key="f.key" size="sm" class="w-[6.5rem]" :label="f.label">
          <SInput v-model="tier[f.key]" type="number" min="0" step="any" :placeholder="f.placeholder" :data-testid="`tier-${f.key}`" />
        </SField>
      </div>
      <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
        <span class="inline-flex items-center gap-2">
          <SHint inline size="xs">{{ t('prices.token.cacheWrite') }}</SHint>
          <SSegmented
            :model-value="hasSplitCache(tier)"
            size="sm"
            :options="cacheOptions"
            :aria-label="t('prices.token.cacheWrite')"
            data-testid="tier-cache"
            @update:model-value="setSplitCache(tier, $event === true)"
          />
        </span>
        <SCheckbox
          :model-value="hasImages(tier)"
          size="xs"
          :label="t('prices.token.images')"
          data-testid="tier-images"
          @update:model-value="setImages(tier, $event)"
        />
      </div>
    </div>
  </li>
</template>
