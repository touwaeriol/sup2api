<script setup lang="ts">
// PROTOTYPE ONLY (design draft, not for merge): 协议绑定 = the protocols the
// account type speaks natively; 协议转换 = every protocol whose requests are
// converted to an upstream protocol first (within the platform or from
// another one). A row per protocol; its models open under it as chips.
import { computed, ref } from 'vue'
import { SButton, SCard, SCheckbox, SChip, SHint, SIcon, SSegmented } from '@sub2api/ui'
import ConversionGradeBadge from '@/components/ConversionGradeBadge.vue'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import ProtocolIssues from './ProtocolIssues.vue'
import { allColumns, columnState, matrixModels, protocolsOf, REST, toggleCell, type ModelRules, type ProtocolColumn } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import type { ProtocolEditor } from './useProtocolEdit'

const props = defineProps<{ editor: ProtocolEditor; models: string[]; options: string[] }>()
const labels = useProtocolLabels()
const { columns, mode, protocols, rules, defaults, issues } = props.editor

const isCross = (c: ProtocolColumn) => c.via === 'converted'
const listed = computed(() => matrixModels(props.models, rules.value))
/** The other models have a chip when the account lists no model (they are all of them) or already has their rule. */
const showRest = computed(() => !props.models.length || REST in rules.value)
const rows = computed(() => (showRest.value ? [...listed.value, REST] : listed.value))
const isOn = (m: string, c: ProtocolColumn) => columnState(protocolsOf(rules.value, m, defaults.value), c) !== 'off'
const served = (c: ProtocolColumn) => rows.value.filter(m => isOn(m, c))
/** Whether the account takes requests on the protocol: an own one it opens, a conversion some model has. */
const accepts = (c: ProtocolColumn) => (isCross(c) ? served(c).length > 0 : columnState(protocols.value, c) !== 'off')

/** Rules saying nothing new are dropped: a model served like the others, the others served by default. */
function pruned(next: ModelRules): ModelRules {
  const same = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every(id => b.includes(id))
  const out = { ...next }
  for (const m of Object.keys(out)) if (m !== REST && same(out[m], out[REST] ?? defaults.value)) delete out[m]
  if (out[REST] && same(out[REST], defaults.value) && Object.keys(out).length === 1) delete out[REST]
  return out
}
const toggle = (m: string, c: ProtocolColumn) => (rules.value = pruned(toggleCell(rules.value, m, c, defaults.value)))
function setModels(c: ProtocolColumn, models: string[], on: boolean) {
  for (const m of models) if (isOn(m, c) !== on) toggle(m, c)
}
/** A conversion from another platform is switched for every model shown. */
function setAccepts(c: ProtocolColumn, on: boolean) {
  if (isCross(c)) return setModels(c, rows.value, on)
  if (mode.value === 'all') props.editor.setMode('custom')
  props.editor.toggleOwn(c)
  // The rules follow: a closed protocol leaves them, a reopened one starts with every model.
  const next = Object.fromEntries(Object.entries(rules.value).map(([m, ids]) => [m, on ? [...new Set([...ids, ...c.ids])] : ids.filter(id => !c.ids.includes(id))]))
  rules.value = pruned(next)
}
/** The models of a closed own protocol cannot be chosen. */
const choosable = (c: ProtocolColumn) => isCross(c) || accepts(c)

function scope(c: ProtocolColumn): { text: string; plain: boolean } {
  if (!accepts(c)) return { text: isCross(c) ? '未开启' : '未开放', plain: true }
  const on = served(c)
  if (on.length === rows.value.length) return { text: '全部模型', plain: true }
  if (!on.length) return { text: '没有模型', plain: false }
  const off = rows.value.length - on.length
  return { text: off <= 3 && off < on.length ? `全部模型，除 ${off} 个` : `${on.length} 个模型`, plain: false }
}

const opened = ref<string[]>([])
const toggleOpen = (k: string) => (opened.value = opened.value.includes(k) ? opened.value.filter(x => x !== k) : [...opened.value, k])

