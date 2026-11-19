<script setup lang="ts">
// "What the plugin provides" of the consent page: platforms, gateway endpoints,
// account types, hooks, events, jobs, routes, menus, how many user
// permissions (listed in the matrix and the upgrade's changes), the
// database, resources and external services. Each entry renders only when the
// review declares it; the whole card only when at least one is present.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SDisclosure, SHint } from '@sub2api/ui'
import type { PluginReview } from '@/api/types'
import { lt } from '@/i18n'
import { formatBytes } from '@/utils/format'
import { useOptionalT } from '@/composables/optionalText'
import { accountTypesOf, asArray, display, endpointText, formatDuration, hpKey, pick, platformsOf } from '../pluginUtil'
import PluginGatewayDecl from '../parts/PluginGatewayDecl.vue'
import { useConsentLabels } from './hostPermission'

const props = defineProps<{ review: PluginReview }>()
const { t } = useI18n()
const tr = useOptionalT()
const { colon } = useConsentLabels()
const listSep = computed(() => t('plugins.listSep'))

const reviewPlatforms = computed(() => platformsOf(props.review))
const reviewAccountTypes = computed(() => accountTypesOf(props.review))

function fieldLabel(f: string): string {
  return tr(`plugins.fields.${hpKey(f)}`, undefined, f)
}

function hookMatch(h: Record<string, any>): string {
  const m = pick<Record<string, any>>(h, 'match') || {}
  const parts: string[] = []
  const protos = asArray<string>(m.protocols)
  const models = asArray<string>(m.models)
  const groups = asArray<string>(m.groups)
  if (protos.length) parts.push(protos.join(', '))
  parts.push(t('plugins.consent.models') + ': ' + (models.length === 0 || models.includes('*') ? t('common.all') : models.join(', ')))
  parts.push(t('plugins.consent.groups') + ': ' + (groups.length === 0 || groups.includes('*') ? t('common.all') : groups.join(', ')))
  return parts.join(' · ')
}

function hookNeeds(h: Record<string, any>): string[] {
  return asArray<string>(pick(h, 'needs', 'fields'))
}

function hookFailure(h: Record<string, any>): string {
  const f = pick<string>(h, 'failure')
  if (!f) return ''
  return f === 'closed' ? t('plugins.consent.failClosed') : t('plugins.consent.failOpen')
}

function eventNames(list: unknown[]): string[] {
  const out: string[] = []
  for (const e of list) {
    if (typeof e === 'string') out.push(e)
    else if (e && typeof e === 'object') {
      const sub = (e as any).subscribe
      if (Array.isArray(sub)) out.push(...sub.map(String))
      else out.push(String(pick(e, 'type', 'name', 'id') ?? display(e)))
    }
  }
  return out
}

function routeText(r: Record<string, any>): string {
  return [pick(r, 'method'), pick(r, 'path')].filter(Boolean).join(' ') || display(r)
}

function menuText(m: Record<string, any>): string {
  const label = lt(pick(m, 'label', 'title')) || String(pick(m, 'id') ?? '')
  const type = pick<string>(m, 'type', 'page_type')
  return type ? `${label} (${type})` : label
}

const resourceItems = computed(() => {
  const r = props.review.resources
  if (!r) return []
  const out: Array<{ label: string; value: string }> = []
  const mem = pick<number>(r, 'memoryMB', 'memory_mb')
  const cpu = pick<number>(r, 'cpu')
  const threads = pick<number>(r, 'maxProcs', 'max_threads', 'maxThreads')
  const files = pick<number>(r, 'maxOpenFiles', 'max_open_files')
  if (mem !== undefined) out.push({ label: t('plugins.resources.memorySuggested'), value: formatBytes(Number(mem) * 1024 * 1024) })
  if (cpu !== undefined) out.push({ label: t('plugins.resources.cpu'), value: t('plugins.resources.cores', { n: cpu }) })
  if (threads !== undefined) out.push({ label: t('plugins.resources.threads'), value: String(threads) })
  if (files !== undefined) out.push({ label: t('plugins.resources.files'), value: String(files) })
  return out
})

/** At least one item is declared: the card is rendered only then. */
const hasProvides = computed(() => {
  const r = props.review
  return (
    asArray(r.gateway_endpoints).length > 0 ||
    reviewPlatforms.value.length > 0 ||
    reviewAccountTypes.value.length > 0 ||
    asArray(r.hooks).length > 0 ||
    asArray(r.events).length > 0 ||
    asArray(r.jobs).length > 0 ||
    asArray(r.routes).length > 0 ||
    asArray(r.menus).length > 0 ||
    asArray(r.user_permissions).length > 0 ||
    !!r.database ||
    resourceItems.value.length > 0 ||
    asArray(r.external_services).length > 0
  )
})
</script>

