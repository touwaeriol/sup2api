<script setup lang="ts">
// The store settings' store dialog: a new third-party store (name, GitHub
// repository and the branch of its default channel) or a store's new name.
// The market shows only the name (CONTRACTS §136).
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SField, SFormModal, SInput, toast } from '@sub2api/ui'
import type { MarketStore, MarketStoreCreate } from '@/api/marketTypes'
import { applyFormError } from '@/utils/formErrors'
import { validBranch, validRepo } from './storeRules'

/** null: a new store; a store: renaming it. */
const props = defineProps<{ open: boolean; store: MarketStore | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [] }>()
const { t } = useI18n()

const form = reactive<MarketStoreCreate>({ name: '', repo: '', branch: 'main' })
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const title = computed(() => (props.store ? t('market.stores.editTitle') : t('market.stores.addTitle')))

watch(
  () => props.open,
  open => {
    if (!open) return
    Object.assign(form, { name: props.store?.name ?? '', repo: '', branch: 'main' })
    errors.value = {}
  }
)

function check(): boolean {
  errors.value = {}
  if (!form.name.trim()) errors.value.name = t('common.required')
  if (!props.store && !validRepo(form.repo)) errors.value.repo = t('market.stores.repoInvalid')
  if (!props.store && !validBranch(form.branch)) errors.value.branch = t('market.settings.branchInvalid')
  return !Object.keys(errors.value).length
}

async function submit() {
  if (!check()) return
  saving.value = true
  try {
    const name = form.name.trim()
    if (props.store) await api.patch(`/market/stores/${props.store.id}`, { name })
    else await api.post('/market/stores', { name, repo: form.repo.trim(), branch: form.branch.trim() })
    toast(props.store ? t('common.updated') : t('common.created'), 'success')
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
  <SFormModal :open="open" :title="title" width="sm" :loading="saving" data-testid="store-form" @update:open="v => emit('update:open', v)" @submit="submit">
    <SField :label="t('market.stores.name')" :hint="store ? undefined : t('market.stores.nameHint')" :error="errors.name" required>
      <SInput v-model="form.name" data-testid="store-name" />
    </SField>
    <template v-if="!store">
      <SField :label="t('market.stores.repo')" :error="errors.repo" required>
        <SInput v-model="form.repo" class="font-mono" placeholder="owner/name" data-testid="store-repo-input" />
      </SField>
      <SField :label="t('market.stores.branch')" :hint="t('market.stores.branchHint')" :error="errors.branch" required>
        <SInput v-model="form.branch" class="font-mono" data-testid="store-branch" />
      </SField>
    </template>
  </SFormModal>
</template>
