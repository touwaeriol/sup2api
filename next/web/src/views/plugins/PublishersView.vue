<script setup lang="ts">
// Publishers: the official store's publishers, read-only (from its index),
// and the local publishers that sign manually uploaded packages (CONTRACTS
// §136, §137). The store tab needs the market permission; the local tab's
// "new publisher" action sits at the end of the tab bar (CONTRACTS §133).
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SPageHeader, STabs, type TabItem } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import StorePublishers from './publishers/StorePublishers.vue'
import LocalPublishers from './publishers/LocalPublishers.vue'

const { t } = useI18n()
const auth = useAuthStore()
const counts = ref<Record<string, number>>({})
const tab = ref(auth.has('plugin:market:read') ? 'store' : 'local')

const tabs = computed<TabItem[]>(() => [
  ...(auth.has('plugin:market:read') ? [{ key: 'store', label: t('publishers.tabs.store'), badge: counts.value.store }] : []),
  { key: 'local', label: t('publishers.tabs.local'), badge: counts.value.local }
])
const setCount = (key: string, n: number) => (counts.value = { ...counts.value, [key]: n })
const local = ref<InstanceType<typeof LocalPublishers>>()
</script>

<template>
  <div>
    <SPageHeader :title="t('publishers.title')" :description="t('publishers.description')" />
    <STabs v-model="tab" :tabs="tabs" :aria-label="t('publishers.title')" data-testid="publisher-tabs">
      <template v-if="tab === 'local' && auth.has('publisher:manage')" #end>
        <SButton variant="primary" icon="plus" data-testid="publisher-create" @click="local?.openCreate()">{{ t('publishers.create') }}</SButton>
      </template>
      <template #default="{ active }">
        <!-- Both stay mounted: each tab's count shows before it is opened. -->
        <StorePublishers v-if="auth.has('plugin:market:read')" v-show="active === 'store'" @count="n => setCount('store', n)" />
        <LocalPublishers v-show="active === 'local'" ref="local" @count="n => setCount('local', n)" />
      </template>
    </STabs>
  </div>
</template>
