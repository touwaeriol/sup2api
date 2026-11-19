<script setup lang="ts">
// The protocols of a set of models as toggle chips: one per protocol the
// account type offers, own ones first, conversions marked. An id no column
// has (its endpoint is gone) stays as a warning chip that can only be removed.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SChip } from '@sub2api/ui'
import { allColumns, columnState, type ProtocolColumn, type ProtocolColumns } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import UnknownProtocolChips from './UnknownProtocolChips.vue'

const props = defineProps<{ columns: ProtocolColumns; ids: string[]; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'toggle', c: ProtocolColumn): void; (e: 'remove', protocol: string): void }>()
const { t } = useI18n()
const labels = useProtocolLabels()

const shown = computed(() => allColumns(props.columns))
const unknown = computed(() => {
  const known = new Set(shown.value.flatMap(c => c.ids))
  return props.ids.filter(id => !known.has(id))
})
const label = (c: ProtocolColumn) => (c.via === 'converted' ? t('accounts.protocols.rules.conversionChip', { name: labels.columnName(c) }) : labels.columnName(c))
</script>

<template>
  <div class="flex flex-wrap items-center gap-1.5" data-testid="protocol-chips">
    <SChip
      v-for="c in shown"
      :key="c.key"
      :pressed="columnState(ids, c) === 'on'"
      :tone="c.via === 'converted' || columnState(ids, c) === 'partial' ? 'warning' : 'neutral'"
      :disabled="disabled"
      :title="`${c.method} ${c.path}`"
      :data-protocol="c.key"
      @update:pressed="emit('toggle', c)"
    >{{ label(c) }}</SChip>
    <UnknownProtocolChips :ids="unknown" :disabled="disabled" @remove="emit('remove', $event)" />
  </div>
</template>
