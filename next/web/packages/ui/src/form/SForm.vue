<script setup lang="ts">
// Form container of useForm: provides the form to the SField / SSettingRow
// frames inside (their `prop`), and emits `submit` (Enter, a submit button)
// only after validate() passed. `disabled` disables every control inside (a
// <fieldset>). Draws nothing of its own. Inside an element that already is
// a <form> (SFormModal, SCredentialForm in the account editor) it renders no
// second <form>: submitting is the outer form's.
import { inject, provide } from 'vue'
import { FORM_KEY, IN_FORM_KEY } from './context'
import type { FormApi } from './useForm'

const props = defineProps<{ form: FormApi<any>; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'submit'): void }>()
const nested = inject(IN_FORM_KEY, false)
provide(FORM_KEY, props.form)
provide(IN_FORM_KEY, true)

async function submit() {
  if (await props.form.validate()) emit('submit')
}
</script>

<template>
  <fieldset v-if="nested" class="s-form" :disabled="disabled"><slot /></fieldset>
  <form v-else novalidate @submit.prevent="submit">
    <fieldset class="s-form" :disabled="disabled"><slot /></fieldset>
    <button type="submit" hidden tabindex="-1" aria-hidden="true" />
  </form>
</template>

<style scoped>
.s-form {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}
</style>
