<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SCard, SEmpty, SGrid, SStatCard } from '@sub2api/ui'
import { useCatalog } from '../catalog'
import { money, num } from '../format'
import { useModHost } from '../host'
import type { CostRef, CostSummary, Status } from '../types'

// Today's cost (new-api cost-card): total, by source (moderation / tests,
// try-outs and simulations), top channels and auditors.
// Unpriced calls are counted but not in the amount.
const props = defineProps<{ costs: Status['costs'] }>()
const t = useModHost().t
const catalog = useCatalog()

const TOP = 5
const amount = (s: CostSummary) => Number(s.cost) || 0
const top = (rows: CostRef[], name: (id: number) => string) =>
  rows
    .filter((r) => r.cost.calls > 0)
    .sort((a, b) => amount(b.cost) - amount(a.cost) || b.cost.unpriced_calls - a.cost.unpriced_calls)
    .slice(0, TOP)
    .map((r) => ({ id: r.id, name: name(r.id), cost: r.cost }))

const sources = computed(() => (['audit', 'manual'] as const).map((k) => ({ key: k, cost: props.costs[k] })))
const lists = computed(() => [
  { key: 'byChannel', rows: top(props.costs.channels, catalog.channelName) },
  { key: 'byAuditor', rows: top(props.costs.auditors, catalog.auditorName) }
])
const total = computed(() => props.costs.total)
</script>

<template>
  <SCard :title="t('todayCost.title')" :subtitle="t('cost.hint')">
    <SEmpty v-if="!total.calls" :text="t('todayCost.empty')" icon="price" />
    <SGrid v-else template="repeat(auto-fit, minmax(220px, 1fr))">
      <div class="mod-stack">
        <SStatCard compact :label="t('cost.total')" :value="money(total.cost)" :sub="t('cost.calls', { n: num(total.calls) })" />
        <SAlert v-if="total.unpriced_calls" tone="warning" dense>{{ t('todayCost.unpriced', { n: num(total.unpriced_calls) }) }}</SAlert>
        <ul class="mod-scores">
          <li v-for="s in sources" :key="s.key">
            <span>{{ t(`todayCost.source.${s.key}`) }} <span class="mod-sub mod-num">{{ t('cost.calls', { n: num(s.cost.calls) }) }}</span></span>
            <span class="mod-num">{{ money(s.cost.cost) }}</span>
          </li>
        </ul>
      </div>
      <div v-for="l in lists" :key="l.key">
        <p class="mod-cost-label mod-cost-label-top">{{ t(`todayCost.${l.key}`) }}</p>
        <span v-if="!l.rows.length" class="mod-sub">{{ t('todayCost.none') }}</span>
        <ul v-else class="mod-scores">
          <li v-for="r in l.rows" :key="r.id">
            <span class="mod-ellipsis" :title="r.name">{{ r.name }}</span>
            <span class="mod-num">
              {{ money(r.cost.cost) }}<span v-if="r.cost.unpriced_calls" class="mod-sub"> · {{ t('channels.unpricedN', { n: num(r.cost.unpriced_calls) }) }}</span>
            </span>
          </li>
        </ul>
      </div>
    </SGrid>
  </SCard>
</template>
