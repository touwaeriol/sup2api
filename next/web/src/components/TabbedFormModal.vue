<script setup lang="ts" generic="K extends string">
import { SFormModal, STabbedForm, type FormTab, type TabbedForm } from '@sub2api/ui'
import { useTabbedSubmit } from '@/composables/useTabbedSubmit'

// The dialog of an editor whose tabs are saved in one request: SFormModal
// around STabbedForm (tabs on top) with the save of `form` (useTabbedForm).
// Save sends the merged body through `send`; once it succeeds the dialog
// says `saved` and closes. A refused save marks its tabs and stays open,
// closing with something not saved asks first, and only the Save button
// submits (Enter in a field of a table must not save it all). v-model:tab
// is the tab in front, the one `form` was made with. Every tab stays mounted
// while the dialog is open, so the tables keep their drafts while another
// tab shows; `minHeight` (a class) keeps it from collapsing on a short tab.
// The slots: one per tab (`{ tab }`), `header`, `before` (above the tabs),
// `footer-start` and `footer` of SFormModal. The test ids are `testid`, and
// `<testid>-tabs`, `<testid>-submit`.
const props = defineProps<{
  open: boolean
  title: string
  tabs: FormTab[]
  form: TabbedForm
  send: (body: Record<string, unknown>) => Promise<unknown>
  submitText: string
  testid: string
  minHeight?: string
}>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved'): void }>()
const tab = defineModel<K>('tab', { required: true })
defineSlots<{ default(props: { tab: string }): unknown; header?(): unknown; before?(): unknown; 'footer-start'?(): unknown; footer?(): unknown }>()

const close = () => emit('update:open', false)
const { submit, leave } = useTabbedSubmit(props.form, {
  send: (body) => props.send(body),
  done() {
    emit('saved')
    close()
  },
  close
})
</script>

<template>
  <SFormModal
    :open="open"
    :title="title"
    width="2xl"
    :loading="form.saving.value"
    :submit-text="submitText"
    :submit-attrs="{ 'data-testid': `${testid}-submit` }"
    :enter-submit="false"
    :data-testid="testid"
    @update:open="leave"
    @submit="submit"
  >
    <template v-if="$slots.header" #header><slot name="header" /></template>
    <slot name="before" />
    <STabbedForm v-if="open" :model-value="tab" :tabs="tabs" :form="form" :aria-label="title" :class="minHeight" :data-testid="`${testid}-tabs`" @update:model-value="tab = $event as K">
      <template #default="{ tab: key }"><slot :tab="key" /></template>
    </STabbedForm>
    <template v-if="$slots['footer-start']" #footer-start><slot name="footer-start" /></template>
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </SFormModal>
</template>