<template>
  <SCard v-if="hasProvides" :title="t('plugins.consent.provides')" class="mt-4">
    <ul class="space-y-4 text-sm">
      <li v-if="reviewPlatforms.length">
        <div class="font-medium">{{ t('plugins.consent.platforms') }}</div>
        <div class="mt-1 pl-4">
          <PluginGatewayDecl :platforms="reviewPlatforms" :account-types="[]" section="platforms" />
        </div>
      </li>
      <li v-else-if="review.gateway_endpoints?.length">
        <div class="font-medium">{{ t('plugins.consent.gatewayEndpoints') }}</div>
        <ul class="mt-1 space-y-0.5 pl-4">
          <li v-for="(e, i) in review.gateway_endpoints" :key="i" class="font-mono text-xs">{{ endpointText(e) }}</li>
        </ul>
      </li>

      <li v-if="reviewAccountTypes.length">
        <div class="font-medium">{{ t('plugins.consent.accountTypes') }}</div>
        <div class="mt-1 pl-4" data-testid="review-account-types">
          <PluginGatewayDecl :platforms="reviewPlatforms" :account-types="reviewAccountTypes" section="account_types" />
        </div>
      </li>

      <li v-if="review.hooks?.length">
        <div class="font-medium">{{ t('plugins.consent.hooks') }}</div>
        <ul class="mt-1 space-y-2 pl-4">
          <li v-for="(h, i) in review.hooks" :key="i" class="text-xs">
            <div>
              <span class="font-mono font-medium">{{ h.point }}</span>
              <SHint v-if="h.id" inline size="xs"> #{{ h.id }}</SHint>
              <SHint inline size="xs"> — {{ hookMatch(h) }}</SHint>
            </div>
            <div class="mt-0.5 flex flex-wrap gap-x-4 gap-y-0.5">
              <span v-if="hookNeeds(h).length">
                <SHint inline size="xs">{{ t('plugins.consent.reads') }}{{ colon }}</SHint>{{ hookNeeds(h).map(fieldLabel).join(listSep) }}
              </span>
              <span v-if="pick(h, 'maxPromptBytes', 'max_prompt_bytes')">
                <SHint inline size="xs">{{ t('plugins.consent.maxPrompt') }}{{ colon }}</SHint>{{ formatBytes(Number(pick(h, 'maxPromptBytes', 'max_prompt_bytes'))) }}
              </span>
              <span v-if="pick(h, 'timeoutMs', 'timeout_ms')">
                <SHint inline size="xs">{{ t('plugins.consent.timeout') }}{{ colon }}</SHint>{{ formatDuration(Number(pick(h, 'timeoutMs', 'timeout_ms'))) }}
              </span>
              <span v-if="hookFailure(h)">
                <SHint inline size="xs">{{ t('plugins.consent.onFailure') }}{{ colon }}</SHint>
                <span :class="h.failure === 'closed' ? 'text-red-600 dark:text-red-400' : ''">{{ hookFailure(h) }}</span>
              </span>
            </div>
          </li>
        </ul>
      </li>

      <li v-if="review.events?.length">
        <span class="font-medium">{{ t('plugins.consent.events') }}{{ colon }}</span>
        <span class="font-mono text-xs">{{ eventNames(review.events).join(', ') }}</span>
      </li>

      <li v-if="review.jobs?.length">
        <span class="font-medium">{{ t('plugins.consent.jobs') }}{{ colon }}</span>
        <span v-for="(j, i) in review.jobs" :key="i" class="mr-3 text-xs">
          <span class="font-mono">{{ j.id }}</span>
          <SHint inline size="xs"> ({{ j.schedule }})</SHint>
        </span>
      </li>

      <li v-if="review.routes?.length">
        <div class="font-medium">{{ t('plugins.consent.routes') }}</div>
        <ul class="mt-1 space-y-0.5 pl-4">
          <li v-for="(r, i) in review.routes" :key="i" class="text-xs">
            <span class="font-mono">{{ routeText(r) }}</span>
            <SBadge v-if="r.scope" class="ml-2" :tone="r.scope === 'public' || r.scope === 'webhook' ? 'warning' : 'gray'">{{ r.scope }}</SBadge>
            <SHint v-if="r.permission" inline size="xs" class="ml-2">{{ r.permission }}</SHint>
          </li>
        </ul>
      </li>

      <li v-if="review.menus?.length">
        <span class="font-medium">{{ t('plugins.consent.menus') }}{{ colon }}</span>
        <span class="text-xs">{{ review.menus.map(menuText).join(listSep) }}</span>
      </li>

      <!-- The permissions themselves are the matrix's and the upgrade changes' below: listed once, there. -->
      <li v-if="review.user_permissions?.length" data-testid="provides-user-permissions">
        <span class="font-medium">{{ t('plugins.permissions.userTitle') }}{{ colon }}</span>
        <span class="text-xs">{{ t('plugins.consent.userPermissionsBelow', { n: review.user_permissions.length }, review.user_permissions.length) }}</span>
      </li>

      <li v-if="review.database">
        <span class="font-medium">{{ t('plugins.consent.database') }}{{ colon }}</span>
        <span class="text-xs">
          schema <code class="font-mono">{{ review.database.schema }}</code>{{ listSep }}{{ t('plugins.consent.migrations', { n: review.database.migrations?.length || 0 }) }}
        </span>
        <SDisclosure v-if="review.database.migrations?.length" class="mt-1 pl-4" :label="t('plugins.consent.showMigrations')">
          <ul class="font-mono">
            <li v-for="m in review.database.migrations" :key="m">{{ m }}</li>
          </ul>
        </SDisclosure>
      </li>

      <li v-if="resourceItems.length">
        <span class="font-medium">{{ t('plugins.consent.resources') }}{{ colon }}</span>
        <span class="text-xs">{{ resourceItems.map((x) => `${x.label} ${x.value}`).join(listSep) }}</span>
      </li>

      <li v-if="review.external_services?.length">
        <span class="font-medium">{{ t('plugins.consent.externalServices') }}{{ colon }}</span>
        <code v-for="s in review.external_services" :key="s" class="mr-2 font-mono text-xs">{{ s }}</code>
      </li>
    </ul>
  </SCard>
</template>
