<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SHint, SLink } from '@sub2api/ui'
import type { AccountBalance } from '@/api/types'
import { formatBalance, formatRelative } from '@/utils/format'

// The balance of an account (CONTRACTS §52.6): never read yet, an amount (the
// upstream's number and unit as they are, never converted), an unlimited key,
// or a failed read (the last amount kept, marked stale). It is only shown: no
// thresholds, no colours by amount.

const { t } = useI18n()

const props = defineProps<{
  balance: AccountBalance | null | undefined
  refreshing?: boolean
}>()

const emit = defineEmits<{
  refresh: []
}>()

const hasAmount = computed(() => props.balance?.amount !== undefined)
const failure = computed(() => {
  const b = props.balance
  if (!b?.error) return ''
  return t('accounts.balance.errorHint', { error: b.error, time: formatRelative(b.error_at, t) })
})
</script>

<template>
  <div v-if="balance" class="min-w-28 space-y-1 text-xs" data-testid="balance-cell">
    <div class="flex items-center justify-between gap-2">
      <span class="text-fg-subtle">{{ t('accounts.balance.label') }}</span>
      <span v-if="balance.unlimited" class="font-semibold" :title="t('accounts.balance.unlimitedHint')" data-testid="balance-value">{{ t('accounts.balance.unlimited') }}</span>
      <span
        v-else-if="balance.amount !== undefined"
        class="font-mono font-semibold tabular-nums"
        :class="{ 'text-fg-muted': failure }"
        :title="failure ? t('accounts.balance.staleHint') : t('accounts.balance.amountHint')"
        data-testid="balance-value"
      >{{ formatBalance(balance.amount, balance.currency) }}</span>
      <span v-else-if="!failure" class="text-fg-muted" data-testid="balance-value">{{ t('accounts.balance.unread') }}</span>
    </div>
    <div v-if="failure" class="flex items-center justify-between gap-2">
      <SBadge tone="danger" size="xs" :title="failure" data-testid="balance-error">{{ t('accounts.balance.failed') }}</SBadge>
      <span v-if="hasAmount || balance.unlimited" class="text-xxs text-fg-muted">{{ t('accounts.balance.stale') }}</span>
    </div>
    <div class="flex items-center justify-between gap-2 text-xxs text-fg-muted">
      <span>{{ balance.updated_at ? t('accounts.balance.updatedAt', { time: formatRelative(balance.updated_at, t) }) : '' }}</span>
      <SLink
        as="button"
        :disabled="refreshing"
        :title="t('accounts.balance.refreshHint')"
        @click="emit('refresh')"
      >
        {{ refreshing ? t('accounts.balance.refreshing') : t('accounts.balance.refresh') }}
      </SLink>
    </div>
  </div>
  <SHint v-else inline>—</SHint>
</template>
