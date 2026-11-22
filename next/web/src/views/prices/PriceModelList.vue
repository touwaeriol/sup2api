<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { SButton, SInput, SPagination, SSelect, SSpinner } from '@sub2api/ui'
import type { Price } from '@/api/types'
import { priceExtras } from '@/components/pricing/pricingFormat'
import { priceLabel } from '@/components/pricing/priceLabels'
import { useList } from '@/composables/useList'
import { PRICE_MODES } from './priceExpr'
import { priceListState as kept } from './priceListState'

// The prices beside the editor: search, mode filter and one row per model,
// the one being edited marked. Picking a row opens it in the editor, which
// builds the page anew: the search, filter and page are kept (priceListState).
const props = defineProps<{ current: number | null; canManage: boolean }>()
const router = useRouter()
const { t } = useI18n()

const { items, loading, page, pageSize, total, filters, reload } = useList<Price>('/prices', { q: kept.q, mode: kept.mode }, { pageSize: 20, immediate: kept.page === 1 })
// A kept page past the first loads as it is set.
page.value = kept.page
watch([() => filters.q, () => filters.mode, page], ([q, mode, p]) => Object.assign(kept, { q, mode, page: p }))
const modeOptions = computed(() => [{ value: '', label: t('common.all') }, ...PRICE_MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`) }))])

/** A price at a glance: "$3 / $15 · 2 tiers · 1 rule", or "$0.01 / request · 1 rule". */
const brief = (p: Price) => [priceLabel(t, p), priceExtras(t, p)].filter(Boolean).join(' · ')
const rows = computed(() => items.value.map(p => ({ p, brief: brief(p) })))

defineExpose({ reload })
</script>

<template>
  <aside class="flex min-h-0 flex-col rounded-xl border border-line bg-surface" data-testid="price-model-list">
    <div class="space-y-2 border-b border-line p-3">
      <SInput v-model="filters.q" size="sm" icon="search" :placeholder="t('prices.sync.searchPlaceholder')" :aria-label="t('common.search')" />
      <div class="flex items-center gap-2">
        <SSelect v-model="filters.mode" size="sm" class="min-w-0 flex-1" :options="modeOptions" :aria-label="t('prices.billingMode')" />
        <SButton v-if="canManage" size="sm" variant="primary" icon="plus" @click="router.push('/prices/new')">{{ t('prices.new') }}</SButton>
      </div>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto">
      <SSpinner v-if="loading && !items.length" block />
      <ul v-else>
        <li v-for="r in rows" :key="r.p.id">
          <button
            type="button"
            class="flex w-full items-center gap-2 border-b border-line/70 px-3 py-2.5 text-left transition hover:bg-surface-2"
            :class="r.p.id === props.current ? 'bg-primary-50 dark:bg-primary-900/30' : ''"
            :aria-current="r.p.id === props.current ? 'page' : undefined"
            @click="router.push(`/prices/${r.p.id}`)"
          >
            <span class="min-w-0 flex-1">
              <span
                class="block truncate font-mono text-[13px]"
                :class="r.p.id === props.current ? 'font-medium text-primary-700 dark:text-primary-300' : 'text-fg'"
                >{{ r.p.model }}</span
              >
              <span class="block truncate text-[11px] text-fg-muted">{{ r.brief }}</span>
            </span>
            <span v-if="!r.p.enabled" class="shrink-0 rounded bg-surface-2 px-1.5 text-[10px] text-fg-subtle">{{ t('common.disabled') }}</span>
          </button>
        </li>
        <li v-if="!rows.length" class="px-3 py-8 text-center text-xs text-fg-muted">{{ t('ui.noResults') }}</li>
      </ul>
    </div>
    <div class="border-t border-line px-2 py-2">
      <SPagination v-model:page="page" v-model:page-size="pageSize" :total="total" hide-sizes />
    </div>
  </aside>
</template>
