<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SChoiceCards, SField, SHint, SInput, SModal, SIcon, toast, type ChoiceOption } from '@sub2api/ui'
import type { UninstallResult } from '@/api/types'
import { notifyError } from '@/utils/errors'
import { displayPluginId, tablePrefix } from '../pluginUtil'

// DELETE /plugins/:key?purge=&purge_accounts= (CONTRACTS §5.7, §14.3).
// Accounts of the plugin's account types are kept (orphaned) unless
// purge_accounts is set; the response then reports accounts_deleted.
/** hasDatabase: the plugin declares a database, so purging drops its tables as well. */
const props = defineProps<{ open: boolean; pluginKey: string; name: string; hasDatabase: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'done'): void }>()
const { t } = useI18n()

/** Optional deletions picked in the dialog: 'purge' (the plugin's tables and KV data; kept without it) and / or 'accounts'. */
const purgeChoice = ref<ChoiceOption['value'][]>([])
const purge = computed(() => purgeChoice.value.includes('purge'))
const purgeAccounts = computed(() => purgeChoice.value.includes('accounts'))
const typed = ref('')
const busy = ref(false)
/** Set after a successful uninstall with purge_accounts: the modal shows the result. */
const result = ref<{ accountsDeleted: number } | null>(null)
/** The plugin id as people see it, which the dialog asks to type. */
const shownId = computed(() => displayPluginId(props.pluginKey))
const matches = computed(() => typed.value.trim() === shownId.value)

const purgeOptions = computed<ChoiceOption[]>(() => [
  {
    value: 'purge',
    label: props.hasDatabase ? t('plugins.uninstall.purge', { prefix: tablePrefix(props.pluginKey) }) : t('plugins.uninstall.purgeKv'),
    description: props.hasDatabase ? t('plugins.uninstall.purgeHint') : t('plugins.uninstall.purgeHintKv'),
    icon: 'trash',
    tone: 'danger'
  },
  {
    value: 'accounts',
    label: t('plugins.uninstall.purgeAccounts'),
    description: t('plugins.uninstall.purgeAccountsHint'),
    icon: 'account',
    tone: 'danger'
  }
])

watch(
  () => props.open,
  (v) => {
    if (v) {
      purgeChoice.value = []
      typed.value = ''
      result.value = null
    }
  }
)

async function submit() {
  if (!matches.value || result.value) return
  busy.value = true
  try {
    const r = await api.del<UninstallResult>(`/plugins/${encodeURIComponent(props.pluginKey)}`, {
      purge: purge.value,
      purge_accounts: purgeAccounts.value || undefined
    })
    toast(t('plugins.uninstall.done', { name: props.name }), 'success')
    if (purgeAccounts.value) {
      // Keep the dialog open to show how many accounts were deleted.
      result.value = { accountsDeleted: r.accounts_deleted }
      return
    }
    emit('update:open', false)
    emit('done')
  } catch (e) {
    notifyError(e)
  } finally {
    busy.value = false
  }
}

function close() {
  emit('update:open', false)
  if (result.value) emit('done')
}
</script>

<template>
  <SModal :open="open" :title="t('plugins.uninstall.title', { name })" width="md" @update:open="(v) => (v ? emit('update:open', v) : close())">
    <div v-if="result" class="space-y-3 text-sm" data-testid="uninstall-result">
      <p class="flex items-center gap-2 font-medium text-emerald-700 dark:text-emerald-400">
        <SIcon name="check" class="h-5 w-5" />{{ t('plugins.uninstall.done', { name }) }}
      </p>
      <p data-testid="accounts-deleted">{{ t('plugins.uninstall.accountsDeleted', { n: result.accountsDeleted }) }}</p>
    </div>
    <div v-else class="space-y-4 text-sm">
      <p>{{ t('plugins.uninstall.body') }}</p>
      <SChoiceCards
        v-model="purgeChoice"
        :options="purgeOptions"
        :cols="1"
        multiple
        :aria-label="t('plugins.uninstall.title', { name })"
        data-testid="uninstall-purge"
      />
      <SHint v-if="purge" size="xs" tone="danger" class="flex items-center gap-1.5">
        <SIcon name="warning" class="h-4 w-4" />{{ t('plugins.uninstall.purgeWarn') }}
      </SHint>
      <SHint v-if="purgeAccounts" size="xs" tone="danger" class="flex items-center gap-1.5">
        <SIcon name="warning" class="h-4 w-4" />{{ t('plugins.uninstall.purgeAccountsWarn') }}
      </SHint>
      <SField :label="t('plugins.uninstall.typeKey', { key: shownId })">
        <SInput v-model="typed" mono :placeholder="shownId" autocomplete="off" @keyup.enter="submit" />
      </SField>
    </div>
    <template #footer>
      <SButton v-if="result" variant="primary" @click="close">{{ t('common.close') }}</SButton>
      <template v-else>
        <SButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</SButton>
        <SButton variant="danger" icon="trash" :loading="busy" :disabled="!matches" @click="submit">{{ t('plugins.uninstall.confirm') }}</SButton>
      </template>
    </template>
  </SModal>
</template>
