<script setup lang="ts">
import { computed, onErrorCaptured, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SHint, SSpinner } from '@sub2api/ui'
import { usePluginStore } from '@/stores/plugins'

// A component a plugin registered in its native module (a settings form, an
// account credential form), shown the way plugin pages show theirs: a spinner
// while the plugin list or the plugin's module loads, then the component, or
// why there is none (the module failed to load, the component is not
// registered, it failed while rendering) with Retry. Retry loads the plugin
// list and the modules not loaded again, or remounts a component that failed.
// The caller renders the component in the default slot ({ component }), so
// it binds its own props and ref; a failure stays inside this box.
const props = defineProps<{ pluginKey: string; name?: string }>()
const { t } = useI18n()
const plugins = usePluginStore()

const component = computed(() => plugins.component(props.pluginKey, props.name))
const waiting = computed(() => !plugins.listed || !!plugins.pending[props.pluginKey])
const crashed = ref('')
// Bumped by Retry after a crash: the component starts over.
const attempt = ref(0)
const retrying = ref(false)

const problem = computed(() => {
  if (crashed.value) return t('pluginHost.componentCrashed', { reason: crashed.value })
  const reason = plugins.errors[props.pluginKey]
  return reason ? t('pluginHost.nativeFailed', { reason }) : t('pluginHost.nativeMissing', { component: props.name || '?' })
})

watch(() => [props.pluginKey, props.name], () => (crashed.value = ''))

onErrorCaptured((err) => {
  console.error(`[plugins] component ${props.pluginKey}/${props.name} failed`, err)
  crashed.value = err instanceof Error ? err.message : String(err)
  return false
})

async function retry() {
  if (crashed.value) {
    crashed.value = ''
    attempt.value++
    return
  }
  retrying.value = true
  try {
    await plugins.refresh()
  } finally {
    retrying.value = false
  }
}
</script>

<template>
  <div v-if="waiting" class="flex justify-center py-6"><SSpinner /></div>
  <slot v-else-if="component && !crashed" :key="attempt" :component="component" />
  <div v-else class="flex flex-wrap items-center gap-3" data-testid="plugin-component-failed">
    <SHint tone="danger">{{ problem }}</SHint>
    <SButton size="sm" icon="refresh" :loading="retrying" data-testid="plugin-component-retry" @click="retry">{{ t('pluginHost.retry') }}</SButton>
  </div>
</template>
