<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SButton, SCode, SCopyButton, SEmpty, SHint, SModal, SSegmented, SSpinner, STabs } from '@sub2api/ui'
import type { ApiKey, GroupServedProtocol } from '@/api/types'
import { notifyError } from '@/utils/errors'
import PlatformEndpointsPreview from '@/views/platforms/PlatformEndpointsPreview.vue'
import { keyAppsFor, magpieFields } from './keyApps'
import { SHELLS, clientsFor, gatewayBase, usageURL, type Shell } from './keyClients'
import { useOpenApp } from './openApp'
import { revealKey } from './reveal'

// How to use an API key: the configuration of each client of its platforms
// with the full key filled in (revealed on open; a legacy key shows its
// prefix), what Magpie's provider editor takes, the one-link imports of the
// open tab, and the endpoints the key can call (those its group serves).
const props = defineProps<{ apiKey: ApiKey | null; platforms?: string[] | null; served?: GroupServedProtocol[] | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()
const openApp = useOpenApp()

const base = gatewayBase()
const key = ref('')
const revealed = ref(false)
const loading = ref(false)
const clients = computed(() => clientsFor(props.platforms))
const apps = computed(() => keyAppsFor(props.platforms))
// An app that takes every platform of the key at once (Magpie) is not set up
// per client, so it gets a tab of its own after the clients'.
const tabs = computed(() => [
  ...clients.value.map(c => ({ key: c.id, label: c.label })),
  ...apps.value.filter(a => !a.client).map(a => ({ key: a.id, label: a.label }))
])
const tab = ref('')
const shell = ref<Shell>(navigator.userAgent.includes('Windows') ? 'powershell' : 'unix')
// As STabs resolves it: the first tab until one is picked.
const active = computed(() => (tabs.value.find(x => x.key === tab.value) ?? tabs.value[0])?.key)
const client = computed(() => clients.value.find(c => c.id === active.value))
const imports = computed(() => apps.value.filter(a => !a.client || a.client === active.value))
/** What the open tab says to put in place: a client's files and commands, else Magpie's fields. */
const blocks = computed(() => {
  if (client.value)
    return client.value
      .snippets(base, key.value, shell.value)
      .map(s => ({ caption: s.path ? t('apikeys.use.file', { path: s.path }) : t('apikeys.use.run'), content: s.content }))
  return Object.entries(magpieFields(props.platforms, base, key.value)).map(([field, content]) => ({
    caption: t(`apikeys.use.magpie.${field}`),
    content
  }))
})
const usageQuery = computed(() => `curl -H "Authorization: Bearer ${key.value}" ${usageURL(base)}`)
const shellOptions = computed(() => SHELLS.map(s => ({ value: s, label: t(`apikeys.use.shells.${s}`) })))
/** Text carrying the prefix that stands in for a legacy key is not worth copying. */
const copyable = (text: string) => revealed.value || !text.includes(key.value)

// Each key the guide is opened for (closing it counts): a reveal answering
// after the guide moved on is another key's and is dropped.
let opened = 0
watch(
  () => props.apiKey,
  async k => {
    const my = ++opened
    key.value = k ? `${k.key_prefix}…` : ''
    revealed.value = false
    tab.value = ''
    loading.value = !!k?.copyable
    if (!k?.copyable) return
    try {
      const full = await revealKey(k.id)
      if (my !== opened) return
      key.value = full
      revealed.value = true
    } catch (e) {
      if (my === opened) notifyError(e)
    } finally {
      if (my === opened) loading.value = false
    }
  },
  { immediate: true }
)
</script>

<template>
  <SModal :open="!!apiKey" :title="t('apikeys.use.title', { name: apiKey?.name })" width="xl" @update:open="!$event && emit('close')">
    <div v-if="loading" class="flex justify-center py-10"><SSpinner /></div>
    <div v-else class="space-y-4" data-testid="key-use-guide">
      <SAlert v-if="!revealed" tone="warning">{{ t('apikeys.use.noKey') }}</SAlert>
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <span class="text-fg-muted">{{ t('apikeys.use.base') }}</span>
        <SCode inline :text="base" />
        <SCopyButton :text="base" icon-only size="xs" />
      </div>
      <STabs v-if="tabs.length" v-model="tab" :tabs="tabs" size="sm">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <SSegmented v-if="client?.perShell" v-model="shell" :options="shellOptions" size="sm" :aria-label="t('apikeys.use.shell')" />
          <SHint v-else-if="!client" size="xs">{{ t('apikeys.use.magpie.intro') }}</SHint>
          <div v-if="revealed" class="flex flex-wrap gap-2">
            <SButton
              v-for="a in imports"
              :key="a.id"
              variant="secondary"
              size="sm"
              icon="upload"
              :data-testid="`guide-import-${a.id}`"
              @click="openApp(a, key)"
            >
              {{ t('apikeys.apps.import', { app: a.app }) }}
            </SButton>
          </div>
        </div>
        <div v-for="b in blocks" :key="b.caption" class="space-y-1">
          <div class="flex items-center justify-between gap-2 text-xs text-fg-muted">
            <span>{{ b.caption }}</span>
            <SCopyButton :text="b.content" size="xs" :disabled="!copyable(b.content)" />
          </div>
          <SCode :text="b.content" :wrap="false" class="text-xs" data-testid="key-use-snippet" />
        </div>
      </STabs>
      <SEmpty v-else size="sm" icon="code" :text="t('apikeys.use.noClient')" />
      <div class="space-y-1 border-t border-line pt-3">
        <div class="flex items-center justify-between gap-2 text-xs text-fg-muted">
          <span>{{ t('apikeys.use.usage') }}</span>
          <SCopyButton :text="usageQuery" size="xs" :disabled="!revealed" />
        </div>
        <SCode :text="usageQuery" :wrap="false" class="text-xs" data-testid="key-usage-query" />
      </div>
      <div v-if="served?.length" class="space-y-2 border-t border-line pt-3">
        <SHint size="xs">{{ t('apikeys.use.endpoints', { base }) }}</SHint>
        <PlatformEndpointsPreview :served="served" />
      </div>
    </div>
    <template #footer>
      <SButton @click="emit('close')">{{ t('common.close') }}</SButton>
    </template>
  </SModal>
</template>
