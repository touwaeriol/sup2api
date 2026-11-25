<script setup lang="ts">
import { watch } from 'vue'
import { SDateTimeInput, SField, SForm, SFormModal, SInput, type Rules } from '@sub2api/ui'
import { paymentApi, usePaymentHost } from '../host'
import { useModalForm } from './actions'

// Generates a batch of redeem codes; the plain codes go to the result modal
// (the list only ever shows them masked).
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; generated: [codes: string[]] }>()
const host = usePaymentHost()
const t = host.t

interface Draft extends Record<string, unknown> {
  count?: number
  value?: number
  expires_at?: string
  notes?: string
}

const future = (v: unknown) => !v || Date.parse(String(v)) > Date.now() || t('admin.futureTime')
const rules: Rules<Draft> = {
  count: { required: true, type: 'integer', min: 1, max: 1000 },
  value: [{ required: true, type: 'number' }, { validator: (v) => Number(v) > 0 || t('admin.positive') }],
  expires_at: { validator: future },
  notes: { max: 200 }
}
const { form, saving, reset, submit } = useModalForm<Draft>(() => ({ count: 10, value: 10 }), rules)

watch(
  () => props.open,
  (open) => open && reset()
)

async function save() {
  const res = await submit((d) => paymentApi.createRedeemCodes({ count: Number(d.count), value: Number(d.value), expires_at: d.expires_at, notes: d.notes?.trim() || undefined }))
  if (!res) return
  emit('update:open', false)
  emit('generated', res.codes)
}
</script>

<template>
  <SFormModal :open="open" :title="t('admin.redeem.form.title')" :loading="saving" :submit-text="t('admin.redeem.form.submit')" @update:open="emit('update:open', $event)" @submit="save">
    <SForm :form="form">
      <SField prop="count" :label="t('admin.redeem.form.count')" :hint="t('admin.redeem.form.countHint')">
        <SInput type="number" min="1" max="1000" step="1" v-bind="form.bindNumber('count')" />
      </SField>
      <SField prop="value" :label="t('admin.redeem.form.value')" :hint="t('admin.redeem.form.valueHint')">
        <SInput type="number" min="0" step="0.01" suffix="USD" v-bind="form.bindNumber('value')" />
      </SField>
      <SField prop="expires_at" :label="t('admin.redeem.form.expires')" :hint="t('admin.redeem.form.expiresHint')">
        <SDateTimeInput v-bind="form.bind('expires_at')" />
      </SField>
      <SField prop="notes" :label="t('admin.redeem.form.notes')">
        <SInput :placeholder="t('admin.redeem.form.notesPlaceholder')" v-bind="form.bind('notes')" />
      </SField>
    </SForm>
  </SFormModal>
</template>
