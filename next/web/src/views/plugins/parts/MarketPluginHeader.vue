<script setup lang="ts">
// The head of a store plugin's page: avatar, name, plugin id, badges,
// description, publisher links and installed version, with the page's
// actions (the installed plugin, the upgrade or install of the latest version).
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SHint } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { lt } from '@/i18n'
import type { MarketState } from '../marketUtil'
import { pluginPath } from '../consent/paths'
import PluginAvatar from './PluginAvatar.vue'
import PluginIdText from './PluginIdText.vue'
import MarketBadges from './MarketBadges.vue'
import MarketLinks from './MarketLinks.vue'

defineProps<{ plugin: MarketPlugin; installedVersion: string; state: MarketState; blocked: string; canInstall: boolean; canOpenInstalled: boolean }>()
const emit = defineEmits<{ install: [version: string] }>()
const { t } = useI18n()
</script>

<template>
  <SCard class="mb-4">
    <div class="flex flex-wrap items-start gap-5">
      <PluginAvatar :name="lt(plugin.name)" :plugin-key="plugin.id" size="lg" />
      <div class="min-w-0 flex-[1_1_24rem] space-y-2">
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-lg font-semibold">{{ lt(plugin.name) || plugin.key }}</span>
          <PluginIdText :id="plugin.id" class="text-sm text-fg-muted" />
          <MarketBadges :plugin="plugin" :upgrade="state === 'upgrade' && !blocked" />
        </div>
        <p v-if="plugin.description" class="text-sm text-fg">{{ lt(plugin.description) }}</p>
        <SHint size="xs" class="flex flex-wrap items-center gap-x-1">
          <MarketLinks :plugin="plugin" />
          <span v-if="installedVersion"> · {{ t('market.installedVersion', { version: installedVersion }) }}</span>
        </SHint>
        <SHint v-if="plugin.deprecated" size="xs" tone="warning">{{ t('market.deprecatedReason', { reason: lt(plugin.deprecated.reason) }) }}</SHint>
        <SHint v-if="blocked" size="xs" tone="danger" data-testid="market-blocked">{{ blocked }}</SHint>
      </div>
      <div class="flex flex-wrap gap-2">
        <SButton v-if="installedVersion && canOpenInstalled" :to="pluginPath(plugin.id)">{{ t('market.detail.viewInstalled') }}</SButton>
        <template v-if="canInstall && !blocked && plugin.latest_version">
          <SButton v-if="state === 'upgrade'" variant="warning" icon="upload" data-testid="market-upgrade" @click="emit('install', plugin.latest_version)">
            {{ t('market.upgradeTo', { version: plugin.latest_version }) }}
          </SButton>
          <SButton v-else-if="state === 'install'" :variant="plugin.deprecated ? 'secondary' : 'primary'" icon="download" data-testid="market-install" @click="emit('install', plugin.latest_version)">
            {{ plugin.deprecated ? t('market.installAnyway') : t('market.install') }}
          </SButton>
        </template>
      </div>
    </div>
  </SCard>
</template>
