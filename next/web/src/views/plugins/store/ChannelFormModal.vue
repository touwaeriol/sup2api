<script setup lang="ts">
// The store settings' channel dialog: a new channel of the store (name and
// branch) or a channel's new name; the branch is a git ref name segment. A
// new channel whose branch has no index yet is saved, with a warning
// (CONTRACTS §137.6).
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SField, SFormModal, SInput, toast } from '@sub2api/ui'
import type { MarketSource, MarketSourceCreate } from '@/api/marketTypes'
import { applyFormError } from '@/utils/formErrors'
import { validBranch } from './storeRules'

/** channel null: a new channel of the store; a channel: renaming it. */
const props = defineProps<{ open: boolean; storeId: number; channel: MarketSource | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [] }>()
const { t } = useI18n()

const form = reactive<MarketSourceCreate>({ name: '', branch: '' })
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const title = computed(() => (props.channel ? t('market.settings.editTitle') : t('market.settings.addTitle')))

watch(
  () => props.open,
  open => {
    if (!open) return
    Object.assign(form, { name: props.channel?.name ?? '', branch: props.channel?.branch ?? '' })
    errors.value = {}
  }
)

function check(): boolean {
  errors.value = {}
  if (!form.name.trim()) errors.value.name = t('common.required')
  if (!props.channel && !validBranch(form.branch)) errors.value.branch = t('market.settings.branchInvalid')
  return !Object.keys(errors.value).length
}

async function submit() {
  if (!check()) return
  let noIndex = false
  saving.value = true
  try {
    const name = form.name.trim()
    if (props.channel) await api.patch(`/market/sources/${props.channel.id}`, { name })
    else noIndex = (await api.post<MarketSource>(`/market/stores/${props.storeId}/sources`, { name, branch: form.branch.trim() }))?.warning === 'no_index'
    toast(props.channel ? t('common.updated') : t('common.created'), 'success')
    if (noIndex) toast(t('market.settings.noIndex', { branch: form.branch.trim() }), 'warning', 8000)
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
  <SFormModal :open="open" :title="title" width="sm" :loading="saving" data-testid="channel-form" @update:open="v => emit('update:open', v)" @submit="submit">
    <SField :label="t('market.settings.name')" :error="errors.name" required>
      <SInput v-model="form.name" data-testid="channel-name" />
    </SField>
    <SField v-if="!channel" :label="t('market.settings.branch')" :hint="t('market.settings.branchHint')" :error="errors.branch" required>
      <SInput v-model="form.branch" class="font-mono" placeholder="develop" data-testid="channel-branch" />
    </SField>
  </SFormModal>
</template>
