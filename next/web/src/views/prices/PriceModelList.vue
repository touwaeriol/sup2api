<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { SButton, SInput, SPagination, SSelect, SSpinner } from '@sub2api/ui'
import type { Price } from '@/api/types'
import { useList } from '@/composables/useList'
import { summarize } from './priceExpr'

// The prices beside the editor: search, mode filter and one row per model,
// the one being edited marked. Picking a row opens it in the editor.
// PROTOTYPE: new texts are Chinese literals until adopted.
const props = defineProps<{ current: number | null; canManage: boolean }>()
const router = useRouter()

const { items, loading, page, pageSize, total, filters } = useList<Price>('/prices', { q: '', mode: '' }, { pageSize: 20 })
const modeOptions = [
  { value: '', label: '全部方式' },
  { value: 'per_token', label: '按 token' },
  { value: 'per_request', label: '按次' },
  { value: 'expression', label: '表达式' }
]

const usd = (n: number) => `$${+n.toFixed(4)}`
function brief(p: Price): string {
  const s = summarize(p.mode, p.config)
  if (s.kind === 'per_request') return `按次 ${usd(s.price)}${s.rules ? ` · ${s.rules} 条规则` : ''}`
  if (s.kind === 'expression') return '表达式'
  if (s.first.unit === 'request') return `按次 ${usd(Number(s.first.price) || 0)}${s.tiers > 1 ? ` · ${s.tiers} 档` : ''}`
  const head = `${usd(Number(s.first.p) || 0)} / ${usd(Number(s.first.c) || 0)}`
  return [head, s.tiers > 1 ? `${s.tiers} 档` : '', s.rules ? `${s.rules} 条规则` : ''].filter(Boolean).join(' · ')
}
const rows = computed(() => items.value.map(p => ({ p, brief: brief(p) })))
</script>

<template>
  <aside class="flex min-h-0 flex-col rounded-xl border border-line bg-surface" data-testid="price-model-list">
    <div class="space-y-2 border-b border-line p-3">
      <SInput v-model="filters.q" size="sm" icon="search" placeholder="搜索模型…" aria-label="搜索模型" />
      <div class="flex items-center gap-2">
        <SSelect v-model="filters.mode" size="sm" class="min-w-0 flex-1" :options="modeOptions" aria-label="计费方式" />
        <SButton v-if="canManage" size="sm" variant="primary" icon="plus" @click="router.push('/prices/new')">添加模型</SButton>
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
            <span v-if="!r.p.enabled" class="shrink-0 rounded bg-surface-2 px-1.5 text-[10px] text-fg-subtle">停用</span>
          </button>
        </li>
        <li v-if="!rows.length" class="px-3 py-8 text-center text-xs text-fg-muted">没有匹配的模型</li>
      </ul>
    </div>
    <div class="border-t border-line px-2 py-2">
      <SPagination v-model:page="page" v-model:page-size="pageSize" :total="total" hide-sizes />
    </div>
  </aside>
</template>
