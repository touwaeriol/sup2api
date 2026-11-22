<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SCode, SCopyButton, SHint, SModal } from '@sub2api/ui'
import type { Account } from '@/api/types'
import { notifyError } from '@/utils/errors'

// The credentials of an account in plain text, with a warning and a copy
// button. reveal(account) asks the server for them and opens once they
// come; closing forgets them.
const { t } = useI18n()
const open = ref(false)
const revealed = ref<Record<string, unknown> | null>(null)
const text = computed(() => JSON.stringify(revealed.value, null, 2))

async function reveal(a: Pick<Account, 'id'>) {
  try {
    revealed.value = await api.post<Record<string, unknown>>(`/accounts/${a.id}/credentials/reveal`)
    open.value = true
  } catch (e) {
    notifyError(e)
  }
}
defineExpose({ reveal })
</script>

<template>
  <SModal v-model:open="open" :title="t('accounts.revealCredentials')" width="lg" @close="revealed = null">
    <SHint tone="warning" class="mb-3">{{ t('accounts.revealWarning') }}</SHint>
    <SCode>{{ text }}</SCode>
    <template #footer>
      <SCopyButton :text="text" :label="t('common.copy')" variant="secondary" size="md" />
      <SButton variant="primary" @click="open = false">{{ t('common.close') }}</SButton>
    </template>
  </SModal>
</template>
