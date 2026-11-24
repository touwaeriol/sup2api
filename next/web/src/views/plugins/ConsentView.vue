<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SAlert, SBadge, SButton, SCard, SHint, SIcon, SLink, SPageHeader, SSpinner, toast } from '@sub2api/ui'
import type { ConsentRequest } from '@/api/pluginConsent'
import type { DatabaseAccess, HostPermissionReview, PluginRoleGrant, ReviewDiffItem, ReviewUserPermission } from '@/api/types'
import { lt } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useConsentTarget } from './consent/useConsentTarget'
import InstallSteps from './consent/InstallSteps.vue'
import ConsentUnavailable from './consent/ConsentUnavailable.vue'
import { RISK_ORDER, asArray, capabilityIdsOf, displayPluginId, riskOf, type Risk } from './pluginUtil'
import PluginAvatar from './parts/PluginAvatar.vue'
import TrustBadge from './parts/TrustBadge.vue'
import ProvidesSummary from './consent/ProvidesSummary.vue'
import HostPermissionTable from './consent/HostPermissionTable.vue'
import type { HPRow } from './consent/hostPermission'
import { useConsentLabels } from './consent/hostPermission'
import { useOptionalT } from '@/composables/optionalText'
import { permissionFate, useConsentMatrix } from './consent/consentMatrix'
import ConsentNeed from './consent/ConsentNeed.vue'
import UpgradePermissionChanges from './consent/UpgradePermissionChanges.vue'
import DatabaseMigrations from './consent/DatabaseMigrations.vue'
import SourceCheck from './consent/SourceCheck.vue'
import NewRiskAck from './consent/NewRiskAck.vue'
import EgressDomains from './consent/EgressDomains.vue'
import { useMarketOrigin } from './consent/useMarketOrigin'
import PluginIdText from './parts/PluginIdText.vue'
import DatabaseAccessList from './parts/DatabaseAccessList.vue'
import ScopeChips from './parts/ScopeChips.vue'
import PermissionMatrix from '@/components/permission/PermissionMatrix.vue'
import type { MatrixPerm } from '@/components/permission/useGrantMatrix'
import StickyActionBar from '@/components/StickyActionBar.vue'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { P } from '@/generated/permissions'

const { t } = useI18n()
const tr = useOptionalT()
const auth = useAuthStore()

const { source, review, loading, failure, busy, settled, isUpgrade, load, approve, reject, cancel } = useConsentTarget()
const showMissing = ref(false)
/** A market install: the channel and index entry its source check shows. */
const origin = useMarketOrigin(source.market)

const checked = reactive<Record<string, boolean>>({})
const consent = useConsentMatrix(review)
const roleGrants = computed(() => asArray<PluginRoleGrant>(review.value?.new_role_grants))
const grantsOf = (p: MatrixPerm) => roleGrants.value.filter((g) => g.permission === p.action)
/** The roles the consent gives a risk's permissions to, by name. */
const roleNames = (keys: string[]) => keys.map((k) => lt(consent.roles.value.find((r) => r.key === k)?.name) || k).join(t('roles.listSep'))
/** An upgrade's user permissions in one line of the changes card: created, declared again, removed (the details are below). */
const userChanges = computed(() => {
  const perms = asArray<ReviewUserPermission>(review.value?.user_permissions)
  const count = (fate: ReturnType<typeof permissionFate>) => perms.filter((p) => permissionFate(p) === fate).length
  return { added: count('created'), revived: count('revived'), removed: asArray(review.value?.removed_user_permissions).length }
})
const anyUserChange = computed(() => userChanges.value.added + userChanges.value.revived + userChanges.value.removed > 0)
onBeforeRouteLeave(() => settled.value || confirmDiscard(consent.matrix.dirty.value))

