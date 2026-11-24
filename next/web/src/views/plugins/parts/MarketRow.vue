<script setup lang="ts">
// One plugin of the market list: name (opens its store page), plugin id,
// badges, publisher links, installed and latest versions, notes (deprecated,
// blocked, newer but incompatible) and the action of its state (CONTRACTS §136.4).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SHint, SIcon, SLink } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { lt } from '@/i18n'
import { deprecatedVersion, newerIncompatible, versionOf, type MarketState } from '../marketUtil'
import { marketPluginPath } from '../consent/paths'
import PluginAvatar from './PluginAvatar.vue'
import PluginIdText from './PluginIdText.vue'
import MarketBadges from './MarketBadges.vue'
import MarketLinks from './MarketLinks.vue'

const props = defineProps<{
  plugin: MarketPlugin
  /** The channel being viewed: the plugin's page opens its versions. */
  channel: number
  /** Version installed here ('' when none). */
  installedVersion: string
  state: MarketState
  /** Why the plugin cannot be installed or upgraded ('' when it can). */
  blocked: string
  canInstall: boolean
}>()
const emit = defineEmits<{ install: [version: string] }>()
const { t } = useI18n()

const detail = computed(() => marketPluginPath(props.plugin, props.channel))
const latest = computed(() => versionOf(props.plugin, props.plugin.latest_version))
const oldDeprecated = computed(() => deprecatedVersion(props.plugin))
const newer = computed(() => (props.state !== 'install' ? newerIncompatible(props.plugin) : ''))
</script>

<template>
  <div class="flex flex-wrap items-start gap-4 border-b border-line px-5 py-4 last:border-b-0" :data-plugin="plugin.id">
    <PluginAvatar :name="lt(plugin.name)" :plugin-key="plugin.id" size="lg" />
    <div class="min-w-0 flex-1 space-y-1">
      <div class="flex flex-wrap items-center gap-2">
        <SLink :to="detail" class="font-semibold" data-testid="market-name">{{ lt(plugin.name) || plugin.key }}</SLink>
        <PluginIdText :id="plugin.id" class="text-sm text-fg-muted" />
        <MarketBadges :plugin="plugin" :upgrade="state === 'upgrade' && !blocked" />
      </div>
      <p v-if="plugin.description" class="line-clamp-2 text-sm text-fg">{{ lt(plugin.description) }}</p>
      <SHint size="xs" class="flex flex-wrap items-center gap-x-1">
        <MarketLinks :plugin="plugin" with-publisher />
        <template v-if="installedVersion">
          · <span data-testid="market-installed-version">{{ t('market.installedVersion', { version: installedVersion }) }}</span>
          <span v-if="state === 'upgrade' && plugin.latest_version">, {{ t('market.latestVersion', { version: plugin.latest_version }) }}</span>
          <span v-else-if="installedVersion === plugin.latest_version">{{ t('market.latestMark') }}</span>
        </template>
        <template v-else-if="plugin.latest_version">
          · {{ t('market.latestVersion', { version: plugin.latest_version }) }}
          <span v-if="latest?.host_compat"> · {{ t('market.requiresCore', { range: latest.host_compat }) }}</span>
        </template>
        <template v-if="latest?.release_url">
          · <SLink :href="latest.release_url" external data-testid="market-release">{{ t('market.releaseNotes') }}</SLink>
        </template>
      </SHint>
      <SHint v-if="plugin.deprecated" size="xs" tone="warning" data-testid="market-deprecated-reason">
        {{ t('market.deprecatedReason', { reason: lt(plugin.deprecated.reason) }) }}
      </SHint>
      <SHint v-if="oldDeprecated?.deprecated" size="xs" tone="warning" data-testid="market-version-deprecated">
        {{ t('market.versionDeprecated', { version: oldDeprecated.version, reason: lt(oldDeprecated.deprecated.reason) }) }}
      </SHint>
      <SHint v-if="blocked" size="xs" tone="danger" class="flex items-center gap-1" data-testid="market-blocked">
        <SIcon name="warning" class="h-3.5 w-3.5 shrink-0" />{{ blocked }}
      </SHint>
      <SHint v-else-if="newer" size="xs">{{ t('market.newerIncompatible', { version: newer }) }}</SHint>
    </div>
    <div class="flex shrink-0 items-center gap-2">
      <SButton v-if="!blocked" size="sm" variant="ghost" :to="detail" data-testid="market-versions">{{ t('market.versions') }}</SButton>
      <SButton v-if="blocked" size="sm" disabled :title="blocked" data-testid="market-not-installable">{{ t('market.notInstallable') }}</SButton>
      <SBadge v-else-if="state === 'installed'" tone="success" data-testid="market-up-to-date">{{ t('market.upToDate') }}</SBadge>
      <template v-else-if="canInstall">
        <SButton v-if="state === 'upgrade'" size="sm" variant="warning" icon="upload" data-testid="market-upgrade" @click="emit('install', plugin.latest_version)">
          {{ t('market.upgradeTo', { version: plugin.latest_version }) }}
        </SButton>
        <SButton v-else size="sm" :variant="plugin.deprecated ? 'secondary' : 'primary'" icon="download" data-testid="market-install" @click="emit('install', plugin.latest_version)">
          {{ plugin.deprecated ? t('market.installAnyway') : t('market.install') }}
        </SButton>
      </template>
    </div>
  </div>
</template>
