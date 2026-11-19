<script setup lang="ts">
// Model test dialog of an account (after new-api's channel model test):
// candidates = the account's model list, or (all-models accounts) its mapping
// request models + the plugin default models; more can be typed in or fetched
// from the upstream. Single / selected / all tests run at most 3 at a time.
// A test is a diagnosis: it never cools down or disables the account, the
// HTTP failures show the response without the plugin's inferred reason/effect.
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SCheckbox, SCode, SCopyButton, SDescItem, SDescriptions, SHint, SIcon, SInput, SLink, SModal, STable, toast, type TableColumn } from '@sub2api/ui'
import type { Account, AccountLastTest, AccountTestResult, AccountType } from '@/api/types'
import { ACCOUNT_ACTIONS, useScope } from '@/composables/useScope'
import { confirmAction } from '@/composables/crud'
import { errorMessage, notifyError } from '@/utils/errors'
import { copyText } from '@/utils/format'
import { runPool } from './pool'
import { isModelId, splitModelIds } from './modelIds'
import { LATENCY_CLASS, TEST_CONCURRENCY, candidateModels, formatLatency, lastTestOf, latencyLevel, mergeCandidates, prettyBody, summarize, withoutModels, type Candidate, type CandidateSource, type TestRow } from './accountTest'

const props = defineProps<{ open: boolean; account: Account | null; accountType?: AccountType | null }>()
const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  /** A test finished: the account's new last_test. */
  (e: 'tested', id: number, last: AccountLastTest): void
  /** The account was saved (failed models removed). */
  (e: 'updated', a: Account): void
}>()
const { t } = useI18n()
const scope = useScope()

const rows = ref<TestRow[]>([])
/** The "default model" probe (no model sent: the server picks the account's first listed model, else the plugin's default), pinned above the list once run. */
const probe = ref<TestRow | null>(null)
const selected = reactive(new Set<string>())
const search = ref('')
const custom = ref('')
const customError = ref('')
const fetching = ref(false)
const running = ref(false)
let ctrl: AbortController | null = null

const canFetch = computed(() => !!props.account && !props.account.orphaned && scope.can(props.account, ACCOUNT_ACTIONS.test))
const canUpdate = computed(() => !!props.account && !props.account.orphaned && scope.can(props.account, ACCOUNT_ACTIONS.update))

function load() {
  stop()
  const a = props.account
  rows.value = a ? candidateModels(a, props.accountType).map((c) => ({ ...c, state: 'idle' as const })) : []
  probe.value = null
  selected.clear()
  search.value = ''
  custom.value = ''
  customError.value = ''
}
watch(() => [props.open, props.account?.id] as const, ([open], prev) => {
  if (open && (!prev?.[0] || prev[1] !== props.account?.id)) load()
  if (!open) stop()
})

const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? rows.value.filter((r) => r.model.toLowerCase().includes(q)) : rows.value
})
const tableRows = computed(() => (probe.value ? [probe.value, ...visible.value] : visible.value))
const sum = computed(() => summarize(rows.value))
const okModels = computed(() => rows.value.filter((r) => r.state === 'ok').map((r) => r.model))
const failedModels = computed(() => rows.value.filter((r) => r.state === 'fail').map((r) => r.model))
/** Failed models that are on the account's list (only those can be removed). */
const removable = computed(() => failedModels.value.filter((m) => props.account?.models?.includes(m)))
const allVisibleSelected = computed(() => visible.value.length > 0 && visible.value.every((r) => selected.has(r.model)))
const someVisibleSelected = computed(() => !allVisibleSelected.value && visible.value.some((r) => selected.has(r.model)))

const columns = computed<TableColumn[]>(() => [
  { key: 'select', label: '', width: '2rem' },
  { key: 'model', label: t('accounts.modelTest.model') },
  { key: 'state', label: t('common.status') },
  { key: 'actions', label: '', align: 'right', width: '5.5rem' }
])

/** The upstream request id of a test, when the response carried one. */
const requestId = (row: TestRow) => row.result?.response_headers?.['request-id'] || row.result?.response_headers?.['anthropic-request-id'] || ''

function toggle(model: string, v: boolean) {
  if (v) selected.add(model)
  else selected.delete(model)
}
function selectVisible(v: boolean) {
  for (const r of visible.value) toggle(r.model, v)
}
function selectFailed() {
  selected.clear()
  for (const m of failedModels.value) selected.add(m)
}