/** Granting db.schema to this plugin takes the override as well (review.needs_db_schema_unconfined). */
const DB_SCHEMA = 'db.schema'
const DB_SCHEMA_UNCONFINED = P.PluginGrantDbSchemaUnconfined
/** The tables outside its own the plugin asks for are db.core_read's scope: listed under it in place of the raw scope. */
const DB_CORE_READ = 'db.core_read'
const databaseAccess = computed(() => asArray<DatabaseAccess>(review.value?.database_access))
/** An upgrade's db.core_read grant in force, to mark what the request adds to it. */
const grantedAccess = (row: HPRow) => (row.current_status === 'granted' ? asArray<string>(row.current_scope?.access) : undefined)

function riskPermOf(hp: HostPermissionReview, risk: Risk): string | null {
  if (hp.requires) return hp.requires
  if (risk === 'critical') return P.PluginGrantCritical
  if (risk === 'high') return P.PluginGrantHigh
  return null
}

/** The console permissions granting `hp` takes; the first one the caller lacks is shown. */
function requiredPermsOf(hp: HostPermissionReview, risk: Risk): string[] {
  const out = [riskPermOf(hp, risk), hp.id === DB_SCHEMA && review.value?.needs_db_schema_unconfined ? DB_SCHEMA_UNCONFINED : null]
  return out.filter((p): p is string => !!p)
}

const rows = computed<HPRow[]>(() => {
  const r = review.value
  if (!r) return []
  const added = new Set(asArray<ReviewDiffItem>(r.diff?.added).map((d) => d.id))
  const widened = new Set(asArray<ReviewDiffItem>(r.diff?.widened).map((d) => d.id))
  return asArray<HostPermissionReview>(r.host_permissions)
    .map((hp) => {
      const risk = riskOf(hp.id, hp.risk)
      const diffTag = added.has(hp.id) ? ('added' as const) : widened.has(hp.id) ? ('widened' as const) : null
      // The server carries over a grant that already covers the request (consent.go decide).
      const carried = !!r.diff && hp.current_status === 'granted' && !diffTag
      const required = requiredPermsOf(hp, risk)
      const lacking = carried ? undefined : required.find((p) => !auth.hasDynamic(p))
      // A revoked low-risk permission is not granted automatically: it is sent only when ticked.
      const locked = risk === 'low' && !hp.revoked
      return { ...hp, risk, requiredPerm: lacking ?? required[0] ?? null, allowed: !lacking, locked, diffTag, carried }
    })
    .sort((a, b) => RISK_ORDER[a.risk] - RISK_ORDER[b.risk] || a.id.localeCompare(b.id))
})

function initChecks() {
  for (const k of Object.keys(checked)) delete checked[k]
  for (const row of rows.value) {
    if (row.locked || row.carried) checked[row.id] = true
    else if (!row.allowed || row.revoked) checked[row.id] = false
    else if (row.risk === 'medium') checked[row.id] = !row.optional
    else checked[row.id] = false
  }
}

function toggle(row: HPRow, v: boolean) {
  if (row.locked || !row.allowed) return
  checked[row.id] = v
}

/** The high and critical host permissions this version newly asks for: approving waits for their acknowledgement. */
const newRisk = computed(() => rows.value.filter((r) => r.diffTag === 'added' && (r.risk === 'high' || r.risk === 'critical')))
const acknowledged = ref(false)
const needsAck = computed(() => newRisk.value.length > 0 && !acknowledged.value)
const missingRequired = computed(() => rows.value.filter((r) => !r.optional && !checked[r.id]))
const blockedByPermission = computed(() => rows.value.filter((r) => !r.optional && !r.allowed))
// A market install is vouched for by its index, not its signature (CONTRACTS §136).
const signatureOk = computed(() => ['valid', 'indexed'].includes(review.value?.signature_status ?? ''))
// Grants to roles the caller may not name go to a super admin's review instead of blocking (D2).
const canSubmit = computed(() => !!review.value && review.value.host_compat_ok !== false && blockedByPermission.value.length === 0 && !needsAck.value)
const { colon, hpLabel } = useConsentLabels()

