<script setup lang="ts">
import { computed, onErrorCaptured, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { SButton, SCard, SEmpty, SHint, SPageHeader, SSpinner } from '@sub2api/ui'
import { usePluginStore, assetURL } from '@/stores/plugins'
import { lt } from '@/i18n'
import PluginIframe from '@/components/plugin/PluginIframe.vue'

const route = useRoute()
const plugins = usePluginStore()
const { t } = useI18n()

const pluginKey = computed(() => String(route.params.plugin))
const pageId = computed(() => String(route.params.page))
const found = computed(() => plugins.page(pluginKey.value, pageId.value))
const crashed = ref('')
// Bumped by "Retry": remounts the page so a crashed plugin component starts over.
const attempt = ref(0)

function retry() {
  crashed.value = ''
  attempt.value++
}

const title = computed(() => {
  const f = found.value
  if (!f) return pluginKey.value
  if (f.page.title) return lt(f.page.title)
  const menu = f.plugin.menus?.find((m) => m.page === pageId.value)
  return menu ? lt(menu.label) : lt(f.plugin.name) || f.plugin.key
})

// Waits for the plugin list, and for a native page also for that plugin's own
// module; other plugins still loading do not hold this page up.
const waiting = computed(() => !plugins.listed || (found.value?.page.type === 'native' && !!plugins.pending[pluginKey.value]))

const nativeComponent = computed(() => (found.value?.page.type === 'native' ? plugins.component(pluginKey.value, found.value.page.component) : undefined))

watch([pluginKey, pageId], () => (crashed.value = ''))

onErrorCaptured((err) => {
  console.error(`[plugins] page ${pluginKey.value}/${pageId.value} failed`, err)
  crashed.value = err instanceof Error ? err.message : String(err)
  return false
})
</script>

<template>
  <div>
    <!-- native pages render their own header -->
    <SPageHeader
      v-if="found?.page.type !== 'native' || !nativeComponent"
      :title="title"
      :description="found ? `${found.plugin.key} v${found.plugin.version}` : undefined"
    />
    <div v-if="waiting" class="flex justify-center py-16"><SSpinner /></div>
    <SEmpty v-else-if="!found" :text="t('pluginHost.notFound', { plugin: pluginKey, page: pageId })" icon="puzzle" />
    <SCard v-else-if="crashed" data-testid="plugin-page-crashed">
      <SEmpty icon="warning" :text="t('pluginHost.crashed')">
        <SHint size="xs" tone="danger" class="max-w-xl break-words">{{ crashed }}</SHint>
        <SButton size="sm" data-testid="plugin-page-retry" icon="refresh" @click="retry">{{ t('pluginHost.retry') }}</SButton>
      </SEmpty>
    </SCard>
    <div v-else :key="attempt">
      <PluginIframe
        v-if="found.page.type === 'iframe' && found.page.src"
        :plugin-key="found.plugin.key"
        :page="pageId"
        :src="assetURL(found.plugin, found.page.src)"
        :min-height="480"
      />
      <template v-else-if="found.page.type === 'native'">
        <component :is="nativeComponent" v-if="nativeComponent" />
        <SCard v-else>
          <SHint>
            {{ plugins.errors[pluginKey] ? t('pluginHost.nativeFailed', { reason: plugins.errors[pluginKey] }) : t('pluginHost.nativeMissing', { component: found.page.component || '?' }) }}
          </SHint>
        </SCard>
      </template>
      <SEmpty v-else :text="t('pluginHost.unsupported', { type: found.page.type })" />
    </div>
  </div>
</template>
