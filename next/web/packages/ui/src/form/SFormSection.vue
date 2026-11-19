<script setup lang="ts">
// A section of a form (account forms, a plugin's own forms): a rule above,
// the title (h4) and an optional `help` line, then the fields of the default
// slot. `collapsed` puts the fields behind the title (SDisclosure
// "section"): closed at first, open by itself while one of `fields` (paths
// of the SForm around it) holds a value or an error, and as the user left
// it once they toggled it.
import { computed, inject, ref } from 'vue'
import SDisclosure from '../SDisclosure.vue'
import SHint from '../SHint.vue'
import SSectionTitle from '../SSectionTitle.vue'
import { isBlank } from '../value'
import { FORM_KEY } from './context'
import { overlaps } from './path'

const props = defineProps<{ title: string; help?: string; collapsed?: boolean; fields?: string[] }>()
const form = inject(FORM_KEY, null)
const toggled = ref<boolean>()
const filled = computed(
  () => !!form && (props.fields ?? []).some((p) => !isBlank(form.get(p)) || Object.keys(form.errors).some((e) => overlaps(e, p)))
)
const open = computed(() => toggled.value ?? filled.value)
</script>

<template>
  <SDisclosure
    v-if="collapsed"
    variant="section"
    class="s-form-section"
    data-testid="form-section"
    :label="title"
    :description="help"
    :open="open"
    @update:open="toggled = $event"
  >
    <div class="s-form-section-fields"><slot /></div>
  </SDisclosure>
  <div v-else class="s-form-section s-form-section-fields" data-testid="form-section">
    <div>
      <SSectionTitle :title="title" tag="h4" />
      <SHint v-if="help" size="xs">{{ help }}</SHint>
    </div>
    <slot />
  </div>
</template>

<style scoped>
.s-form-section {
  border-top: 1px solid rgb(var(--line));
  padding-top: 1rem;
}
.s-form-section-fields {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
</style>