function sigLabel(s: string | undefined): string {
  const v = s || 'unknown'
  return tr(`plugins.signature.${v}`, undefined, v)
}

// ------------------------------------------------------------------ "provides" formatting

const capabilityIds = computed(() => capabilityIdsOf(review.value?.capabilities))

// ------------------------------------------------------------------ submit

/** What the ticks and the matrix grant: a carried grant left ticked is not sent, the server keeps it. */
function consentRequest(): ConsentRequest {
  const grants = rows.value.filter((x) => checked[x.id] && !x.carried).map((x) => (x.scope ? { permission: x.id, scope: x.scope } : { permission: x.id }))
  const denied = rows.value.filter((x) => !checked[x.id]).map((x) => x.id)
  return { grants, denied, new_permission_roles: consent.payload() }
}

function submit() {
  if (!review.value || !canSubmit.value) return
  if (missingRequired.value.length && review.value.consent_status !== 'approved') {
    showMissing.value = true
    toast(t('plugins.consent.missingRequired', { list: missingRequired.value.map((x) => hpLabel(x.id)).join(', ') }), 'error')
    return
  }
  return approve(consentRequest())
}

// A review loaded again (409 package_changed) starts from its own ticks.
watch(review, (r) => {
  acknowledged.value = false
  if (r) initChecks()
})
onMounted(() => Promise.all([load(), origin.load()]))
</script>