async function testRow(row: TestRow) {
  const a = props.account
  if (!a) return
  row.state = 'testing'
  row.result = undefined
  row.error = undefined
  try {
    const r = await api.post<AccountTestResult>(`/accounts/${a.id}/test`, row.model ? { model: row.model } : {}, { signal: AbortSignal.timeout(120000) })
    row.result = r
    row.state = r.ok ? 'ok' : 'fail'
    emit('tested', a.id, lastTestOf(r, row.model))
  } catch (e) {
    row.state = 'fail'
    row.error = errorMessage(e)
  }
}

async function runMany(list: TestRow[]) {
  if (running.value || !list.length) return
  running.value = true
  ctrl = new AbortController()
  try {
    await runPool(list, TEST_CONCURRENCY, (r) => testRow(r), ctrl.signal)
  } finally {
    running.value = false
    ctrl = null
  }
  const s = summarize(list)
  toast(t('accounts.modelTest.doneToast', { ok: s.ok, fail: s.fail }), s.fail ? 'warning' : 'success')
}
const testAll = () => runMany(visible.value)
const testSelected = () => runMany(rows.value.filter((r) => selected.has(r.model)))
function stop() {
  ctrl?.abort()
}

function testProbe() {
  if (!probe.value) probe.value = { model: '', source: 'default', state: 'idle' }
  void testRow(probe.value)
}

function addRows(models: string[], source: CandidateSource): number {
  const merged = mergeCandidates(rows.value, models, source)
  const known = new Set(rows.value.map((r) => r.model))
  rows.value = merged.list.map((c: Candidate) => (known.has(c.model) ? rows.value.find((r) => r.model === c.model)! : { ...c, state: 'idle' as const }))
  return merged.added
}

function addCustom() {
  const parts = splitModelIds(custom.value)
  const bad = parts.find((p) => !isModelId(p))
  if (bad) {
    customError.value = t('accounts.modelInvalid', { model: bad })
    return
  }
  customError.value = ''
  if (!parts.length) return
  addRows(parts, 'custom')
  for (const p of parts) selected.add(p)
  custom.value = ''
}

async function fetchUpstream() {
  const a = props.account
  if (!a || fetching.value) return
  fetching.value = true
  try {
    const r = await api.post<{ models: string[]; skipped: number }>(`/accounts/${a.id}/models/fetch`, {})
    const added = addRows(r.models || [], 'upstream')
    toast(added ? t('accounts.modelTest.fetchAdded', { n: added }) : t('accounts.modelTest.fetchNothing'), added ? 'success' : 'info')
  } catch (e) {
    notifyError(e)
  } finally {
    fetching.value = false
  }
}

async function copyList(list: string[]) {
  if (list.length && (await copyText(list.join(',')))) toast(t('accounts.editorUi.copied', { n: list.length }), 'success')
}

async function removeFailed() {
  const a = props.account
  if (!a || !removable.value.length) return
  const next = withoutModels(a.models || [], removable.value)
  if (!next) return
  // An empty list means "every model": refusing beats silently widening the account.
  if (!next.length) {
    toast(t('accounts.modelTest.removeAllRefused'), 'warning')
    return
  }
  const ok = await confirmAction(
    {
      title: t('accounts.modelTest.removeTitle'),
      message: t('accounts.modelTest.removeConfirm', { n: removable.value.length, models: removable.value.join(', '), left: next.length }),
      confirmText: t('accounts.modelTest.removeFailed', { n: removable.value.length }),
      danger: true
    },
    () => api.patch<Account>(`/accounts/${a.id}`, { models: next }),
    (saved) => {
      const drop = new Set(removable.value)
      rows.value = rows.value.filter((r) => !drop.has(r.model))
      for (const m of drop) selected.delete(m)
      toast(t('accounts.modelTest.removed', { n: drop.size }), 'success')
      emit('updated', saved)
    }
  )
  if (!ok) return
}

function close() {
  stop()
  emit('update:open', false)
}

