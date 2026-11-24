<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SCode, SDisclosure, SHint } from '@sub2api/ui'
import PluginStatusBadge from '../parts/PluginStatusBadge.vue'
import { lt } from '@/i18n'
import { formatNumber } from '@/utils/format'
import { accountTypesOf, asArray, capabilityIdsOf, endpointText, pick, platformsOf, type PluginDetail } from '../pluginUtil'
import PluginGatewayDecl from '../parts/PluginGatewayDecl.vue'
import TrustBadge from '../parts/TrustBadge.vue'
import PluginIdText from '../parts/PluginIdText.vue'

const props = defineProps<{ detail: PluginDetail }>()
const { t } = useI18n()

const m = computed<Record<string, any>>(() => props.detail.manifest || {})
const ui = computed<Record<string, any>>(() => pick(m.value, 'ui') || {})

const description = computed(() => lt(pick(m.value, 'description')))
const capabilities = computed(() => capabilityIdsOf(pick(m.value, 'capabilities')))
// New platforms declared by the plugin (CONTRACTS §13); endpoints belong to them.
const platforms = computed(() => platformsOf(m.value))
const endpoints = computed(() => asArray<Record<string, any>>(pick(m.value, 'gateway_endpoints', 'gatewayEndpoints')))
const menus = computed(() => asArray<Record<string, any>>(pick(ui.value, 'menus') ?? pick(m.value, 'menus')))
const pages = computed(() => {
  const p = pick<Record<string, any>>(ui.value, 'pages') ?? pick(m.value, 'pages')
  if (!p || typeof p !== 'object') return []
  return Array.isArray(p) ? p.map((x: any) => ({ id: String(x.id ?? ''), type: String(x.type ?? '') })) : Object.entries(p).map(([id, v]) => ({ id, type: String((v as any)?.type ?? '') }))
})
const slots = computed(() => asArray<Record<string, any>>(pick(ui.value, 'slots') ?? pick(m.value, 'slots')))
// Account types are top level (any plugin can declare them); each lists its platforms.
const accountTypes = computed(() => accountTypesOf(m.value))

const nodeStates = computed(() => {
  const counts: Record<string, number> = {}
  for (const n of props.detail.nodes || []) {
    const s = typeof n.state === 'string' ? n.state : String(pick(n.state, 'status', 'state') ?? 'unknown')
    counts[s] = (counts[s] || 0) + 1
  }
  return Object.entries(counts)
})
</script>

