<script setup lang="ts">
// Form dialog: SModal + <form> + the usual "Cancel / Save" footer, so a page
// only writes its fields and a submit handler.
//   <SFormModal v-model:open="editOpen" :title="…" :loading="saving" @submit="save">
//     <SField …><SInput v-model="form.name" /></SField>
//   </SFormModal>
// Enter in a field and the submit button both emit `submit` (native form
// submission, prevented); `enterSubmit` false leaves Enter alone, for dialogs
// that must only submit on a click (money, destructive or multi-step choices).
// While `loading` the dialog cannot be dismissed and the submit button spins.
// `submitText` defaults to "Save"; `submitVariant` danger for destructive
// forms; `submitDisabled` blocks submitting.
// `#footer` replaces the whole footer, `#footer-start` adds content on its
// left (a hint, a secondary action). `#header` is SModal's. Other attributes
// (data-testid, …) land on the <form>.
// On phones it takes SModal's `mobile` form; Cancel and Submit share the
// footer width, with #footer-start on its own row above them. An SForm in the
// default slot renders no second <form> (validate it in the submit handler).
import { provide } from 'vue'
import { useUiT } from './i18n'
import { IN_FORM_KEY } from './form/context'
import SButton from './SButton.vue'
import SModal from './SModal.vue'
import type { ModalMobile } from './types'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    width?: 'sm' | 'md' | 'lg' | 'xl' | '2xl'
    loading?: boolean
    submitText?: string
    cancelText?: string
    submitVariant?: 'primary' | 'danger'
    submitDisabled?: boolean
    /** Enter in a field submits the form. Default true. */
    enterSubmit?: boolean
    /** Extra attributes of the submit button (data-testid, …). */
    submitAttrs?: Record<string, unknown>
    /** Extra attributes of the cancel button. */
    cancelAttrs?: Record<string, unknown>
    persistent?: boolean
    describedby?: string
    /** SModal's form on phones. */
    mobile?: ModalMobile
    /** SModal's `fixed`: one size whatever the content. */
    fixed?: boolean
  }>(),
  { width: 'md', submitVariant: 'primary', enterSubmit: true, submitText: undefined, cancelText: undefined, title: undefined, describedby: undefined, mobile: 'auto' }
)
defineOptions({ inheritAttrs: false })
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'submit'): void; (e: 'close'): void }>()
/** Cancel closes like ×, Esc and the overlay do (SModal emits both). */
function cancel() {
  emit('update:open', false)
  emit('close')
}
const t = useUiT()
provide(IN_FORM_KEY, true)

function submit() {
  if (props.loading || props.submitDisabled) return
  emit('submit')
}
</script>

<template>
  <SModal
    :open="open"
    :title="title"
    :width="width"
    :persistent="persistent || loading"
    :closable="!loading"
    :describedby="describedby"
    :mobile="mobile"
    :fixed="fixed"
    @update:open="emit('update:open', $event)"
    @close="emit('close')"
  >
    <template v-if="$slots.header" #header><slot name="header" /></template>
    <form class="space-y-4" novalidate v-bind="$attrs" @submit.prevent="enterSubmit && submit()">
      <slot />
      <!-- Enter in a field submits through this (implicit submission needs a submit button inside the form). -->
      <button v-if="enterSubmit" type="submit" class="hidden" tabindex="-1" aria-hidden="true" />
    </form>
    <template #footer>
      <slot name="footer">
        <div v-if="$slots['footer-start']" class="mr-auto flex min-w-0 items-center gap-2 max-md:!basis-full"><slot name="footer-start" /></div>
        <SButton :disabled="loading" v-bind="cancelAttrs" @click="cancel">{{ cancelText || t('ui.cancel') }}</SButton>
        <SButton :variant="submitVariant" :loading="loading" :disabled="submitDisabled" v-bind="submitAttrs" @click="submit">
          {{ submitText || t('ui.save') }}
        </SButton>
      </slot>
    </template>
  </SModal>
</template>