function sourceLabel(s: CandidateSource) {
  return t(`accounts.modelTest.source.${s}`)
}
function stateBadge(r: TestRow): { tone: 'gray' | 'info' | 'success' | 'danger'; label: string } {
  if (r.state === 'testing') return { tone: 'info', label: t('accounts.modelTest.state.testing') }
  if (r.state === 'ok') return { tone: 'success', label: t('accounts.modelTest.state.ok') }
  if (r.state === 'fail') return { tone: 'danger', label: t('accounts.modelTest.state.fail') }
  return { tone: 'gray', label: t('accounts.modelTest.state.idle') }
}
function effectBadge(e?: string): { tone: 'warning' | 'danger'; label: string } | null {
  if (!e) return null
  if (e === 'cooldown') return { tone: 'warning', label: t('accounts.testEffect.cooldown') }
  if (e === 'disable') return { tone: 'danger', label: t('accounts.testEffect.disable') }
  return { tone: 'warning', label: e }
}
function usageText(r?: AccountTestResult): string {
  const u = r?.usage
  if (!u) return ''
  const parts: string[] = []
  const pairs = [
    ['testUsageInput', u.input_tokens],
    ['testUsageOutput', u.output_tokens],
    ['testUsageCacheRead', u.cache_read_tokens],
    ['testUsageCacheWrite', u.cache_creation_tokens]
  ] as const
  for (const [key, v] of pairs) if (v != null) parts.push(`${t(`accounts.${key}`)} ${v}`)
  return parts.join(' · ')
}
/** The model that went upstream when the mapping / plugin changed it. */
function actualModel(r: TestRow): string {
  const m = r.result?.model
  return m && m !== r.model ? m : ''
}
</script>

