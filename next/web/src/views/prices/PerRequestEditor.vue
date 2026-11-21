<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SInput, SLink } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import { isSet, num, type MarkupRule } from './priceExpr'
import PriceRuleGroups from './PriceRuleGroups.vue'

// The per_request mode: one price per call, common prices to pick, what it
// comes to at volume, and the request rule groups of per_token. Tiers by
// length live in per_token.
const price = defineModel<number | null>({ required: true })
const rules = defineModel<MarkupRule[]>('rules', { required: true })
defineProps<{ disabled?: boolean; error?: string }>()
const emit = defineEmits<{ (e: 'to-token'): void }>()
const { t } = useI18n()

const COMMON = [0.001, 0.005, 0.01, 0.02, 0.04, 0.1]
const VOLUMES = { thousand: 1e3, hundredThousand: 1e5, million: 1e6 }
const p = computed(() => (isSet(price.value) ? num(price.value) : null))
const volumes = computed(() => {
  const each = p.value
  return each === null ? [] : Object.entries(VOLUMES).map(([k, n]) => ({ label: t(`prices.perRequest.volume.${k}`), v: each * n }))
})
</script>

<template>
  <div class="space-y-4 rounded-xl border border-line p-3 sm:p-4" data-testid="per-request-editor">
    <div>
      <div class="mb-2 flex items-center justify-between">
        <label class="text-sm font-semibold text-fg" for="per-request-price">{{ t('prices.perRequestPrice') }}</label>
        <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">${{ t('prices.per.request') }}</span>
      </div>
      <SInput id="per-request-price" v-model="price" type="number" min="0" step="any" class="max-w-xs" placeholder="0" :disabled="disabled" />
      <p v-if="error" class="mt-1 text-xs text-danger-600">{{ error }}</p>
      <div class="mt-2 flex flex-wrap items-center gap-1.5">
        <span class="text-xs text-fg-muted">{{ t('prices.perRequest.common') }}</span>
        <button
          v-for="c in COMMON"
          :key="c"
          type="button"
          :disabled="disabled"
          class="rounded-full border px-2.5 py-0.5 font-mono text-xs"
          :class="p === c ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'border-line text-fg hover:border-primary-400'"
          @click="price = c"
        >
          ${{ c }}
        </button>
      </div>
      <p class="mt-3 text-xs text-fg-muted">
        {{ t('prices.perRequest.hint') }}
        <span v-for="x in volumes" :key="x.label" class="ml-2">{{ x.label }} <b class="font-mono text-fg">{{ formatMoney(x.v) }}</b></span>
      </p>
      <p v-if="!disabled" class="mt-1 text-xs text-fg-muted">
        {{ t('prices.perRequest.toTokenAsk') }}
        <SLink as="button" class="text-xs" data-testid="per-request-to-token" @click="emit('to-token')">{{ t('prices.perRequest.toToken') }}</SLink>
      </p>
    </div>
    <div class="border-t border-line pt-4">
      <PriceRuleGroups v-model="rules" :disabled="disabled" />
    </div>
  </div>
</template>
