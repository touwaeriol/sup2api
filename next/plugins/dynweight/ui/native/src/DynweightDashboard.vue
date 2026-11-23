<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SButton, SPageHeader, STabs } from '@sub2api/ui'
import StatusTab from './StatusTab.vue'
import EventsTab from './EventsTab.vue'
import ConfigTab from './ConfigTab.vue'
import { omit, useDwHost, type Filter } from './host'

// Dynamic weight page (CONTRACTS §74.5): effective weights, weight events and
// the configuration. Tabs are mounted on first visit and synced to ?tab; the
// account list's links come with ?account= (and group=, model=), which
// preset the key filter of the status and events tabs (dashboardRoute), and
// the account pages' with ?override_account=, which opens that account's
// override on the config tab.
const host = useDwHost()
const t = host.t

type TabKey = 'status' | 'events' | 'config'

const tabs = computed(() => (['status', 'events', 'config'] as const).map(key => ({ key, label: t(`tabs.${key}`) })))

const query = host.router.currentRoute.value.query
const param = (k: string) => (typeof query[k] === 'string' ? (query[k] as string) : '')

function initialTab(): TabKey {
  const q = query.tab
  return tabs.value.some(x => x.key === q) ? (q as TabKey) : 'status'
}

function initialFilter(): Filter | null {
  const f: Filter = { account_id: param('account'), group_id: param('group'), model: param('model') }
  return f.account_id || f.group_id || f.model ? f : null
}
const preset = initialFilter()
// The preset is consumed: a cleared filter must not come back on reload.
if (preset) {
  void host.router.replace({ query: Object.fromEntries(Object.entries(query).filter(([k]) => !['account', 'group', 'model'].includes(k))) })
}

const tab = ref<TabKey>(initialTab())

const visited = ref(new Set<TabKey>([tab.value]))

watch(tab, v => {
  if (!visited.value.has(v)) visited.value = new Set([...visited.value, v])
  const route = host.router.currentRoute.value
  if (route.query.tab !== v) host.router.replace({ query: { ...route.query, tab: v } })
})

// ?override_account= is consumed like the preset, also when it comes while
// the page is open: the config tab opens the dialog, then clears the ref.
const overrideAccount = ref<string | null>(null)
watch(
  () => host.router.currentRoute.value.query.override_account,
  v => {
    if (typeof v !== 'string' || !/^\d+$/.test(v)) return
    overrideAccount.value = v
    tab.value = 'config'
    void host.router.replace({ query: { ...omit(host.router.currentRoute.value.query, 'override_account'), tab: 'config' } })
  },
  { immediate: true }
)

// Tabs with reload(), refreshed by the header button.
const refs: Partial<Record<TabKey, { reload?: () => unknown } | null>> = {}
const setRef = (k: TabKey) => (el: unknown) => (refs[k] = el as { reload?: () => unknown } | null)
const refreshing = ref(false)
async function refresh() {
  refreshing.value = true
  try {
    await refs[tab.value]?.reload?.()
  } finally {
    refreshing.value = false
  }
}

// "Events of this key" from the status tab.
const eventsPreset = ref<Filter | null>(preset)
function showEvents(f: Filter) {
  eventsPreset.value = { ...f }
  tab.value = 'events'
}
</script>

<template>
  <div>
    <SPageHeader :title="t('title')" :description="t('subtitle')" />

    <STabs v-model="tab" :tabs="tabs">
      <template #end>
        <SButton icon="refresh" :loading="refreshing" @click="refresh">{{ t('refresh') }}</SButton>
      </template>
      <StatusTab
        v-if="visited.has('status')"
        v-show="tab === 'status'"
        :ref="setRef('status')"
        :preset="preset"
        @show-events="showEvents"
      />
      <EventsTab v-if="visited.has('events')" v-show="tab === 'events'" :ref="setRef('events')" :preset="eventsPreset" />
      <ConfigTab v-if="visited.has('config')" v-show="tab === 'config'" :ref="setRef('config')" v-model:open-account="overrideAccount" />
    </STabs>
  </div>
</template>
