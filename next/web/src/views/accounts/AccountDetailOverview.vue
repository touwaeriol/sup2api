<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCode, SDescItem, SDescriptions, SHint, SLink, SSectionTitle } from '@sub2api/ui'
import type { Account } from '@/api/types'
import UserRef from '@/components/entity/UserRef.vue'
import { formatDateTime, formatRelative } from '@/utils/format'
import { useAccountTypes } from './accountTypes'
import AccountBalanceCell from './AccountBalanceCell.vue'
import LastTestCell from './AccountLastTest.vue'
import AccountLimitsTable from './AccountLimitsTable.vue'
import AccountQuotaCell from './AccountQuotaCell.vue'
import AccountTypeEndpoints from './AccountTypeEndpoints.vue'
import AccountTypePlatforms from './AccountTypePlatforms.vue'
import AccountUsageCell from './AccountUsageCell.vue'
import { hasBalance, hasQuota } from './accountQuota'
import { coolingDown, statusView } from './accountStatus'

// The overview tab of an account's details: what it is, its status (with a
// reset while it cools down), groups, scheduling, limits, usage, quota and
// balance (each with its refresh), models, the state of its credentials
// (with a manual refresh) and who owns it. The list holds the requests: the
// buttons only ask (reset-status, refresh-quota, refresh-balance,
// refresh-creds), and the `*Refreshing` flags say which run.
const props = defineProps<{
  account: Account
  groupsText?: string
  showOwner?: boolean
  showCreator?: boolean
  testing?: boolean
  quotaRefreshing?: boolean
  balanceRefreshing?: boolean
  credRefreshing?: boolean
  canResetStatus?: boolean
  canRefreshCreds?: boolean
}>()
const emit = defineEmits<{ (e: 'reset-status' | 'refresh-quota' | 'refresh-balance' | 'refresh-creds'): void }>()
const { t } = useI18n()
const accountTypes = useAccountTypes()

const type = computed(() => accountTypes.find(props.account.plugin_key, props.account.type))
const status = computed(() => statusView(t, props.account))

/** The state of the credentials: expired or when they expire, the last refresh, and a failure (warn). */
const credState = computed(() => {
  const r = props.account.refresh
  if (!r) return { text: '', warn: false }
  if (r.error_type === 'auth_rejected') return { text: t('accounts.credRefresh.authRejected'), warn: true }
  const parts: string[] = [expiry(r.expires_at)]
  if (r.last_success_at) parts.push(t('accounts.credRefresh.lastSuccess', { time: formatRelative(r.last_success_at, t) }))
  if (r.error_type === 'transient') parts.push(t('accounts.credRefresh.transient', { error: r.error }))
  return { text: parts.join(' · '), warn: r.error_type === 'transient' }
})
function expiry(at: string | null | undefined): string {
  if (!at) return t('accounts.credRefresh.unknown')
  return new Date(at).getTime() <= Date.now() ? t('accounts.credRefresh.expired') : t('accounts.credRefresh.expiresAt', { time: formatDateTime(at) })
}
</script>