<template>
  <div class="mx-auto max-w-5xl pb-24">
    <SPageHeader :title="review ? `${isUpgrade ? t('plugins.consent.titleUpgrade') : t('plugins.consent.titleInstall')}${colon}${lt(review.name) || displayPluginId(review.plugin_key)}` : t(source.reject ? 'nav.crumbs.pluginConsent' : 'nav.crumbs.pluginInstall')">
      <template v-if="review" #title-extra>
        <span class="font-mono text-sm text-gray-500">v{{ review.version }}</span>
      </template>
    </SPageHeader>

    <div v-if="loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <ConsentUnavailable v-else-if="!review" :failure="failure" :source="source" @retry="load" @back="cancel" />

    <template v-else>
      <!-- header -->
      <SCard>
        <div class="flex flex-wrap items-start gap-4">
          <PluginAvatar :name="lt(review.name)" :plugin-key="review.plugin_key" size="lg" />
          <div class="min-w-0 flex-1">
            <div class="mb-2 flex flex-wrap items-center gap-2">
              <span class="font-semibold">{{ lt(review.name) || displayPluginId(review.plugin_key) }}</span>
              <PluginIdText :id="review.plugin_key" class="text-sm text-fg-muted" />
              <span v-if="review.upgrade_from" class="font-mono text-sm" data-testid="consent-version-change">v{{ review.upgrade_from }} → <b class="text-warning-700 dark:text-warning-300">v{{ review.version }}</b></span>
            </div>
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
              <span class="inline-flex items-center gap-2">
                <SHint inline>{{ t('plugins.publisher') }}</SHint>
                <span class="font-medium">{{ review.publisher || '—' }}</span>
                <TrustBadge :trust="review.trust" />
              </span>
              <span class="inline-flex items-center gap-1.5">
                <SIcon :name="signatureOk ? 'check' : 'warning'" class="h-4 w-4" :class="signatureOk ? 'text-emerald-500' : 'text-red-500'" />
                <span :class="signatureOk ? '' : 'font-medium text-red-600 dark:text-red-400'">{{ t('plugins.consent.signature') }}{{ colon }}{{ sigLabel(review.signature_status) }}</span>
              </span>
              <span class="inline-flex items-center gap-1.5">
                <SIcon :name="review.host_compat_ok ? 'check' : 'x'" class="h-4 w-4" :class="review.host_compat_ok ? 'text-emerald-500' : 'text-red-500'" />
                <span>{{ t('plugins.consent.hostCompat') }} <code class="font-mono text-xs">{{ review.host_compat || '—' }}</code></span>
              </span>
            </div>
            <div v-if="capabilityIds.length" class="mt-2 flex flex-wrap gap-1">
              <SBadge v-for="c in capabilityIds" :key="c" tone="gray">{{ c }}</SBadge>
            </div>
          </div>
        </div>
      </SCard>
      <SourceCheck v-if="source.market" :review="review" :channel="origin.channel.value" :version="origin.version.value" />
      <InstallSteps v-if="!source.reject" :upgrade="isUpgrade" :version="review.version" :from="review.upgrade_from" :approved="review.consent_status === 'approved'" />

      <!-- warnings -->
      <SAlert v-if="!review.host_compat_ok" tone="danger" icon="x" :title="t('plugins.consent.incompatibleTitle')" class="mt-4">
        {{ t('plugins.consent.incompatibleBody', { range: review.host_compat || '—' }) }}
      </SAlert>
      <SAlert v-if="review.request_access" :title="t('plugins.consent.requestAccessTitle')" class="mt-4" data-testid="consent-request-access">
        {{ lt(review.request_access) }}
      </SAlert>
      <SAlert v-if="!signatureOk" tone="warning" :title="t('plugins.consent.signatureWarnTitle', { status: sigLabel(review.signature_status) })" class="mt-4">
        {{ t('plugins.consent.signatureWarnBody') }}
      </SAlert>

      <!-- upgrade diff -->
      <SCard v-if="review.diff" :title="t('plugins.consent.diffTitle')" class="mt-4">
        <div class="space-y-2 text-sm">
          <div v-if="review.diff.added?.length" class="flex flex-wrap items-center gap-1.5">
            <span class="w-24 shrink-0 font-medium text-red-600 dark:text-red-400">+ {{ t('plugins.consent.diffAdded') }}</span>
            <SBadge v-for="p in review.diff.added" :key="p.id" tone="danger">{{ hpLabel(p.id) }}</SBadge>
          </div>
          <div v-if="review.diff.widened?.length" class="flex flex-wrap items-center gap-1.5">
            <span class="w-24 shrink-0 font-medium text-orange-600 dark:text-orange-400">↑ {{ t('plugins.consent.diffWidened') }}</span>
            <SBadge v-for="p in review.diff.widened" :key="p.id" tone="warning">{{ hpLabel(p.id) }}</SBadge>
          </div>
          <div v-if="review.diff.removed?.length" class="flex flex-wrap items-center gap-1.5">
            <SHint inline class="w-24 shrink-0 font-medium">− {{ t('plugins.consent.diffRemoved') }}</SHint>
            <SBadge v-for="p in review.diff.removed" :key="p.id" tone="gray"><span class="line-through">{{ hpLabel(p.id) }}</span></SBadge>
          </div>
          <SHint v-if="!review.diff.added?.length && !review.diff.widened?.length && !review.diff.removed?.length">
            {{ t('plugins.consent.diffNone') }}
          </SHint>
          <!-- The user permissions too, in one line: their matrix and changes are further down. -->
          <div v-if="anyUserChange" class="flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-line pt-2" data-testid="consent-diff-user">
            <span class="font-medium">{{ t('plugins.permissions.userTitle') }}</span>
            <SHint inline>{{ t('plugins.consent.diffUser', userChanges) }}</SHint>
            <SLink :href="consent.perms.value.length ? '#consent-user-permissions' : '#upgrade-permission-changes'" class="text-xs">{{ t('plugins.consent.diffUserSee') }}</SLink>
          </div>
        </div>
      </SCard>

      <!-- what the plugin provides -->
      <ProvidesSummary :review="review" />

      <!-- the roles the permissions the consent creates go to -->
      <SCard
        v-if="consent.perms.value.length"
        id="consent-user-permissions"
        :title="t('plugins.permissions.userTitle')"
        :subtitle="t('plugins.matrix.hint', { n: consent.perms.value.length }, consent.perms.value.length)"
        class="mt-4"
        data-testid="permission-matrix"
      >
        <PermissionMatrix :matrix="consent.matrix" :groups="review.permission_groups" :plugin-key="review.plugin_key" collapse-custom>
          <template v-if="isUpgrade" #trailing-head>{{ t('plugins.matrix.needsConfirm') }}</template>
          <template v-if="isUpgrade" #trailing="{ perm }"><ConsentNeed :grants="grantsOf(perm)" /></template>
        </PermissionMatrix>
      </SCard>
      <UpgradePermissionChanges v-if="isUpgrade" :review="review" />

      <!-- host permissions -->
      <HostPermissionTable :rows="rows" :checked="checked" :show-missing="showMissing" @toggle="toggle">
        <template #scope="{ row }">
          <DatabaseAccessList v-if="row.id === DB_CORE_READ && databaseAccess.length" :access="databaseAccess" :granted="grantedAccess(row)" class="mt-2" />
          <ScopeChips v-else :scope="row.scope" class="mt-1" />
        </template>
      </HostPermissionTable>
      <DatabaseMigrations :review="review" />
      <EgressDomains :review="review" />
      <NewRiskAck v-model="acknowledged" :rows="newRisk" />

      <!-- footer -->
      <StickyActionBar class="mt-4">
        <p v-if="!review.host_compat_ok" class="text-danger-600 dark:text-danger-400">{{ t('plugins.consent.incompatibleTitle') }}</p>
        <p v-else-if="blockedByPermission.length" class="text-warning-700 dark:text-warning-300">
          {{ t('plugins.consent.blockedByPermission', { list: blockedByPermission.map((x) => hpLabel(x.id)).join(', ') }) }}
        </p>
        <p v-else-if="needsAck" class="text-danger-600 dark:text-danger-400" data-testid="consent-ack-required">{{ t('pluginInstall.ackRequired') }}</p>
        <p v-else-if="missingRequired.length" class="text-fg-muted">
          {{ t('plugins.consent.stillRequired', { n: missingRequired.length }) }}
        </p>
        <p v-if="consent.summary.value.sensitive.perms" class="text-fg-muted" data-testid="consent-summary-sensitive">
          {{ t('plugins.matrix.summarySensitive', { n: consent.summary.value.sensitive.perms, roles: roleNames(consent.summary.value.sensitive.roles) }, consent.summary.value.sensitive.perms) }}
        </p>
        <p v-if="consent.summary.value.critical.perms" class="text-danger-600 dark:text-danger-400" data-testid="consent-summary-critical">
          {{ t('plugins.matrix.summaryCritical', { n: consent.summary.value.critical.perms, roles: roleNames(consent.summary.value.critical.roles) }, consent.summary.value.critical.perms) }}
        </p>
        <p v-if="consent.summary.value.pending" class="text-warning-700 dark:text-warning-300" data-testid="consent-summary-pending">
          {{ t('plugins.matrix.summaryPending', { n: consent.summary.value.pending }, consent.summary.value.pending) }}
        </p>
        <template #actions>
          <SButton v-if="source.reject" variant="danger" :loading="busy === 'reject'" :disabled="!!busy" @click="reject">{{ t('plugins.consent.reject') }}</SButton>
          <SButton v-else :disabled="!!busy" data-testid="consent-cancel" @click="cancel">{{ isUpgrade ? t('pluginInstall.cancelUpgrade') : t('pluginInstall.cancelInstall') }}</SButton>
          <SButton variant="primary" :loading="busy === 'approve'" :disabled="!canSubmit || !!busy" icon="shield" data-testid="consent-approve" @click="submit">
            {{ isUpgrade ? t('plugins.consent.approveUpgrade') : t('plugins.consent.approve') }}
          </SButton>
        </template>
      </StickyActionBar>
    </template>
  </div>
</template>
