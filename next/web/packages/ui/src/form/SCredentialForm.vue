<script setup lang="ts">
// Body of a native account form: SForm (inside the account editor's own
// <form>, so it renders none) with the errors no field claims listed on
// top, then the fields of the default slot.
import { provide } from 'vue'
import SAlert from '../SAlert.vue'
import SForm from './SForm.vue'
import { IN_FORM_KEY } from './context'
import type { FormApi } from './useForm'

defineProps<{ form: FormApi<any>; disabled?: boolean }>()
provide(IN_FORM_KEY, true)
</script>

<template>
  <SForm :form="form" :disabled="disabled">
    <div class="s-credential-form">
      <SAlert v-if="form.unclaimed.value.length" tone="danger" dense data-testid="credential-form-errors">
        <p v-for="(msg, i) in form.unclaimed.value" :key="i">{{ msg }}</p>
      </SAlert>
      <slot />
    </div>
  </SForm>
</template>

<style scoped>
.s-credential-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
</style>