const all = computed(() => allColumns(columns.value))
const groups = computed(() => [
  { key: 'native', title: '协议绑定', tone: 'text-primary-600 dark:text-primary-300', hint: '账号原生支持的协议：客户端用什么协议，就按什么协议发给上游', list: all.value.filter(c => c.via === 'native') },
  { key: 'converted', title: '协议转换', tone: 'text-warning-600 dark:text-warning-400', hint: '先把客户端的协议转换成上游协议，再发给上游', list: all.value.filter(c => c.via !== 'native') }
])
</script>

<template>
  <SCard title="协议绑定与转换" subtitle="这个账号接哪些协议的请求，每个协议下提供哪些模型">
    <template #actions><SSegmented size="sm" :options="[{ value: 'visual', label: '可视化' }, { value: 'json', label: 'JSON' }]" model-value="visual" /></template>
    <div class="space-y-4">
      <div v-for="g in groups" :key="g.key" class="space-y-2" :data-group="g.key">
        <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span class="text-sm font-semibold" :class="g.tone">{{ g.title }}</span>
          <span class="text-xs text-fg-subtle">{{ g.hint }}</span>
        </div>
        <SHint v-if="!g.list.length" size="xs">{{ g.key === 'native' ? '此账号类型没有原生协议。' : '此账号类型没有可转换的协议。' }}</SHint>
        <ul v-else class="divide-y divide-line overflow-hidden rounded-xl border border-line">
          <li v-for="c in g.list" :key="c.key" :data-protocol="c.key">
            <div class="flex items-center gap-3 px-3 py-2">
              <div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1">
                <SCheckbox :model-value="accepts(c)" :label="labels.columnName(c)" class="font-medium md:min-w-[12.5rem]" data-testid="b-accept" @update:model-value="setAccepts(c, $event)" />
                <template v-if="g.key === 'converted'">
                  <span class="whitespace-nowrap text-xs text-fg-muted md:min-w-[6.5rem]"><span class="text-fg-subtle">→</span> {{ labels.targetName(c) }}</span>
                  <ConversionGradeBadge :grade="c.grade" :via="c.hops" />
                </template>
                <code class="hidden min-w-0 flex-1 truncate font-mono text-xs text-fg-subtle md:block">{{ c.method }} {{ c.path }}</code>
              </div>
              <button
                type="button"
                class="inline-flex h-7 shrink-0 items-center gap-1 rounded-control border px-2 text-xs hover:border-primary-400 disabled:cursor-not-allowed disabled:opacity-60"
                :class="scope(c).plain ? 'border-line text-fg-subtle' : 'border-primary-400 text-primary-700 dark:text-primary-300'"
                :disabled="!choosable(c)"
                data-testid="b-scope"
                @click="toggleOpen(c.key)"
              >
                {{ scope(c).text }}<SIcon name="chevron-down" class="h-3.5 w-3.5 transition-transform" :class="opened.includes(c.key) && choosable(c) ? 'rotate-180' : ''" />
              </button>
            </div>
            <div v-if="opened.includes(c.key) && choosable(c)" class="space-y-2 border-t border-dashed border-line bg-surface-2/60 px-3 py-2.5" data-testid="b-panel">
              <div class="flex flex-wrap items-center gap-1.5">
                <SChip v-for="m in rows" :key="m" :pressed="isOn(m, c)" :data-model="m" @update:pressed="toggle(m, c)"><span :class="m === REST ? '' : 'font-mono'">{{ labels.modelName(m) }}</span></SChip>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <SButton size="xs" data-testid="b-all" @click="setModels(c, rows, true)">全选</SButton>
                <SButton size="xs" variant="ghost" @click="setModels(c, rows, false)">清空</SButton>
                <SHint v-if="showRest" size="xs" inline class="ml-1">“其余模型”= 没有单独列出的全部模型</SHint>
              </div>
              <ModelTagSelect :model-value="[]" :options="options" placeholder="列表之外的模型：输入模型名，回车添加" @update:model-value="setModels(c, $event, true)" />
            </div>
          </li>
        </ul>
      </div>
      <div v-if="mode === 'custom'" class="flex flex-wrap items-center gap-2" data-testid="b-custom">
        <SHint size="xs" class="min-w-0 flex-1">已指定开放的协议：账号类型以后新增的协议不会自动开放。</SHint>
        <SButton size="xs" variant="ghost" @click="editor.setMode('all')">恢复全部开放</SButton>
      </div>
      <ProtocolIssues :issues="issues" />
    </div>
  </SCard>
</template>
