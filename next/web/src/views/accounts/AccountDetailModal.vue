<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SModal, STabs } from '@sub2api/ui'
import type { Account } from '@/api/types'
import { usePluginStore } from '@/stores/plugins'
import { forAccountType, slotKey } from '@/components/plugin/slots'
import AccountDetailOverview from './AccountDetailOverview.vue'

// An account's details: the overview, then the tabs plugins add for its
// account type (account.detail.tabs; one declaring accountTypes, UI 3.8, only
// for those types of its plugin). Each account opens on the overview. The
// overview's own props and events (actions the list also offers) pass
// through.
defineOptions({ inheritAttrs: false })
const props = defineProps<{ open: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()
const { t } = useI18n()
const plugins = usePluginStore()

const tab = ref('overview')
watch(
  () => [props.open, props.account?.id] as const,
  ([open]) => {
    if (open) tab.value = 'overview'
  }
)

const slots = computed(() => plugins.slotEntries('account.detail.tabs').filter((e) => forAccountType(e, props.account)))
const tabs = computed(() => [{ key: 'overview', label: t('accounts.overview') }, ...slots.value.map((e) => ({ key: slotKey(e), label: tabLabel(e.pluginKey, e.name) }))])
const slot = computed(() => slots.value.find((e) => slotKey(e) === tab.value))

/** A plugin tab's label: its translation, or the plugin key and the tab name. */
function tabLabel(pluginKey: string, name: string) {
  const msgKey = `plugin.${pluginKey}.tabs.${name}`
  const translated = t(msgKey)
  return translated !== msgKey ? translated : `${pluginKey} · ${name}`
}
</script>

<template>
  <SModal :open="open" :title="account?.name || ''" width="xl" @update:open="emit('update:open', $event)">
    <STabs v-if="account" v-model="tab" :tabs="tabs">
      <AccountDetailOverview v-if="tab === 'overview'" v-bind="$attrs" :account="account" />
      <component :is="slot.component" v-else-if="slot" :account="account" />
    </STabs>
  </SModal>
</template>
