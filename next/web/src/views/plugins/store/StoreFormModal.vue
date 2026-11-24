<script setup lang="ts">
// The store settings' store dialog: the store's new name, the only thing of
// the official store that changes; the market shows only the name
// (CONTRACTS §137.6).
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SField, SFormModal, SInput, toast } from '@sub2api/ui'
import type { MarketStore } from '@/api/marketTypes'
import { applyFormError } from '@/utils/formErrors'

const props = defineProps<{ open: boolean; store: MarketStore | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [] }>()
const { t } = useI18n()

const name = ref('')
const errors = ref<Record<string, string>>({})
const saving = ref(false)

watch(
  () => props.open,
  open => {
    if (!open) return
    name.value = props.store?.name ?? ''
    errors.value = {}
  }
)

async function submit() {
  errors.value = name.value.trim() ? {} : { name: t('common.required') }
  if (errors.value.name || !props.store) return
  saving.value = true
  try {
    await api.patch(`/market/stores/${props.store.id}`, { name: name.value.trim() })
    toast(t('common.updated'), 'success')
    emit('update:open', false)
    emit('saved')
  } catch (e) {
    applyFormError(e, errors)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SFormModal :open="open" :title="t('market.stores.editTitle')" width="sm" :loading="saving" data-testid="store-form" @update:open="v => emit('update:open', v)" @submit="submit">
    <SField :label="t('market.stores.name')" :hint="t('market.stores.nameHint')" :error="errors.name" required>
      <SInput v-model="name" data-testid="store-name" />
    </SField>
  </SFormModal>
</template>