<template>
  <div class="space-y-4">
    <SDescriptions label-width="9rem">
      <SDescItem label="ID">{{ account.id }}</SDescItem>
      <SDescItem :label="t('accounts.type')">
        {{ accountTypes.typeLabel(account.plugin_key, account.type, account.type_label) }}
        <SHint inline size="xs">· {{ accountTypes.pluginName(account.plugin_key) }} <span class="font-mono">({{ account.plugin_key }}/{{ account.type }})</span></SHint>
      </SDescItem>
      <SDescItem v-if="type" :label="t('platforms.supported')"><AccountTypePlatforms :items="type.platforms" /></SDescItem>
      <SDescItem :label="t('accounts.servesEndpoints')">
        <AccountTypeEndpoints v-if="type" :endpoints="type.endpoints" :platforms="type.platforms.map((p) => p.id)" :served="account.served_protocols" />
        <SHint v-else inline size="xs">{{ t('accounts.typeUnavailable') }}</SHint>
      </SDescItem>
      <SDescItem :label="t('common.status')">
        <SBadge :tone="status.tone" dot>{{ status.label }}</SBadge>
        <SHint v-if="status.note" inline size="xs" class="ml-2">{{ status.note }}</SHint>
        <SHint v-if="status.detail" inline size="xs" class="ml-2 whitespace-pre-line" data-testid="account-detail-status-reason">{{ status.detail }}</SHint>
        <SLink v-if="coolingDown(account) && canResetStatus" as="button" class="ml-2 text-xs" :title="t('accounts.resetStatusHint')" @click="emit('reset-status')">{{ t('accounts.resetStatus') }}</SLink>
      </SDescItem>
      <SDescItem :label="t('accounts.groups')">{{ groupsText }}</SDescItem>
      <SDescItem :label="t('accounts.priority')">{{ account.priority }}</SDescItem>
      <SDescItem :label="t('accounts.weight')">{{ account.weight ?? 1 }}</SDescItem>
      <SDescItem :label="t('accounts.listUi.limitsColumn')"><AccountLimitsTable :account="account" class="max-w-xl" /></SDescItem>
      <SDescItem v-if="account.refresh" :label="t('accounts.credRefresh.label')" data-testid="account-cred-refresh">
        <span :class="credState.warn ? 'text-warning-600' : ''" class="text-sm">{{ credState.text }}</span>
        <SLink v-if="canRefreshCreds" as="button" class="ml-2 text-xs" :title="t('accounts.credRefresh.actionHint')" :disabled="credRefreshing" @click="emit('refresh-creds')">{{ t('accounts.credRefresh.action') }}</SLink>
      </SDescItem>
      <SDescItem :label="t('accounts.usage.column')"><AccountUsageCell :account-id="account.id" :usage="account.usage" /></SDescItem>
      <SDescItem v-if="hasQuota(account.quota)" :label="t('accounts.quota.column')"><AccountQuotaCell :quota="account.quota" :refreshing="quotaRefreshing" @refresh="emit('refresh-quota')" /></SDescItem>
      <SDescItem v-if="hasBalance(account.balance)" :label="t('accounts.balance.accountLabel')"><AccountBalanceCell :balance="account.balance" :refreshing="balanceRefreshing" @refresh="emit('refresh-balance')" /></SDescItem>
      <SDescItem :label="t('accounts.models')">
        <span v-if="account.models?.length" class="flex flex-wrap gap-1">
          <SBadge v-for="m in account.models" :key="m" tone="gray"><span class="font-mono">{{ m }}</span></SBadge>
        </span>
        <SHint v-else inline>{{ t('accounts.allModels') }}</SHint>
      </SDescItem>
      <SDescItem :label="t('accounts.modelMapping')">
        <ul v-if="account.model_mapping && Object.keys(account.model_mapping).length" class="space-y-0.5">
          <li v-for="(to, from) in account.model_mapping" :key="from" class="font-mono text-xs">
            {{ from }} <SHint inline size="xs">→</SHint> {{ to }}
          </li>
        </ul>
        <SHint v-else inline>—</SHint>
      </SDescItem>
      <SDescItem :label="t('accounts.schedulable')">{{ account.schedulable ? t('common.yes') : t('common.no') }}</SDescItem>
      <SDescItem :label="t('accounts.lastUsed')">{{ formatDateTime(account.last_used_at) }}</SDescItem>
      <SDescItem :label="t('accounts.lastTest.column')"><LastTestCell :last="account.last_test" :testing="testing" /></SDescItem>
      <SDescItem v-if="showOwner" :label="t('accounts.owner')"><UserRef :id="account.owner_id" :email="account.owner_email" /></SDescItem>
      <SDescItem v-if="showCreator" :label="t('accounts.createdBy')"><UserRef :id="account.created_by" :email="account.created_by_email" /></SDescItem>
      <SDescItem :label="t('common.createdAt')">{{ formatDateTime(account.created_at) }}</SDescItem>
    </SDescriptions>
    <div v-if="account.credentials">
      <SSectionTitle :title="t('accounts.credentials')" />
      <SCode>{{ JSON.stringify(account.credentials, null, 2) }}</SCode>
    </div>
  </div>
</template>
