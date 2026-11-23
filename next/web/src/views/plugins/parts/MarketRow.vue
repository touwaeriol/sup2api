<script setup lang="ts">
// One plugin of the market list: name, latest version, badges, links, installed version, warnings and the install action (CONTRACTS §136.4).
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SHint, SIcon, SLink } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/types'
import { lt } from '@/i18n'
import { newerIncompatible } from '../marketUtil'
import PluginAvatar from './PluginAvatar.vue'
import MarketBadges from './MarketBadges.vue'
import MarketLinks from './MarketLinks.vue'

defineProps<{
  plugin: MarketPlugin
  /** Version installed here ('' when none). */
  installedVersion: string
  /** The plugin has a row here (its detail page exists). */
  hasDetail: boolean
  state: 'install' | 'upgrade' | 'installed'
  /** Why the plugin cannot be installed ('' when it can). */
  blocked: string
  canInstall: boolean
}>()
const emit = defineEmits<{ pick: [] }>()
const { t } = useI18n()
</script>

<template>
  <div
    class="flex flex-wrap items-center gap-4 border-b border-gray-100 px-5 py-4 last:border-b-0 dark:border-dark-700"
    :data-plugin="plugin.key"
  >
    <PluginAvatar :name="lt(plugin.name)" :plugin-key="plugin.key" size="lg" />
    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="font-semibold text-gray-900 dark:text-white">{{ lt(plugin.name) || plugin.key }}</span>
        <SHint inline size="xs" class="font-mono">{{ plugin.key }}</SHint>
        <span v-if="plugin.latest_version" class="font-mono text-sm">v{{ plugin.latest_version }}</span>
        <MarketBadges :plugin="plugin" />
      </div>
      <p v-if="plugin.description" class="mt-1 line-clamp-2 text-sm text-gray-600 dark:text-gray-300">{{ lt(plugin.description) }}</p>
      <SHint size="xs" class="mt-1">
        <MarketLinks :plugin="plugin" />
        <template v-if="installedVersion">
          · {{ t('plugins.market.installedVersion', { version: installedVersion }) }}
          <SLink v-if="hasDetail" :to="`/plugins/${encodeURIComponent(plugin.key)}`" class="ml-1">{{ t('common.detail') }}</SLink>
        </template>
      </SHint>
      <SHint v-if="plugin.deprecated" size="xs" tone="warning" class="mt-1" data-testid="market-deprecated-reason">
        {{ t('plugins.market.deprecatedReason', { reason: lt(plugin.deprecated.reason) }) }}
      </SHint>
      <SHint v-if="blocked" size="xs" tone="danger" class="mt-1 flex items-center gap-1" data-testid="market-blocked">
        <SIcon name="warning" class="h-3.5 w-3.5" />{{ blocked }}
      </SHint>
      <SHint v-else-if="state !== 'install' && newerIncompatible(plugin)" size="xs" class="mt-1">
        {{ t('plugins.market.newerIncompatible', { version: newerIncompatible(plugin) }) }}
      </SHint>
    </div>
    <div class="flex shrink-0 items-center gap-2">
      <template v-if="state === 'installed'">
        <SBadge tone="success"><SIcon name="check" class="h-3.5 w-3.5" />{{ t('plugins.market.installed') }}</SBadge>
        <SButton v-if="canInstall && (plugin.versions?.length || 0) > 1" size="sm" variant="ghost" @click="emit('pick')">
          {{ t('plugins.market.versions') }}
        </SButton>
      </template>
      <SButton
        v-else-if="canInstall"
        :variant="state === 'upgrade' ? 'warning' : 'primary'"
        size="sm"
        :icon="state === 'upgrade' ? 'upload' : 'download'"
        :disabled="!!blocked"
        :title="blocked || undefined"
        @click="emit('pick')"
      >
        {{ state === 'upgrade' ? t('plugins.market.upgrade') : t('plugins.market.install') }}
      </SButton>
    </div>
  </div>
</template>
