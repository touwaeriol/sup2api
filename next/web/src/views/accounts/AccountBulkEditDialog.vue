<script setup lang="ts">
// Bulk edit of the selected accounts' core properties: each field applies
// only when ticked (editing a field ticks it), everything else stays as it
// is. Groups replace, add to or remove from each account's own groups. One
// PATCH /accounts/:id per account (the same checks, plugin hooks and audit as
// a single edit), a few at a time; a stop keeps the rest unchanged. Failures
// stay listed in the dialog; the saved rows are reported through `saved`.
// A field some selected account's type fixes (common controls) is disabled.
// The session cap and its window are one field (fixed when either is).
// The own protocols and the protocols per model (DESIGN-2026-11-17-PROTOCOL-BINDING §4)
// replace each account's; what can be ticked depends on the account type, so
// they are offered only when every selected account is of one type.
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SCheckbox, SHint, SInput, SModal, SProgress, SSegmented, SSwitch, toast } from '@sub2api/ui'
import type { Account, AccountType } from '@/api/types'
import GroupPicker from '@/components/GroupPicker.vue'
import ProxyPicker from '@/components/ProxyPicker.vue'
import { useBulkRun, type BulkFailure } from '@/composables/useBulkRun'
import { useAccountCaps } from '@/composables/useAccountCaps'
import { errorMessage } from '@/utils/errors'
import { fixedCount, type CommonControlName } from './commonControls'
import { todayQuery } from './accountUsage'
import { buildBulkPatch, BULK_LIMITS, type BulkFields, type GroupMode, type NumberField } from './bulkEdit'
import { typeKey } from './accountTypes'
import ModelProtocolList from './protocols/ModelProtocolList.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import ProtocolIssues from './protocols/ProtocolIssues.vue'
import { bindingIssues, sharedOwnProtocols, type ProtocolBinding } from './protocols/protocolBinding'
import { useProtocolEdit } from './protocols/useProtocolEdit'
import { SPM_WINDOW_DEFAULT, validWindow } from './spmWindow'
import SessionLimitInput from './controls/SessionLimitInput.vue'

const props = defineProps<{ open: boolean; accounts: Account[]; typeOf: (a: Account) => AccountType | undefined }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved', rows: Account[]): void }>()
const { t } = useI18n()

const on = reactive({ priority: false, weight: false, max_concurrency: false, tpm_limit: false, rpm_limit: false, spm_limit: false, groups: false, proxy: false, auto_disable: false, protocols: false, model_protocols: false })
/** The fields edited here; the two protocol fields are edited in `protocolEdit`. */
type Values = Omit<BulkFields, keyof ProtocolBinding>
const initialValues = (): Values => ({ priority: 10, weight: 1, max_concurrency: 10, tpm_limit: 0, rpm_limit: 0, spm_limit: 0, spm_window_seconds: SPM_WINDOW_DEFAULT, group_ids: [], groupMode: 'replace', proxy_id: null, auto_disable: true })
const values = reactive<Values>(initialValues())
const failures = ref<BulkFailure<Account>[]>([])
/** The fields that are common controls. */
const FIXABLE = ['auto_disable', ...(Object.keys(BULK_LIMITS) as NumberField[])] as const
const bulk = useBulkRun(4)
const caps = useAccountCaps(true)

