<script setup lang="ts">
// PROTOTYPE ONLY (design comparison, not for merge): protocol rows, model chips.
import { computed, ref } from 'vue'
import { SBadge, SButton, SCard, SCheckbox, SChip, SHint, SIcon, SSegmented } from '@sub2api/ui'
import ConversionGradeBadge from '@/components/ConversionGradeBadge.vue'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import ProtocolIssues from './ProtocolIssues.vue'
import { columnState, matrixModels, protocolsOf, REST, toggleCell, type ProtocolColumn } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import type { ProtocolEditor, ProtocolMode } from './useProtocolEdit'

const props = defineProps<{ editor: ProtocolEditor; models: string[]; options: string[] }>()
const labels = useProtocolLabels()
const { columns, mode, protocols, rules, defaults, issues } = props.editor
const rows = computed(() => [...matrixModels(props.models, rules.value), REST])
const isOn = (m: string, c: ProtocolColumn) => columnState(protocolsOf(rules.value, m, defaults.value), c) !== 'off'
const opened = ref<string[]>([])
const toggleOpen = (k: string) => (opened.value = opened.value.includes(k) ? opened.value.filter(x => x !== k) : [...opened.value, k])
const open = (c: ProtocolColumn) => columnState(protocols.value, c) !== 'off'
function scope(c: ProtocolColumn) {
  if (c.via !== 'converted' && !open(c)) return '未开放'
  const on = rows.value.filter(m => isOn(m, c))
  if (on.length === rows.value.length) return '全部模型'
  if (!on.length) return c.via === 'converted' ? '未开启' : '不提供任何模型'
  if (on.includes(REST)) return `全部模型，除 ${rows.value.length - on.length} 个`
  return `${on.length} 个模型`
}
const toggle = (m: string, c: ProtocolColumn) => (rules.value = toggleCell(rules.value, m, c, defaults.value))
function setAll(c: ProtocolColumn, on: boolean) {
  for (const m of rows.value) if (m !== REST && isOn(m, c) !== on) toggle(m, c)
}
/** Models outside the account's list join the protocol. */
function add(c: ProtocolColumn, models: string[]) {
  for (const m of models) if (!isOn(m, c)) toggle(m, c)
}
const groups = computed(() => [
  { key: 'own', title: `本平台（${labels.platformsName([...new Set(columns.value.own.map(c => c.platform))].map(id => ({ id })))}）`, hint: '账号接哪些协议的请求', list: columns.value.own },
  { key: 'cross', title: '协议转换', hint: '默认不提供；给模型开启后，才接受这些协议的请求并转换', list: columns.value.cross }
])
</script>

<template>
  <SCard title="协议绑定与转换" subtitle="这个账号接哪些协议的请求，每个协议下提供哪些模型">
    <template #actions><SSegmented size="sm" :options="[{ value: 'visual', label: '可视化' }, { value: 'json', label: 'JSON' }]" model-value="visual" /></template>
    <div class="space-y-4">
      <div v-for="g in groups" :key="g.key" class="space-y-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="text-sm"><span class="font-semibold" :class="g.key === 'own' ? 'text-primary-600 dark:text-primary-300' : 'text-warning-600 dark:text-warning-400'">{{ g.title }}</span><span class="ml-2 text-xs text-fg-subtle">{{ g.hint }}</span></div>
          <SSegmented v-if="g.key === 'own'" size="sm" :options="[{ value: 'all', label: '全部' }, { value: 'custom', label: '指定' }]" :model-value="mode" @update:model-value="editor.setMode($event as ProtocolMode)" />
        </div>
        <ul class="divide-y divide-line overflow-hidden rounded-xl border border-line">
          <li v-for="c in g.list" :key="c.key" :data-protocol="c.key">
            <div class="flex items-center gap-3 px-3 py-2">
              <div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1">
                <SCheckbox v-if="g.key === 'own'" :model-value="open(c)" :disabled="mode === 'all'" :label="labels.columnName(c)" class="font-medium md:min-w-[11rem]" @update:model-value="editor.toggleOwn(c)" />
                <span v-else class="text-sm font-medium text-fg md:min-w-[11rem]">{{ labels.columnName(c) }}</span>
                <SBadge v-if="c.via === 'adapted'" tone="info">适配 → {{ labels.targetName(c) }}</SBadge>
                <SBadge v-else-if="c.via === 'native'" tone="success">原生</SBadge>
                <ConversionGradeBadge v-else :grade="c.grade" :via="c.hops" converted />
                <code class="hidden min-w-0 flex-1 truncate font-mono text-xs text-fg-subtle md:block">{{ c.method }} {{ c.path }}</code>
              </div>
              <button type="button" class="inline-flex h-7 shrink-0 items-center gap-1 rounded-control border px-2 text-xs hover:border-primary-400" :class="scope(c) === '全部模型' || scope(c) === '未开启' || scope(c) === '未开放' ? 'border-line text-fg-subtle' : 'border-primary-400 text-primary-700 dark:text-primary-300'" data-testid="b-scope" @click="toggleOpen(c.key)">
                {{ scope(c) }}<SIcon name="chevron-down" class="h-3.5 w-3.5 transition-transform" :class="opened.includes(c.key) ? 'rotate-180' : ''" />
              </button>
            </div>
            <div v-if="opened.includes(c.key)" class="space-y-2 border-t border-dashed border-line bg-surface-2/60 px-3 py-2.5" data-testid="b-panel">
              <div class="flex flex-wrap items-center gap-1.5">
                <SChip v-for="m in rows" :key="m" :pressed="isOn(m, c)" :data-model="m" @update:pressed="toggle(m, c)"><span :class="m === REST ? '' : 'font-mono'">{{ labels.modelName(m) }}</span></SChip>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <SButton size="xs" data-testid="b-all" @click="setAll(c, true)">全选</SButton>
                <SButton size="xs" variant="ghost" @click="setAll(c, false)">清空</SButton>
                <SHint size="xs" inline class="ml-1">“其余模型”= 没有单独列出的全部模型</SHint>
              </div>
              <ModelTagSelect :model-value="[]" :options="options" placeholder="列表之外的模型：输入模型名，回车添加" @update:model-value="add(c, $event)" />
            </div>
          </li>
        </ul>
      </div>
      <ProtocolIssues :issues="issues" />
    </div>
  </SCard>
</template>
