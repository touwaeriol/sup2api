<script setup lang="ts">
// "Last test" cell of the account list: latency (coloured) + relative time +
// success / failure; the title shows the model, the message and the time.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SSpinner, SStatusDot } from '@sub2api/ui'
import type { AccountLastTest } from '@/api/types'
import { formatDateTime, formatRelative } from '@/utils/format'
import { LATENCY_CLASS, formatLatency, latencyLevel } from './accountTest'

const props = defineProps<{ last?: AccountLastTest | null; testing?: boolean }>()
const { t } = useI18n()

const title = computed(() => {
  const l = props.last
  if (!l) return ''
  return [`${t('accounts.lastTest.model')}: ${l.model || '—'}`, l.protocol ? `${t('accounts.testProtocol')}: ${l.protocol}` : '', l.status !== undefined ? (l.status > 0 ? `HTTP ${l.status}` : t('accounts.testNoHTTPResponse')) : '', l.error_code ? `${t('accounts.testErrorCode')}: ${l.error_code}` : '', l.error_source ? `${t('accounts.testErrorSource')}: ${t(`accounts.testErrorSources.${l.error_source}`)}` : '', l.message ? `${t('accounts.message')}: ${l.message}` : '', formatDateTime(l.at)].filter(Boolean).join('\n')
})
</script>

<template>
  <div class="min-w-[4.5rem] text-xs" data-testid="account-last-test">
    <span v-if="testing" class="inline-flex items-center gap-1.5 text-fg-muted"><SSpinner size="sm" />{{ t('accounts.lastTest.testing') }}</span>
    <div v-else-if="last" :title="title">
      <div class="flex items-center gap-1.5">
        <SStatusDot :tone="last.ok ? 'success' : 'danger'" />
        <span v-if="last.ok" class="font-mono tabular-nums" :class="LATENCY_CLASS[latencyLevel(last.latency_ms)]">{{ formatLatency(last.latency_ms) }}</span>
        <span v-else class="font-medium text-danger-600 dark:text-danger-400">{{ t('accounts.lastTest.failed') }}</span>
      </div>
      <div class="mt-0.5 text-xxs text-fg-subtle">{{ formatRelative(last.at, t) }}</div>
    </div>
    <span v-else class="text-fg-subtle">{{ t('accounts.lastTest.never') }}</span>
  </div>
</template>