// ---------------------------------------------------------------- protocols: one account type only
/** The type every selected account has; null when they differ or the type is gone. */
const sharedType = computed(() => {
  const [first, ...rest] = props.accounts
  const same = first && rest.every((a) => typeKey(a.plugin_key, a.type) === typeKey(first.plugin_key, first.type))
  return (same && props.typeOf(first)) || null
})
const protocolEdit = useProtocolEdit(() => sharedType.value)
/** Models the protocol entries offer: those of the selected accounts and the type's presets. */
const protocolModels = computed(() => [...new Set([...props.accounts.flatMap((a) => a.models || []), ...(sharedType.value?.default_models || [])])])
// Editing either field ticks it (sync, so resetting on open can untick afterwards).
watch(protocolEdit.protocols, () => (on.protocols = true), { flush: 'sync' })
watch(protocolEdit.rules, () => (on.model_protocols = true), { flush: 'sync' })
watch(sharedType, (at) => !at && (on.protocols = on.model_protocols = false))
/**
 * Findings of the ticked protocol fields. Whether an account still serves a
 * protocol takes both fields: with one ticked the other is each account's
 * own, and the server decides. The rules meet the own protocols set here
 * when that field is ticked, else each account's: a rule's own protocol is
 * closed where some selected account does not open it.
 */
const protocolIssues = computed(() => {
  if (!on.model_protocols || !sharedType.value) return []
  const own = on.protocols ? protocolEdit.protocols.value : sharedOwnProtocols(props.accounts.map((a) => a.protocols))
  return bindingIssues(own, protocolEdit.rules.value, protocolEdit.columns.value).filter((i) => i.code !== 'no_protocol' || on.protocols)
})
const protocolsInvalid = computed(() => protocolIssues.value.some((i) => i.level === 'error'))

watch(() => props.open, (open) => {
  if (!open) return
  Object.assign(values, initialValues())
  protocolEdit.setSaved(null)
  Object.keys(on).forEach((k) => (on[k as keyof typeof on] = false))
  failures.value = []
})

/** Editing a field ticks it. */
function set<K extends keyof Values>(key: K, v: Values[K], flag: keyof typeof on) {
  values[key] = v
  on[flag] = true
}

const numberFields = computed(() => [
  { key: 'priority' as const, label: t('accounts.priority'), hint: t('accounts.priorityHint') },
  { key: 'weight' as const, label: t('accounts.weight'), hint: t('accounts.bulk.weightHint') },
  { key: 'max_concurrency' as const, label: t('accounts.concurrency'), hint: t('accounts.bulk.zeroUnlimited') },
  { key: 'rpm_limit' as const, label: t('accounts.rpmLimit'), hint: t('accounts.bulk.zeroUnlimited') },
  { key: 'tpm_limit' as const, label: t('accounts.tpmLimit'), hint: t('accounts.bulk.zeroUnlimited') },
  { key: 'spm_limit' as const, label: t('accounts.spmLimit'), hint: t('accounts.bulk.spmHint') }
])
const invalid = computed(() => numberFields.value.filter((f) => {
  if (!on[f.key]) return false
  const v = Number(values[f.key])
  const [min, max] = BULK_LIMITS[f.key]
  return values[f.key] === null || !Number.isInteger(v) || v < min || v > max || (f.key === 'spm_limit' && !validWindow(values.spm_window_seconds))
}).map((f) => f.key))
const groupModes = computed(() => (['replace', 'add', 'remove'] as GroupMode[]).map((m) => ({ value: m, label: t(`accounts.bulk.groupMode.${m}`) })))
const anyOn = computed(() => Object.values(on).some(Boolean))
/** Controls behind a field: the session limit is the cap and its window. */
const controlsOf = (k: (typeof FIXABLE)[number]): CommonControlName[] => (k === 'spm_limit' ? ['spm_limit', 'spm_window_seconds'] : [k])
/** Selected accounts whose type fixes the field (common controls); such a field is not applied. */
const fixed = computed(() => Object.fromEntries(FIXABLE.map((k) => [k, fixedCount(props.accounts, props.typeOf, ...controlsOf(k))])) as Record<(typeof FIXABLE)[number], number>)
watch(fixed, (f) => (Object.keys(f) as Array<keyof typeof f>).forEach((k) => f[k] && (on[k] = false)), { immediate: true })

