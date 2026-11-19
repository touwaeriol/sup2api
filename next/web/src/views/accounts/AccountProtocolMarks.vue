<script setup lang="ts">
// The platforms an account serves, under its name in the list
// (DESIGN-2026-11-17-PROTOCOL-BINDING §5.2): the platforms of its type by the
// protocols the account opens ("partial n/m" when it closes some), then the
// platforms some model is converted for, marked. Hovering a mark lists the
// protocols, and the models of a conversion.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AccountType } from '@/api/types'
import PlatformIcon from '@/components/platform/PlatformIcon.vue'
import { platformMarks, type PlatformMark, type ProtocolColumn } from './protocols/protocolBinding'
import { useProtocolLabels } from './protocols/protocolLabels'

const props = defineProps<{ account: Pick<Account, 'protocols' | 'model_protocols'>; accountType?: AccountType }>()
const { t } = useI18n()
const labels = useProtocolLabels()

const marks = computed(() => (props.accountType ? platformMarks(props.account, props.accountType) : []))
const partial = (m: PlatformMark) => !m.converted && m.columns.length < m.total
const sep = computed(() => t('common.listSeparator'))
const ownName = (c: ProtocolColumn) => (c.via === 'adapted' ? t('accounts.protocols.marks.adapted', { name: labels.protocolName(c.key) }) : labels.protocolName(c.key))

function detail(m: PlatformMark): string {
  const platform = labels.platformLabel(m.platform)
  if (m.converted) {
    return t('accounts.protocols.marks.conversion', { platform, protocols: m.columns.map(c => labels.protocolName(c.key)).join(sep.value), n: m.models.length, models: m.models.map(labels.modelName).join(sep.value) })
  }
  if (!m.total) return platform
  if (!m.columns.length) return t('accounts.protocols.marks.ownNone', { platform })
  return t(partial(m) ? 'accounts.protocols.marks.own' : 'accounts.protocols.marks.ownAll', { platform, protocols: m.columns.map(ownName).join(sep.value) })
}
</script>

<template>
  <span v-if="marks.length" class="inline-flex shrink-0 items-center gap-1" data-testid="account-platforms">
    <span v-for="m in marks" :key="`${m.platform}:${m.converted}`" class="inline-flex items-center gap-0.5" :data-converted="m.converted || undefined">
      <span class="relative inline-flex">
        <PlatformIcon :id="m.platform" :detail="detail(m)" />
        <span v-if="m.converted" class="absolute -right-1.5 -top-1.5 rounded bg-warning-500 px-0.5 text-[8px] font-bold leading-3 text-white" aria-hidden="true" data-testid="account-platform-converted">{{ t('accounts.protocols.marks.converted') }}</span>
      </span>
      <span v-if="partial(m)" class="rounded border border-info-400 px-1 text-[10px] leading-3 text-info-600 dark:text-info-300" :title="detail(m)" data-testid="account-platform-partial">{{ t('accounts.protocols.marks.partial', { open: m.columns.length, total: m.total }) }}</span>
      <span v-if="m.converted" class="w-1" aria-hidden="true" />
    </span>
  </span>
</template>