<template>
  <div class="grid gap-4 lg:grid-cols-3">
    <SCard :title="t('plugins.detail.overview')" class="lg:col-span-2">
      <p v-if="description" class="mb-4 text-sm text-gray-700 dark:text-gray-300">{{ description }}</p>
      <dl class="kv">
        <dt>{{ t('plugins.key') }}</dt>
        <dd><PluginIdText :id="detail.key" :display-id="detail.display_id" /></dd>
        <dt>{{ t('common.status') }}</dt>
        <dd class="flex flex-wrap items-center gap-2">
          <PluginStatusBadge :status="detail.status" />
          <SHint v-if="detail.status_reason" inline size="xs" tone="danger">{{ detail.status_reason }}</SHint>
        </dd>
        <dt>{{ t('plugins.activeVersion') }}</dt>
        <dd class="font-mono">{{ detail.active_version ? 'v' + detail.active_version : '—' }}</dd>
        <template v-if="detail.desired_version && detail.desired_version !== detail.active_version">
          <dt>{{ t('plugins.desiredVersion') }}</dt>
          <dd class="font-mono">v{{ detail.desired_version }}</dd>
        </template>
        <template v-if="m.version && m.version !== detail.active_version">
          <dt>{{ t('plugins.manifestVersion') }}</dt>
          <dd class="font-mono">v{{ m.version }}</dd>
        </template>
        <dt>{{ t('plugins.publisher') }}</dt>
        <dd class="flex items-center gap-2">{{ detail.publisher || m.publisher || '—' }} <TrustBadge :trust="detail.trust" /></dd>
        <template v-if="pick(m, 'hostCompat', 'host_compat')">
          <dt>{{ t('plugins.consent.hostCompat') }}</dt>
          <dd class="font-mono text-xs">{{ pick(m, 'hostCompat', 'host_compat') }}</dd>
        </template>
        <template v-if="capabilities.length">
          <dt>{{ t('plugins.detail.capabilities') }}</dt>
          <dd class="flex flex-wrap gap-1">
            <SBadge v-for="c in capabilities" :key="c">{{ c }}</SBadge>
          </dd>
        </template>
        <template v-if="platforms.length">
          <dt>{{ t('plugins.consent.platforms') }}</dt>
          <dd><PluginGatewayDecl :platforms="platforms" :account-types="[]" section="platforms" /></dd>
        </template>
        <template v-if="accountTypes.length">
          <dt>{{ t('plugins.consent.accountTypes') }}</dt>
          <dd data-testid="detail-account-types">
            <PluginGatewayDecl :platforms="platforms" :account-types="accountTypes" section="account_types" />
          </dd>
        </template>
        <template v-if="!platforms.length && endpoints.length">
          <dt>{{ t('plugins.consent.gatewayEndpoints') }}</dt>
          <dd>
            <div v-for="(e, i) in endpoints" :key="i" class="font-mono text-xs">{{ endpointText(e) }}</div>
          </dd>
        </template>
        <template v-if="menus.length">
          <dt>{{ t('plugins.consent.menus') }}</dt>
          <dd class="flex flex-wrap gap-1">
            <SBadge v-for="(x, i) in menus" :key="i">{{ lt(x.label) || x.id }}</SBadge>
          </dd>
        </template>
        <template v-if="pages.length">
          <dt>{{ t('plugins.detail.pages') }}</dt>
          <dd class="flex flex-wrap gap-1">
            <SBadge v-for="p in pages" :key="p.id">{{ p.id }}<span v-if="p.type" class="opacity-70"> ({{ p.type }})</span></SBadge>
          </dd>
        </template>
        <template v-if="slots.length">
          <dt>{{ t('plugins.detail.slots') }}</dt>
          <dd class="flex flex-wrap gap-1">
            <SBadge v-for="(s, i) in slots" :key="i" tone="purple">{{ s.slot }} → {{ s.component }}</SBadge>
          </dd>
        </template>
      </dl>
      <SDisclosure v-if="detail.manifest" class="mt-4" :label="t('plugins.detail.rawManifest')">
        <SCode class="max-h-96" :text="JSON.stringify(detail.manifest, null, 2)" />
      </SDisclosure>
    </SCard>

    <SCard :title="t('plugins.detail.runtime')">
      <dl class="kv">
        <dt>{{ t('plugins.detail.tabs.nodes') }}</dt>
        <dd class="flex flex-wrap gap-1">
          <template v-if="nodeStates.length">
            <span v-for="[s, n] in nodeStates" :key="s" class="inline-flex items-center gap-1">
              <PluginStatusBadge :status="s" /><span class="text-xs">× {{ n }}</span>
            </span>
            <SHint inline size="xs">{{ t('plugins.detail.nodeCount', { n: detail.nodes?.length || 0 }) }}</SHint>
          </template>
          <SHint v-else inline>—</SHint>
        </dd>
        <dt>{{ t('plugins.detail.tabs.hooks') }}</dt>
        <dd>{{ detail.hooks?.length || 0 }}</dd>
        <dt>{{ t('plugins.detail.tabs.jobs') }}</dt>
        <dd>{{ detail.jobs?.length || 0 }}</dd>
        <template v-if="detail.events">
          <dt>{{ t('plugins.events.backlog') }}</dt>
          <dd>{{ formatNumber(detail.events.backlog ?? 0) }}</dd>
          <dt>{{ t('plugins.events.deadletters') }}</dt>
          <dd>{{ Array.isArray(detail.events.deadletters) ? detail.events.deadletters.length : formatNumber(detail.events.deadletters ?? 0) }}</dd>
        </template>
        <dt>{{ t('plugins.egress.policy') }}</dt>
        <dd>{{ detail.egress_policy ? t(`plugins.egress.policies.${detail.egress_policy}`) : '—' }}</dd>
        <dt>{{ t('plugins.detail.tabs.grants') }}</dt>
        <dd>{{ detail.grants?.length ?? '—' }}</dd>
      </dl>
    </SCard>
  </div>
</template>
