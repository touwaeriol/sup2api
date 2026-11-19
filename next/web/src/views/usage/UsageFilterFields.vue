<script setup lang="ts">
// The search fields of UsageFilters, for the first row and for More filters:
// an EntityPicker for each of `fields` that picks an entity (ENTITY_FIELDS), a
// select for those picked from a list (see useUsageSearchLabels), a text box
// for the rest, offering its suggestions (a <datalist>) where it has any. They show the search draft `values` and report each change
// with the parameters it sets: a pick (a picker's, a select's) as `pick`,
// which the search applies at once, typed text as `update`, which waits for
// Search. The pickers follow the draft too: the API keys are those of the
// picked user, the accounts of the picked group come first, and the model
// picker also offers `modelOptions`. A picker that is set carries data-set,
// for a layout that gives it the room to show its name. Without `textSearch`
// an entity is picked only, never searched by name (the account view,
// CONTRACTS §103.5).
import { computed, useId } from 'vue'
import { SInput, SSelect } from '@sub2api/ui'
import EntityPicker from '@/components/EntityPicker.vue'
import { ENTITY_FIELDS, entityPatch, fieldParams, useUsageSearchLabels } from './usageFilters'

const props = withDefaults(
  defineProps<{
    fields: string[]
    values: Record<string, string>
    /** The caller's own records: the pickers list only what is theirs. */
    mine?: boolean
    protocolOptions?: string[]
    /** The models under the current search. */
    modelOptions?: string[]
    textSearch?: boolean
  }>(),
  { mine: false, protocolOptions: () => [], modelOptions: () => [], textSearch: true }
)
const emit = defineEmits<{ update: [patch: Record<string, string>]; pick: [patch: Record<string, string>] }>()
const { label, options, suggestions } = useUsageSearchLabels(() => props.protocolOptions, () => props.values.protocol ?? '')
const listId = `usage-filter-${useId()}`
const rows = computed(() =>
  props.fields.map(field => ({ field, entity: ENTITY_FIELDS[field], set: fieldParams(field).some(param => !!props.values[param]) }))
)
/** A parameter's draft value; undefined for a parameter the field does not have. */
const draft = (param: string | undefined) => (param === undefined ? undefined : (props.values[param] ?? ''))
const scopes = computed<Record<string, Record<string, string | undefined>>>(() => ({
  api_key: { user_id: props.values.user_id },
  account: { prefer_group_id: props.values.group_id }
}))
const extras = computed<Record<string, Array<{ id: string; name: string }>>>(() => ({
  model: props.modelOptions.map(name => ({ id: name, name }))
}))
</script>

<template>
  <template v-for="{ field, entity, set } in rows" :key="field">
    <EntityPicker
      v-if="entity"
      :id="draft(entity.id)"
      :kind="entity.kind"
      :text="textSearch ? draft(entity.text) : undefined"
      size="sm"
      :mine="mine"
      :scope="scopes[field]"
      :extra="extras[field]"
      :data-testid="`filter-${field}`"
      :data-set="set || undefined"
      @change="emit('pick', entityPatch(field, $event))"
    />
    <SSelect
      v-else-if="options[field]"
      :model-value="values[field] ?? ''"
      size="sm"
      :aria-label="label(field)"
      :options="options[field]"
      :data-testid="`filter-${field}`"
      @update:model-value="emit('pick', { [field]: String($event ?? '') })"
    />
    <SInput
      v-else
      :model-value="values[field] ?? ''"
      size="sm"
      :icon="field === 'client_ip' ? 'globe' : undefined"
      :placeholder="label(field)"
      :title="label(field)"
      :aria-label="label(field)"
      :maxlength="256"
      :mono="field === 'client_ip'"
      :list="suggestions[field] ? `${listId}-${field}` : undefined"
      :data-testid="`filter-${field}`"
      @update:model-value="emit('update', { [field]: String($event ?? '') })"
    />
    <datalist v-if="!entity && !options[field] && suggestions[field]" :id="`${listId}-${field}`" :data-testid="`filter-${field}-suggestions`">
      <option v-for="o in suggestions[field]" :key="o.value" :value="o.value" :label="o.label" />
    </datalist>
  </template>
</template>
