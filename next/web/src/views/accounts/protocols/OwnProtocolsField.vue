<script setup lang="ts">
// 本平台协议 (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1): which protocols of the
// account type's own platforms the account serves. "All" ticks every row and
// greys them (saved as null); "chosen" lets each one be unticked, and says how
// many stay closed. A protocol the type adapts to another endpoint of the
// platform names the target. A chosen id that is no own protocol of the type
// (its endpoint is gone) has no row: it shows as a warning chip that can only
// be removed, since the server refuses it. Used by the account editor and the
// bulk edit.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SBadge, SCheckbox, SHint, SSegmented } from '@sub2api/ui'
import { columnState, openColumns, unknownOwnProtocols } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import UnknownProtocolChips from './UnknownProtocolChips.vue'
import type { ProtocolEditor, ProtocolMode } from './useProtocolEdit'

const props = defineProps<{
  editor: ProtocolEditor
  /** Messages of the server's errors of `protocols`. */
  errors?: string[]
  disabled?: boolean
}>()
const { t } = useI18n()
const labels = useProtocolLabels()
const { columns, mode, protocols } = props.editor

const modes = computed(() => (['all', 'custom'] as ProtocolMode[]).map(m => ({ value: m, label: t(`accounts.protocols.own.${m}`) })))
const fixed = computed(() => props.disabled || mode.value === 'all')
const closed = computed(() => columns.value.own.length - openColumns(columns.value, protocols.value).length)
const unknown = computed(() => unknownOwnProtocols(protocols.value, columns.value))
</script>

<template>
  <div class="space-y-2" data-testid="own-protocols">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <SHint size="xs">{{ t('accounts.protocols.own.modeHint') }}</SHint>
      <SSegmented size="sm" :options="modes" :model-value="mode" :disabled="disabled" :aria-label="t('accounts.protocols.own.title')" data-testid="own-protocols-mode" @update:model-value="editor.setMode($event as ProtocolMode)" />
    </div>
    <SHint v-if="!columns.own.length" size="xs" data-testid="own-protocols-none">{{ t('accounts.protocols.own.none') }}</SHint>
    <ul v-else class="divide-y divide-line rounded-xl border border-line">
      <li v-for="c in columns.own" :key="c.key" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2" :data-protocol="c.key">
        <SCheckbox
          :model-value="columnState(protocols, c) === 'on'"
          :indeterminate="columnState(protocols, c) === 'partial'"
          :disabled="fixed"
          :label="labels.columnName(c)"
          class="min-w-[11rem] font-medium"
          @update:model-value="editor.toggleOwn(c)"
        />
        <SBadge v-if="c.via === 'adapted'" tone="info" :title="(c.hops || []).join(' → ') || undefined" data-testid="own-protocol-adapted">{{ t('accounts.protocols.own.adapted', { target: labels.targetName(c) }) }}</SBadge>
        <SBadge v-else tone="success">{{ t('accounts.protocols.own.native') }}</SBadge>
        <code class="min-w-0 truncate font-mono text-xs text-fg-subtle">{{ c.method }} {{ c.path }}</code>
      </li>
    </ul>
    <SAlert v-if="unknown.length" tone="warning" dense data-testid="own-protocols-unknown">
      <span class="flex flex-wrap items-center gap-1.5">
        {{ t('accounts.protocols.own.unknown') }}
        <UnknownProtocolChips :ids="unknown" :disabled="disabled" @remove="editor.removeOwn" />
      </span>
    </SAlert>
    <SHint v-for="line in errors" :key="line" tone="danger" size="xs" data-testid="own-protocols-error">{{ line }}</SHint>
    <SAlert v-if="mode === 'custom' && closed > 0" tone="warning" dense data-testid="own-protocols-closed">{{ t('accounts.protocols.own.closed', { n: closed }) }}</SAlert>
  </div>
</template>
