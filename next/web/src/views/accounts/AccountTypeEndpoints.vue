<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SHint } from '@sub2api/ui'
import type { AccountServedProtocol, AccountTypeEndpoint } from '@/api/types'
import ConversionGradeBadge from '@/components/ConversionGradeBadge.vue'
import { usePlatforms } from '@/composables/platforms'
import { isConvertedGrade } from '@/utils/conversion'
import { COUNT_TOKENS_FAMILY } from './protocols/protocolBinding'
import { useProtocolLabels } from './protocols/protocolLabels'

// Endpoints an account type can currently serve, grouped by platform;
// converted ones (and local estimates) are marked with their grade. A
// platform the type does not declare (`platforms` lists the declared ones) is
// reached through conversion, which an account turns on per model
// (DESIGN-2026-11-17-PROTOCOL-BINDING §5.2). With `served` (an account's
// served_protocols) the endpoints the account does not open are greyed, and
// one served for some models only lists them.
const props = defineProps<{ endpoints: AccountTypeEndpoint[]; compact?: boolean; platforms?: string[]; served?: AccountServedProtocol[] }>()
const { t } = useI18n()
const catalog = usePlatforms()
catalog.load()
const labels = useProtocolLabels()

const byPlatform = computed(() => {
  const out: Array<{ id: string; label: string; items: AccountTypeEndpoint[] }> = []
  for (const e of props.endpoints) {
    const id = e.platform || '—'
    let g = out.find((x) => x.id === id)
    if (!g) {
      g = { id, label: catalog.label(id), items: [] }
      out.push(g)
    }
    g.items.push(e)
  }
  return out
})

/** A platform reached through conversion only: its endpoints need a model to turn them on. */
const needsModel = (g: { id: string; items: AccountTypeEndpoint[] }) => !!props.platforms && !props.platforms.includes(g.id) && g.items.some((e) => e.grade !== 'local')

const servedEntry = (protocol: string) => props.served?.find((s) => s.protocol === protocol)
/** What the account serves of an endpoint; a count_tokens endpoint follows its generation protocol (§3.3). */
const entryOf = (e: AccountTypeEndpoint) => servedEntry(e.protocol) ?? (e.protocol in COUNT_TOKENS_FAMILY ? servedEntry(COUNT_TOKENS_FAMILY[e.protocol]) : undefined)
const closed = (e: AccountTypeEndpoint) => !!props.served && !entryOf(e)
/** The first models an endpoint is limited to, and all of them for the tooltip. */
function modelsOf(e: AccountTypeEndpoint) {
  const models = entryOf(e)?.models
  if (!models) return null
  const names = models.map(labels.modelName)
  const sep = t('common.listSeparator')
  return { text: t('accounts.protocols.served.models', { n: names.length, models: names.slice(0, 3).join(sep) + (names.length > 3 ? ' …' : '') }), all: names.join(sep) }
}
</script>

<template>
  <div v-if="endpoints.length" class="space-y-1.5" data-testid="type-endpoints">
    <div v-for="g in byPlatform" :key="g.id" :data-platform="g.id">
      <div class="text-xxs font-semibold uppercase tracking-wide text-fg-muted">
        {{ g.label }}<span v-if="needsModel(g)" class="ml-1.5 font-normal normal-case tracking-normal text-fg-subtle" data-testid="type-endpoints-needs-model">· {{ t('accounts.protocols.served.needsModel') }}</span>
      </div>
      <ul class="space-y-0.5">
        <li v-for="e in g.items" :key="`${e.method} ${e.path}`" class="flex flex-wrap items-center gap-1.5 text-xs" :data-closed="closed(e) || undefined">
          <code class="font-mono" :class="closed(e) ? 'text-fg-subtle line-through decoration-fg-subtle/40' : 'text-gray-700 dark:text-gray-200'">{{ e.method }} {{ e.path }}</code>
          <SHint v-if="!compact" inline size="xs" class="font-mono">{{ e.protocol }}</SHint>
          <SBadge v-if="closed(e)" tone="gray" size="xs" data-testid="endpoint-closed">{{ t('accounts.protocols.served.closed') }}</SBadge>
          <template v-else>
            <ConversionGradeBadge v-if="isConvertedGrade(e.grade)" :grade="e.grade" :via="e.via" />
            <SHint v-if="modelsOf(e)" inline size="xs" :title="modelsOf(e)!.all" data-testid="endpoint-models">{{ modelsOf(e)!.text }}</SHint>
          </template>
        </li>
      </ul>
    </div>
  </div>
  <p v-else class="text-xs text-warning-600 dark:text-warning-400">{{ t('accounts.noEndpoints') }}</p>
</template>