async function apply() {
  if (!anyOn.value || invalid.value.length || protocolsInvalid.value || bulk.running.value) return
  const fields: BulkFields = { ...values, ...protocolEdit.binding() }
  failures.value = []
  const saved: Account[] = []
  let unchanged = 0
  const out = await bulk.run(props.accounts, async (a, signal) => {
    const body = buildBulkPatch(a, fields, on)
    if (!body) {
      unchanged++
      return
    }
    saved.push(await api.patch<Account>(`/accounts/${a.id}`, body, { signal, query: todayQuery() }))
  })
  failures.value = out.failed
  if (saved.length) emit('saved', saved)
  const params = { ok: saved.length, fail: out.failed.length, skipped: out.skipped + unchanged }
  if (!out.failed.length && !out.skipped) {
    toast(t('accounts.bulk.editDone', params), 'success')
    emit('update:open', false)
  } else {
    toast(t('accounts.bulk.editPartial', params), 'warning')
  }
}
</script>

<template>
  <SModal :open="open" :title="t('accounts.bulk.editTitle', { n: accounts.length })" width="lg" :persistent="bulk.running.value" :closable="!bulk.running.value" @update:open="(v) => emit('update:open', v)">
    <div class="space-y-4">
      <SHint>{{ t('accounts.bulk.editInfo', { n: accounts.length }) }}</SHint>
      <fieldset class="grid gap-x-4 gap-y-3 sm:grid-flow-row-dense sm:grid-cols-2" :disabled="bulk.running.value">
        <div v-for="f in numberFields" :key="f.key" class="space-y-1" :class="f.key === 'spm_limit' ? 'sm:col-span-2' : ''" :data-testid="`bulk-field-${f.key}`">
          <SCheckbox v-model="on[f.key]" size="sm" :disabled="!!fixed[f.key]">{{ f.label }}</SCheckbox>
          <SessionLimitInput
            v-if="f.key === 'spm_limit'"
            :limit="values.spm_limit"
            :window-seconds="values.spm_window_seconds"
            :limit-max="BULK_LIMITS.spm_limit[1]"
            :suffix="t('accounts.spmWindow.sessions')"
            :aria-label="f.label"
            :class="on.spm_limit ? '' : 'opacity-60'"
            :error="invalid.includes('spm_limit')"
            :disabled="!!fixed.spm_limit"
            testid="bulk-spm_limit"
            @update:limit="set('spm_limit', $event ?? null, 'spm_limit')"
            @update:window-seconds="set('spm_window_seconds', $event ?? null, 'spm_limit')"
          />
          <SInput v-else type="number" :model-value="values[f.key]" :min="BULK_LIMITS[f.key][0]" :max="BULK_LIMITS[f.key][1]" :aria-label="f.label" :class="on[f.key] ? '' : 'opacity-60'" :error="invalid.includes(f.key)" :disabled="!!fixed[f.key]" @update:model-value="set(f.key, $event as number | null, f.key)" />
          <SHint v-if="fixed[f.key]" size="xs" tone="warning">{{ t('accounts.bulk.controlFixed', { n: fixed[f.key] }) }}</SHint>
          <SHint v-else size="xs">{{ f.hint }}</SHint>
        </div>
        <div class="space-y-1" data-testid="bulk-field-auto_disable">
          <SCheckbox v-model="on.auto_disable" size="sm" :disabled="!!fixed.auto_disable">{{ t('accounts.autoDisable') }}</SCheckbox>
          <SSwitch :model-value="values.auto_disable" :label="values.auto_disable ? t('common.yes') : t('common.no')" :disabled="!!fixed.auto_disable" @update:model-value="set('auto_disable', $event, 'auto_disable')" />
          <SHint v-if="fixed.auto_disable" size="xs" tone="warning">{{ t('accounts.bulk.controlFixed', { n: fixed.auto_disable }) }}</SHint>
        </div>
        <div class="space-y-1 sm:col-span-2" data-testid="bulk-field-groups">
          <SCheckbox v-model="on.groups" size="sm" :disabled="!caps.bindGroups.value">{{ t('accounts.groups') }}</SCheckbox>
          <SSegmented :model-value="values.groupMode" :options="groupModes" size="sm" :aria-label="t('accounts.bulk.groupModeLabel')" :disabled="!caps.bindGroups.value" @update:model-value="set('groupMode', $event as GroupMode, 'groups')" />
          <GroupPicker :model-value="values.group_ids" multiple :disabled="!caps.bindGroups.value" @update:model-value="set('group_ids', $event as number[], 'groups')" />
          <SHint size="xs" :tone="caps.bindGroups.value ? undefined : 'warning'">{{ caps.bindGroups.value ? t(`accounts.bulk.groupModeHint.${values.groupMode}`) : t('accounts.groupsLocked') }}</SHint>
        </div>
        <div class="space-y-1 sm:col-span-2" data-testid="bulk-field-proxy">
          <SCheckbox v-model="on.proxy" size="sm">{{ t('accounts.proxy') }}</SCheckbox>
          <ProxyPicker :model-value="values.proxy_id" @update:model-value="set('proxy_id', $event, 'proxy')" />
          <SHint size="xs">{{ t('accounts.bulk.proxyHint') }}</SHint>
        </div>
        <div class="space-y-1 sm:col-span-2" data-testid="bulk-field-protocols">
          <SCheckbox v-model="on.protocols" size="sm" :disabled="!sharedType">{{ t('accounts.protocols.bulk.own') }}</SCheckbox>
          <SHint v-if="!sharedType" size="xs" tone="warning" data-testid="bulk-protocols-mixed">{{ t('accounts.protocols.bulk.mixedTypes') }}</SHint>
          <template v-else>
            <OwnProtocolsField :editor="protocolEdit" :class="on.protocols ? '' : 'opacity-60'" />
            <SHint size="xs">{{ t('accounts.protocols.bulk.replaceHint') }}</SHint>
          </template>
        </div>
        <div v-if="sharedType" class="space-y-1 sm:col-span-2" data-testid="bulk-field-model_protocols">
          <SCheckbox v-model="on.model_protocols" size="sm">{{ t('accounts.protocols.bulk.rules') }}</SCheckbox>
          <ModelProtocolList :editor="protocolEdit" :options="protocolModels" :class="on.model_protocols ? '' : 'opacity-60'" />
          <ProtocolIssues :issues="protocolIssues" />
          <SHint size="xs">{{ t('accounts.protocols.bulk.replaceHint') }}</SHint>
        </div>
      </fieldset>
      <SProgress v-if="bulk.running.value || bulk.done.value" :value="bulk.done.value" :max="bulk.total.value || 1" :label="t('accounts.bulk.progress', { done: bulk.done.value, total: bulk.total.value })" show-value />
      <div v-if="failures.length" class="rounded-lg border border-danger-200 p-3 dark:border-danger-800" data-testid="bulk-failures">
        <p class="mb-1 text-sm font-semibold text-danger-600">{{ t('accounts.bulk.failures', { n: failures.length }) }}</p>
        <ul class="max-h-40 space-y-0.5 overflow-auto text-xs">
          <li v-for="f in failures" :key="f.item.id"><span class="font-mono text-fg-subtle">#{{ f.item.id }}</span> {{ f.item.name }}：<span class="text-danger-600">{{ errorMessage(f.error) }}</span></li>
        </ul>
      </div>
    </div>
    <template #footer>
      <SButton v-if="bulk.running.value" variant="danger" icon="stop" @click="bulk.stop()">{{ t('accounts.modelTest.stop') }}</SButton>
      <SButton v-else @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
      <SButton variant="primary" :loading="bulk.running.value" :disabled="!anyOn || invalid.length > 0 || protocolsInvalid || !accounts.length" data-testid="bulk-edit-apply" @click="apply">{{ t('accounts.bulk.apply', { n: accounts.length }) }}</SButton>
    </template>
  </SModal>
</template>