<template>
  <SModal :open="open" :title="`${t('accounts.modelTest.title')} · ${account?.name || ''}`" width="2xl" @update:open="(v) => (v ? emit('update:open', true) : close())">
    <div v-if="account" class="space-y-3" data-testid="model-test">
      <!-- summary -->
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 text-sm" data-testid="model-test-summary">
        <span class="text-fg-muted">{{ t('accounts.modelTest.total', { n: sum.total }) }}</span>
        <SBadge tone="success" dot>{{ t('accounts.modelTest.okCount', { n: sum.ok }) }}</SBadge>
        <SBadge tone="danger" dot>{{ t('accounts.modelTest.failCount', { n: sum.fail }) }}</SBadge>
        <SBadge v-if="sum.testing" tone="info" dot>{{ t('accounts.modelTest.testingCount', { n: sum.testing }) }}</SBadge>
        <span class="ml-auto flex flex-wrap items-center gap-1.5">
          <SButton size="sm" variant="ghost" icon="copy" :disabled="!okModels.length" data-testid="model-test-copy-ok" @click="copyList(okModels)">{{ t('accounts.modelTest.copyOk') }}</SButton>
          <SButton size="sm" variant="ghost" icon="copy" :disabled="!failedModels.length" data-testid="model-test-copy-fail" @click="copyList(failedModels)">{{ t('accounts.modelTest.copyFail') }}</SButton>
          <SButton v-if="canUpdate && removable.length" size="sm" variant="danger" icon="trash" :disabled="running" data-testid="model-test-remove-failed" @click="removeFailed">{{ t('accounts.modelTest.removeFailed', { n: removable.length }) }}</SButton>
        </span>
      </div>
      <SHint size="xs">{{ t('accounts.modelTest.diagnosisOnly') }}</SHint>

      <!-- toolbar -->
      <div class="flex flex-wrap items-start gap-2">
        <div class="min-w-[12rem] flex-1">
          <SInput v-model="search" icon="search" clearable :placeholder="t('accounts.modelTest.search')" :aria-label="t('accounts.modelTest.search')" />
        </div>
        <form class="flex min-w-[14rem] flex-1 flex-col gap-1" @submit.prevent="addCustom">
          <div class="flex gap-2">
            <SInput v-model="custom" mono :error="!!customError" :placeholder="t('accounts.modelTest.customPlaceholder')" data-testid="model-test-custom" />
            <SButton type="submit" icon="plus" :disabled="!custom.trim()">{{ t('accounts.modelTest.add') }}</SButton>
          </div>
          <SHint v-if="customError" tone="danger" size="xs">{{ customError }}</SHint>
        </form>
        <SButton v-if="canFetch" icon="download" :loading="fetching" :title="t('accounts.modelTest.fetchHint')" data-testid="model-test-fetch" @click="fetchUpstream">{{ t('accounts.modelTest.fetch') }}</SButton>
        <SButton icon="bolt" :loading="probe?.state === 'testing'" :title="t('accounts.modelTest.probeHint')" data-testid="model-test-probe" @click="testProbe">{{ t('accounts.modelTest.probe') }}</SButton>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-fg-muted">
        <SCheckbox size="xs" :model-value="allVisibleSelected" :indeterminate="someVisibleSelected" :disabled="!visible.length" data-testid="model-test-select-all" @update:model-value="selectVisible">
          {{ t('accounts.modelTest.selectVisible', { n: visible.length }) }}
        </SCheckbox>
        <span class="flex items-center gap-3">
          <SLink v-if="failedModels.length" as="button" class="text-xs" @click="selectFailed">{{ t('accounts.modelTest.selectFailed') }}</SLink>
          <span>{{ t('accounts.modelTest.selected', { n: selected.size }) }}</span>
        </span>
      </div>

      <SHint v-if="!rows.length" tone="warning">{{ t('accounts.modelTest.noCandidates') }}</SHint>
      <STable v-else-if="tableRows.length" :columns="columns" :rows="tableRows" row-key="model" expandable dense data-testid="model-test-table">
        <template #cell-select="{ row }">
          <SCheckbox v-if="row.model" bare :model-value="selected.has(row.model)" :aria-label="row.model" @update:model-value="toggle(row.model, $event)" />
        </template>
        <template #cell-model="{ row }">
          <div class="flex min-w-0 max-w-[18rem] flex-wrap items-center gap-1.5">
            <span v-if="row.model" class="truncate font-mono text-xs" :title="row.model">{{ row.model }}</span>
            <span v-else class="text-xs font-medium">{{ t('accounts.modelTest.probeRow') }}</span>
            <SBadge v-if="row.model && row.source !== 'account'" tone="gray" size="xs">{{ sourceLabel(row.source) }}</SBadge>
          </div>
          <div v-if="actualModel(row) || (!row.model && row.result?.model)" class="mt-0.5 flex items-center gap-1 font-mono text-xxs text-accent-600 dark:text-accent-300" :title="t('accounts.testActualModel')">
            <SIcon name="chevron-right" class="h-3 w-3" />{{ actualModel(row) || row.result?.model }}
          </div>
        </template>
        <!-- Like new-api: the outcome and request time; a failure lists what went wrong below it. -->
        <template #cell-state="{ row }">
          <div class="max-w-[26rem] space-y-0.5 text-xs">
            <div class="flex items-center gap-2">
              <SBadge :tone="stateBadge(row).tone" dot>{{ stateBadge(row).label }}</SBadge>
              <span v-if="row.result" class="tabular-nums" :class="LATENCY_CLASS[latencyLevel(row.result.latency_ms)]" data-testid="model-test-duration">{{ t('accounts.modelTest.duration', { time: formatLatency(row.result.latency_ms) }) }}</span>
              <span v-if="row.result?.protocol" class="font-mono text-xxs text-fg-muted" :title="t('accounts.testProtocol')" data-testid="model-test-protocol">{{ row.result.protocol }}</span>
            </div>
            <div v-if="row.error" class="line-clamp-2 break-all text-danger-600 dark:text-danger-400" :title="row.error">{{ row.error }}</div>
            <template v-else-if="row.result && !row.result.ok">
              <div v-if="row.result.status || row.result.error_code" class="break-all font-mono text-danger-600 dark:text-danger-400" data-testid="model-test-error-code">
                <template v-if="row.result.status">HTTP {{ row.result.status }}</template><template v-if="row.result.status && row.result.error_code"> · </template><template v-if="row.result.error_code">{{ row.result.error_code }}</template>
              </div>
              <SHint v-if="row.result.error_source" size="xs">{{ t('accounts.testErrorSource') }}: {{ t(`accounts.testErrorSources.${row.result.error_source}`) }}</SHint>
              <div v-if="row.result.error_source !== 'response' && row.result.reason" class="line-clamp-2 break-all font-medium text-danger-600 dark:text-danger-400" :title="row.result.reason">{{ row.result.reason }}</div>
              <!-- What the upstream answered: the plugin's reason alone hides it. -->
              <div v-if="row.result.message && (row.result.error_source === 'response' || !row.result.reason?.includes(row.result.message))" class="line-clamp-3 break-all font-mono text-xxs text-danger-500/90 dark:text-danger-300/90" :title="row.result.message" data-testid="model-test-error-body">
                {{ row.result.message }}
              </div>
              <div v-if="requestId(row)" class="break-all font-mono text-xxs text-fg-muted" data-testid="model-test-request-id">Request ID: {{ requestId(row) }}</div>
              <SBadge v-if="row.result.error_source !== 'response' && effectBadge(row.result.effect)" :tone="effectBadge(row.result.effect)!.tone" :title="t('accounts.testEffectNotApplied')">{{ effectBadge(row.result.effect)!.label }}</SBadge>
            </template>
          </div>
        </template>
        <template #cell-actions="{ row }">
          <SButton size="sm" variant="ghost" :loading="row.state === 'testing'" :disabled="running && row.state !== 'testing'" data-testid="model-test-one" @click="testRow(row)">{{ t('common.test') }}</SButton>
        </template>
        <template #expand="{ row }">
          <div class="space-y-3 px-2 py-2 text-xs">
            <SHint v-if="!row.result && !row.error" size="xs">{{ t('accounts.modelTest.notRun') }}</SHint>
            <SDescriptions v-if="row.result" dense label-width="7rem">
              <SDescItem v-if="row.result.requested_model" :label="t('accounts.modelTest.requested')" mono>{{ row.result.requested_model }}</SDescItem>
              <SDescItem v-if="row.result.model" :label="t('accounts.testActualModel')" mono>{{ row.result.model }}</SDescItem>
              <SDescItem v-if="row.result.protocol" :label="t('accounts.testProtocol')" mono>{{ row.result.protocol }}</SDescItem>
              <SDescItem v-if="row.result.upstream" :label="t('accounts.testUpstream')" mono class="break-all">{{ row.result.upstream }}</SDescItem>
              <SDescItem label="HTTP" mono>{{ row.result.status || t('accounts.testNoHTTPResponse') }}</SDescItem>
              <SDescItem v-if="requestId(row)" label="Request ID" mono class="break-all">
                <span data-testid="model-test-request-id-detail">{{ requestId(row) }}</span>
                <SCopyButton :text="requestId(row)" class="ml-1 align-middle" />
              </SDescItem>
              <SDescItem v-if="row.result.response_headers?.['retry-after']" label="Retry-After" mono>{{ row.result.response_headers['retry-after'] }}</SDescItem>
              <SDescItem v-if="usageText(row.result)" :label="t('accounts.testUsage')" class="tabular-nums">{{ usageText(row.result) }}</SDescItem>
              <SDescItem v-if="row.result.error_code" :label="t('accounts.testErrorCode')" mono>{{ row.result.error_code }}</SDescItem>
              <SDescItem v-if="row.result.error_source" :label="t('accounts.testErrorSource')">{{ t(`accounts.testErrorSources.${row.result.error_source}`) }}</SDescItem>
              <SDescItem v-if="row.result.message" :label="t('accounts.message')" class="break-all">{{ row.result.message }}</SDescItem>
              <SDescItem v-if="row.result.error_source !== 'response' && row.result.reason" :label="t('accounts.testReason')" class="break-all">{{ row.result.reason }}</SDescItem>
              <SDescItem v-if="row.result.error_source !== 'response' && effectBadge(row.result.effect)" :label="t('accounts.testEffectTitle')">
                <SBadge :tone="effectBadge(row.result.effect)!.tone">{{ effectBadge(row.result.effect)!.label }}</SBadge>
                <SHint inline size="xs" class="ml-2">{{ t('accounts.testEffectNotApplied') }}</SHint>
              </SDescItem>
            </SDescriptions>
            <div v-if="row.result?.body">
              <div class="mb-1 flex items-center justify-between">
                <span class="font-medium text-fg">{{ t('accounts.testBody') }}</span>
                <SCopyButton :text="prettyBody(row.result.body)" />
              </div>
              <SCode class="max-h-64 overflow-y-auto">{{ prettyBody(row.result.body) }}</SCode>
            </div>
            <SHint v-else-if="row.result" size="xs">{{ t('accounts.testBodyEmpty') }}</SHint>
            <div v-if="row.result?.response_headers && Object.keys(row.result.response_headers).length">
              <div class="mb-1 font-medium">{{ t('accounts.testResponseHeaders') }}</div>
              <SCode class="max-h-48 overflow-y-auto">{{ JSON.stringify(row.result.response_headers, null, 2) }}</SCode>
            </div>
          </div>
        </template>
      </STable>
      <SHint v-else size="xs">{{ t('accounts.modelTest.noMatch') }}</SHint>
    </div>
    <template #footer>
      <SButton v-if="running" variant="danger" icon="stop" data-testid="model-test-stop" @click="stop">{{ t('accounts.modelTest.stop') }}</SButton>
      <SButton @click="close">{{ t('common.close') }}</SButton>
      <SButton :disabled="!selected.size || running" data-testid="model-test-selected" @click="testSelected">{{ t('accounts.modelTest.testSelected', { n: selected.size }) }}</SButton>
      <SButton variant="primary" :loading="running" :disabled="!visible.length || running" data-testid="model-test-all" @click="testAll">{{ t('accounts.modelTest.testAll', { n: visible.length }) }}</SButton>
    </template>
  </SModal>
</template>
