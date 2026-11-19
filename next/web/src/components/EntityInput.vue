<script setup lang="ts">
// An entity picker as a form control: v-model is the value the form stores,
// where the pickers themselves speak in id strings. `multiple` is a list of
// ids (EntityMultiPicker), else one id (EntityPicker); `numeric` ids are
// stored as numbers and none as null, else as strings and none as ''. Read,
// null, '' and 0 are all "none". GroupPicker and ProxyPicker are this with
// their kind.
import { computed } from 'vue'
import EntityMultiPicker from './EntityMultiPicker.vue'
import EntityPicker from './EntityPicker.vue'
import type { EntityKind } from './entity/entities'

type Id = number | string
const props = defineProps<{
  kind: EntityKind
  modelValue: Id[] | Id | null | undefined
  multiple?: boolean
  numeric?: boolean
  placeholder?: string
  mine?: boolean
  /** `multiple`: no more can be added once this many are picked. */
  max?: number
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: Id[] | Id | null] }>()

const ids = computed(() => {
  const v = props.modelValue
  if (Array.isArray(v)) return v.map(String)
  return v ? [String(v)] : []
})
const stored = (id: string): Id => (props.numeric ? Number(id) : id)
function one(id: string) {
  if (id) return emit('update:modelValue', stored(id))
  emit('update:modelValue', props.numeric ? null : '')
}
</script>

<template>
  <EntityMultiPicker
    v-if="multiple"
    :kind="kind"
    :ids="ids"
    :placeholder="placeholder"
    :mine="mine"
    :max="max"
    :disabled="disabled"
    @update:ids="emit('update:modelValue', $event.map(stored))"
  />
  <!-- As the multiple one: the width of its place, not the text field's own (which would widen a narrow form past its edge). -->
  <div v-else class="grid min-w-48 grid-cols-1">
    <EntityPicker :id="ids[0] ?? ''" :kind="kind" :placeholder="placeholder" :mine="mine" :disabled="disabled" @update:id="one" />
  </div>
</template>
