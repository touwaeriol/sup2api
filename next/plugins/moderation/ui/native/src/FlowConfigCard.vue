<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SBadge, SCard, SField, SHint, SInput, SLink, SSelect, SSwitch, STagInput } from '@sub2api/ui'
import FlowTimeoutField from './FlowTimeoutField.vue'
import { enumOptions } from './format'
import { useModHost } from './host'
import { SCOPES, type Scope, type Spec } from './types'
import { NAME_MAX } from './flowOps'

// Name, enabled switch, bound groups (read-only, with the count of account
// bindings), sample rate, model scope, moderated scope and total timeout of a flow (new-api
// flow-config-card.tsx). The enabled switch is its own column here (not in
// the spec) and saves at once.
const props = defineProps<{
  name: string
  enabled: boolean
  draft: Spec
  /** names of the groups bound to the saved flow (edited in the list) */
  groups: string[]
  /** account bindings of the saved flow (the bindings tab) */
  accounts: number
  autoMs: number
  isNew: boolean
  disabled: boolean
  enabledBusy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:name', v: string): void
  (e: 'update:enabled', v: boolean): void
  (e: 'update:draft', v: Spec): void
  (e: 'go-list'): void
}>()
const t = useModHost().t

const filterOptions = computed(() => enumOptions('modelFilter', ['all', 'only', 'exclude']))
const scopeOptions = computed(() => enumOptions('flows.scope', SCOPES))
/** A spec saved before the scope existed moderates everything. */
const scope = computed<Scope>(() => props.draft.scope ?? 'all')
const patch = (p: Partial<Spec>) => emit('update:draft', { ...props.draft, ...p })
const setFilter = (p: Partial<Spec['model_filter']>) => patch({ model_filter: { ...props.draft.model_filter, ...p } })

function setRate(v: unknown) {
  // an empty box stays invalid (NaN) instead of becoming 0, so validation reports it
  patch({ sample_rate: v === null || v === '' ? Number.NaN : Number(v) })
}
</script>

<template>
  <SCard :title="t('flows.configTitle')" :subtitle="t('flows.configDesc')">
    <div class="mod-config-grid">
      <SField :label="t('flows.col.name')" required>
        <SInput :model-value="name" :maxlength="NAME_MAX" :disabled="disabled" :placeholder="t('flows.namePlaceholder')" @update:model-value="emit('update:name', String($event ?? ''))" />
      </SField>

      <SField :label="t('enabled')">
        <div class="mod-config-inline">
          <SSwitch :model-value="enabled" :disabled="disabled || enabledBusy" :aria-label="t('enabled')" @update:model-value="emit('update:enabled', $event)" />
          <SHint inline size="xs">{{ enabled ? t('flows.enabledOn') : t('flows.enabledOff') }}</SHint>
        </div>
      </SField>

      <SField :label="t('flows.timingTitle')" class="mod-config-wide">
        <SHint size="xs">{{ t('flows.timingDesc') }}</SHint>
      </SField>

      <SField :label="t('flows.boundGroups')">
        <div class="mod-config-inline mod-wrap">
          <SBadge v-for="g in groups" :key="g">{{ g }}</SBadge>
          <span v-if="accounts" class="mod-sub">{{ groups.length ? t('flows.accountBindings', { n: accounts }) : t('flows.accountsOnly', { n: accounts }) }}</span>
          <span v-else-if="!groups.length" class="mod-text-warning">{{ isNew ? t('flows.noGroupsNew') : t('flows.noGroupsWarn') }}</span>
          <SLink v-if="!isNew" as="button" @click="emit('go-list')">{{ t('flows.editInList') }}</SLink>
        </div>
      </SField>

      <SField :label="t('flows.sampleRate')" :hint="t('flows.sampleRateHint')">
        <SInput
          :model-value="Number.isNaN(draft.sample_rate) ? null : draft.sample_rate"
          type="number"
          :min="0"
          :max="100"
          :step="1"
          class="mod-config-num"
          :disabled="disabled"
          @update:model-value="setRate"
        />
      </SField>

      <SField :label="t('flows.modelFilter')" :hint="t('flows.modelsHint')" class="mod-config-wide">
        <div class="mod-config-inline mod-config-models">
          <SSelect :model-value="draft.model_filter.type" :options="filterOptions" :disabled="disabled" :aria-label="t('flows.modelFilter')" class="mod-config-filter" @update:model-value="setFilter({ type: $event as Spec['model_filter']['type'] })" />
          <STagInput
            v-if="draft.model_filter.type !== 'all'"
            :model-value="draft.model_filter.models"
            :disabled="disabled"
            :placeholder="t('flows.modelsPlaceholder')"
            :aria-label="t('flows.models')"
            class="mod-grow"
            @update:model-value="setFilter({ models: $event })"
          />
        </div>
      </SField>

      <SField :label="t('flows.scopeTitle')" :hint="t('flows.scopeHint')" class="mod-config-wide" data-testid="mod-flow-scope">
        <div class="mod-form">
          <SSelect :model-value="scope" :options="scopeOptions" :disabled="disabled" :aria-label="t('flows.scopeTitle')" @update:model-value="patch({ scope: $event as Scope })" />
          <SAlert v-if="scope === 'all' || scope === 'all_but_own'" tone="warning">{{ t('flows.scopeWarn') }}</SAlert>
        </div>
      </SField>

      <FlowTimeoutField :model-value="draft.total_timeout_ms" :auto-ms="autoMs" :disabled="disabled" @update:model-value="patch({ total_timeout_ms: $event })" />
    </div>
  </SCard>
</template>
