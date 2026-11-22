<script setup lang="ts">
// The proxy row of the account editor's basics (useProxyChoice): pick an
// existing proxy, or paste a URL and test it (a working one is saved and
// selected). v-model is the form's proxy_id.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SField, SHint, SInput, SSegmented } from '@sub2api/ui'
import ProxyPicker from '@/components/ProxyPicker.vue'
import type { ProxyChoice } from './useProxyChoice'

const props = defineProps<{ proxy: ProxyChoice; required?: boolean }>()
const proxyId = defineModel<number | null>({ required: true })
const { t } = useI18n()
const { mode, url, error, urlHint, probing, probed, showProbed } = props.proxy
const modeOptions = computed(() => [
  { value: 'existing', label: t('accounts.proxyPickExisting') },
  ...(props.proxy.canPasteUrl.value ? [{ value: 'url', label: t('accounts.proxyPasteUrl') }] : [])
])
</script>

<template>
  <div data-testid="proxy-field">
    <div class="mb-1.5 flex flex-wrap items-center justify-between gap-2">
      <span class="text-sm font-medium text-fg">{{ t('accounts.proxy') }}<span v-if="required" class="ml-0.5 text-red-500" aria-hidden="true">*</span></span>
      <SSegmented v-if="modeOptions.length > 1" :model-value="mode" :options="modeOptions" size="sm" :aria-label="t('accounts.proxy')" data-testid="proxy-mode" @update:model-value="proxy.setMode(String($event))" />
    </div>
    <SField :error="error">
      <ProxyPicker v-if="mode === 'existing'" v-model="proxyId" />
      <div v-else class="flex gap-2">
        <SInput v-model="url" class="flex-1" mono autocomplete="off" spellcheck="false" data-testid="proxy-url" placeholder="socks5://user:pass@host:port" @keydown.enter.prevent="proxy.probe" />
        <SButton :disabled="!url.trim()" :loading="probing" data-testid="proxy-url-probe" @click="proxy.probe">
          {{ probing ? t('proxies.probing') : t('proxies.probeProxy') }}
        </SButton>
      </div>
      <template v-if="mode === 'url'" #hint>
        <SHint v-if="urlHint" inline size="xs" tone="warning" data-testid="proxy-url-hint">{{ urlHint }}</SHint>
        <template v-else>{{ t('accounts.proxyUrlHint') }}</template>
      </template>
    </SField>
    <SHint v-if="showProbed" tone="success" size="xs" class="mt-1" role="status" data-testid="proxy-probe-result">
      {{ t(probed!.created ? 'accounts.proxyProbeCreated' : 'accounts.proxyProbeReused', { protocol: probed!.protocol, ip: probed!.ip, id: probed!.id }) }}
    </SHint>
  </div>
</template>
