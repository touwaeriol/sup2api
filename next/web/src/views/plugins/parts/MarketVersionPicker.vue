<script setup lang="ts">
// The version picker of a market plugin: every version newest first, with its compatibility, deprecation and release notes; the
// installed and incompatible versions cannot be chosen (CONTRACTS §136.4).
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SChoiceCards, SHint, SLink, SModal, type ChoiceOption } from '@sub2api/ui'
import type { MarketPlugin, MarketVersion } from '@/api/types'
import { lt } from '@/i18n'
import { formatBytes } from '@/utils/format'
import { incompatible, publisherBlocked, sortedVersions, versionOf } from '../marketUtil'

const props = defineProps<{
  /** The plugin to pick a version of; null closes the picker. */
  plugin: MarketPlugin | null
  /** Version installed here ('' when none). */
  installedVersion: string
  /** Version of the running core. */
  hostVersion: string
}>()
const emit = defineEmits<{ close: []; install: [version: string] }>()
const { t } = useI18n()

const picked = ref('')
const versions = computed(() => (props.plugin ? sortedVersions(props.plugin) : []))
const title = computed(() => (props.plugin ? t('plugins.market.pickTitle', { name: lt(props.plugin.name) || props.plugin.key }) : ''))

/** The latest version, else the newest compatible one. */
watch(
  () => props.plugin,
  m => (picked.value = m ? m.latest_version || versions.value.find(v => !incompatible(v))?.version || '' : '')
)

function describe(v: MarketVersion): string | undefined {
  if (incompatible(v)) return t('plugins.market.incompatibleReason', { range: v.host_compat || '—', version: props.hostVersion || '?' })
  const meta = [v.host_compat ? `${t('plugins.consent.hostCompat')} ${v.host_compat}` : '', v.size ? formatBytes(v.size) : '']
  return meta.filter(Boolean).join(' · ') || undefined
}

const choices = computed<ChoiceOption[]>(() =>
  versions.value.map(v => ({
    value: v.version,
    label: 'v' + v.version,
    description: describe(v),
    disabled: v.version === props.installedVersion || incompatible(v)
  }))
)

/** The picked version cannot be installed (already installed, incompatible or its publisher is not active). */
const blocked = computed(() => {
  const m = props.plugin
  if (!m || !picked.value || publisherBlocked(m)) return true
  return picked.value === props.installedVersion || incompatible(versionOf(m, picked.value))
})
</script>

<template>
  <SModal :open="!!plugin" :title="title" width="md" @update:open="v => !v && emit('close')">
    <template v-if="plugin">
      <SHint class="mb-3">{{ t('plugins.market.pickHint') }}</SHint>
      <div class="max-h-80 overflow-y-auto">
        <SChoiceCards
          :model-value="picked"
          :options="choices"
          :cols="1"
          :aria-label="title"
          @update:model-value="v => (picked = String(v))"
        >
          <template v-for="v in versions" :key="v.version" #[`option-${v.version}`]>
            <span class="mt-1.5 flex flex-wrap items-center gap-1.5" :data-version="v.version">
              <SBadge v-if="v.version === plugin.latest_version" tone="primary">{{ t('plugins.market.latest') }}</SBadge>
              <SBadge v-if="v.version === installedVersion" tone="success">{{ t('plugins.market.installed') }}</SBadge>
              <SBadge v-if="v.deprecated" tone="warning" :title="lt(v.deprecated.reason)" data-testid="version-deprecated">{{
                t('plugins.market.deprecated')
              }}</SBadge>
              <SBadge v-if="v.compatible === true" tone="success" data-testid="version-compatible">{{
                t('plugins.market.compatible')
              }}</SBadge>
              <SBadge v-else-if="incompatible(v)" tone="danger" data-testid="version-incompatible">{{
                t('plugins.market.incompatible')
              }}</SBadge>
              <SLink v-if="v.release_url" :href="v.release_url" external data-testid="version-release-notes" @click.stop>{{
                t('plugins.market.releaseNotes')
              }}</SLink>
            </span>
            <SHint v-if="v.deprecated" size="xs" tone="warning" class="mt-1">{{
              t('plugins.market.deprecatedReason', { reason: lt(v.deprecated.reason) })
            }}</SHint>
          </template>
        </SChoiceCards>
      </div>
      <SHint v-if="hostVersion" size="xs" class="mt-3">{{ t('plugins.market.hostVersion', { version: hostVersion }) }}</SHint>
    </template>
    <template #footer>
      <SButton @click="emit('close')">{{ t('common.cancel') }}</SButton>
      <SButton variant="primary" :disabled="blocked" icon="shield" data-testid="market-continue" @click="emit('install', picked)">
        {{ t('plugins.market.continue') }}
      </SButton>
    </template>
  </SModal>
</template>
