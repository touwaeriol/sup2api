<script setup lang="ts">
// A store plugin's versions, newest first: status (latest, installed,
// deprecated), the core they need, size, sha256 (when the core sends it),
// release and the version's action; a built-in plugin never offers a version
// below the installed one (CONTRACTS §136.4).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SCard, SHint, SLink, STable, type TableColumn } from '@sub2api/ui'
import type { MarketPlugin, MarketVersion } from '@/api/marketTypes'
import { lt } from '@/i18n'
import { formatBytes } from '@/utils/format'
import { compareVersions } from '../pluginUtil'
import { incompatible, offersVersion, publisherBlocked, releaseTag, shortSha, sortedVersions } from '../marketUtil'

const props = defineProps<{ plugin: MarketPlugin; installedVersion: string; canInstall: boolean }>()
const emit = defineEmits<{ install: [version: string] }>()
const { t } = useI18n()

const versions = computed(() => sortedVersions(props.plugin))
const deprecated = computed(() => versions.value.filter(v => v.deprecated))
const withSha = computed(() => versions.value.some(v => v.sha256))
const columns = computed<TableColumn[]>(() => [
  { key: 'version', label: t('market.detail.version'), mobile: 'title' },
  { key: 'status', label: t('common.status'), mobile: 'status' },
  { key: 'host_compat', label: t('market.detail.hostCompat'), mobile: 'meta' },
  { key: 'size', label: t('market.detail.size'), mobile: 'meta' },
  ...(withSha.value ? [{ key: 'sha256', label: 'sha256', mobile: 'meta' as const }] : []),
  { key: 'release', label: t('market.detail.release'), mobile: 'meta' },
  { key: 'actions', label: '', align: 'right' }
])

/** What a version row says in place of an action ('' when it offers one). */
function note(v: MarketVersion): string {
  if (v.version === props.installedVersion) return t('market.detail.current')
  if (incompatible(v)) return t('market.detail.incompatible')
  return offersVersion(props.plugin, v, props.installedVersion) ? '' : t('market.detail.belowInstalled')
}
const upgrades = (v: MarketVersion) => !!props.installedVersion && compareVersions(v.version, props.installedVersion) > 0
const actionable = (v: MarketVersion) => props.canInstall && !publisherBlocked(props.plugin) && !note(v)
const rowClass = (v: MarketVersion) => (v.version === props.installedVersion ? 'bg-primary-50/60 dark:bg-primary-900/20' : '')
const builtinFloor = computed(() => props.plugin.builtin && versions.value.some(v => !!props.installedVersion && compareVersions(v.version, props.installedVersion) < 0))
</script>

<template>
  <SCard :title="t('market.detail.versionsTitle')" :subtitle="t('market.detail.versionsHint')" :padded="false" data-testid="market-versions-table">
    <STable :columns="columns" :rows="versions" row-key="version" :row-class="rowClass">
      <template #cell-version="{ row }">
        <span class="font-mono" :class="row.deprecated ? 'text-fg-muted' : ''">v{{ row.version }}</span>
      </template>
      <template #cell-status="{ row }">
        <div class="flex flex-wrap gap-1" :data-version="row.version">
          <SBadge v-if="row.version === plugin.latest_version" tone="success">{{ t('market.detail.latest') }}</SBadge>
          <SBadge v-if="row.version === installedVersion" tone="primary">{{ t('market.detail.installed') }}</SBadge>
          <SBadge v-if="row.deprecated" tone="warning" :title="lt(row.deprecated.reason)" data-testid="version-deprecated">{{ t('market.deprecated') }}</SBadge>
          <SHint v-if="row.version !== plugin.latest_version && row.version !== installedVersion && !row.deprecated" inline>—</SHint>
        </div>
      </template>
      <template #cell-host_compat="{ row }"><span class="font-mono text-sm">{{ row.host_compat || '—' }}</span></template>
      <template #cell-size="{ row }">{{ row.size ? formatBytes(row.size) : '—' }}</template>
      <template #cell-sha256="{ row }">
        <SHint inline size="xs" class="font-mono" :title="row.sha256">{{ row.sha256 ? shortSha(row.sha256) : '—' }}</SHint>
      </template>
      <template #cell-release="{ row }">
        <SLink v-if="row.release_url" :href="row.release_url" external>{{ releaseTag(row.release_url) }}</SLink>
        <SHint v-else inline>—</SHint>
      </template>
      <template #cell-actions="{ row }">
        <SButton v-if="actionable(row)" size="sm" :data-testid="`version-install-${row.version}`" @click="emit('install', row.version)">
          {{ upgrades(row) ? t('market.detail.upgradeToThis') : t('market.detail.installThis') }}
        </SButton>
        <SHint v-else-if="note(row)" inline size="xs" :data-testid="`version-note-${row.version}`">{{ note(row) }}</SHint>
      </template>
    </STable>
    <SHint v-for="v in deprecated" :key="v.version" size="xs" tone="warning" class="border-t border-line px-4 py-2.5">
      {{ t('market.versionDeprecated', { version: v.version, reason: lt(v.deprecated?.reason) }) }}
    </SHint>
    <SHint v-if="builtinFloor" size="xs" class="border-t border-line px-4 py-2.5" data-testid="market-builtin-floor">{{ t('market.detail.builtinNoDowngrade') }}</SHint>
  </SCard>
</template>
