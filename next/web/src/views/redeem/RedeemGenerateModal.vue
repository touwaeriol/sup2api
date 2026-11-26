<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SDateTimeInput, SField, SForm, SFormModal, SInput, type Rules } from '@sub2api/ui'
import { useModalForm } from '@/composables/useModalForm'
import type { GenerateRequest, Generated } from './redeem'

// Generates a batch of redeem codes (POST /redeem-codes); the plain codes go
// to the result modal, the list only ever shows them masked.
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; generated: [codes: string[]] }>()
const { t } = useI18n()

interface Draft extends Record<string, unknown> {
  count?: number
  value?: number
  expires_at?: string
  notes?: string
}

const future = (v: unknown) => !v || Date.parse(String(v)) > Date.now() || t('redeem.form.future')
const rules: Rules<Draft> = {
  count: { required: true, type: 'integer', min: 1, max: 1000 },
  value: [{ required: true, type: 'number' }, { validator: v => Number(v) > 0 || t('redeem.form.positive') }],
  expires_at: { validator: future },
  notes: { max: 200 }
}

const { form, saving, reset, submit } = useModalForm<Draft>(() => ({ count: 10, value: 10 }), rules)
watch(
  () => props.open,
  open => open && reset()
)

const body = (d: Draft): GenerateRequest => ({ count: Number(d.count), value: Number(d.value), expires_at: d.expires_at || undefined, notes: d.notes?.trim() || undefined })

async function save() {
  const res = await submit(d => api.post<Generated>('/redeem-codes', body(d)))
  if (!res) return
  emit('update:open', false)
  emit('generated', res.codes)
}
</script>

<template>
  <SFormModal :open="open" :title="t('redeem.form.title')" :loading="saving" :submit-text="t('redeem.form.submit')" @update:open="emit('update:open', $event)" @submit="save">
    <SForm :form="form">
      <SField prop="count" :label="t('redeem.form.count')" :hint="t('redeem.form.countHint')">
        <SInput type="number" min="1" max="1000" step="1" v-bind="form.bindNumber('count')" />
      </SField>
      <SField prop="value" :label="t('redeem.form.value')" :hint="t('redeem.form.valueHint')">
        <SInput type="number" min="0" step="0.01" suffix="USD" v-bind="form.bindNumber('value')" />
      </SField>
      <SField prop="expires_at" :label="t('redeem.form.expires')" :hint="t('redeem.form.expiresHint')">
        <SDateTimeInput v-bind="form.bind('expires_at')" />
      </SField>
      <SField prop="notes" :label="t('redeem.form.notes')">
        <SInput :placeholder="t('redeem.form.notesPlaceholder')" v-bind="form.bind('notes')" />
      </SField>
    </SForm>
  </SFormModal>
</template>
