<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { SAlert, SBadge, SButton, SCard, SEmpty, SHint, SIcon, SLink, SPageHeader, SSpinner, confirm, toast } from '@sub2api/ui'
import type { ConsentResult, HostPermissionReview, PluginReview, PluginRoleGrant, ReviewDiffItem, ReviewUserPermission } from '@/api/types'
import { lt } from '@/i18n'
import { errorMessage, notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import { dropReview, getReview } from './reviewCache'
import { RISK_ORDER, asArray, capabilityIdsOf, riskOf, type Risk } from './pluginUtil'
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
import PermissionMatrix from '@/components/permission/PermissionMatrix.vue'
import type { MatrixPerm } from '@/components/permission/useGrantMatrix'
import StickyActionBar from '@/components/StickyActionBar.vue'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { P } from '@/generated/permissions'

const { t } = useI18n()
const tr = useOptionalT()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const key = computed(() => String(route.params.key))
const version = computed(() => String(route.params.version))

const review = ref<PluginReview | null>(null)
const loading = ref(true)
const loadError = ref('')
/** No reachable node holds the version's package (410 package_gone): nothing to review or approve. */
const packageGone = ref(false)
const fromCache = ref(false)
const submitting = ref(false)
const rejecting = ref(false)
const showMissing = ref(false)

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
/** Approved or rejected: the page goes away without asking about the matrix. */
const settled = ref(false)
onBeforeRouteLeave(() => settled.value || confirmDiscard(consent.matrix.dirty.value))

/** Granting db.schema to this plugin takes the override as well (review.needs_db_schema_unconfined). */
const DB_SCHEMA = 'db.schema'
const DB_SCHEMA_UNCONFINED = P.PluginGrantDbSchemaUnconfined

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

const missingRequired = computed(() => rows.value.filter((r) => !r.optional && !checked[r.id]))
const blockedByPermission = computed(() => rows.value.filter((r) => !r.optional && !r.allowed))
const signatureOk = computed(() => review.value?.signature_status === 'valid')
const isUpgrade = computed(() => !!review.value?.diff)
// Grants to roles the caller may not name go to a super admin's review instead of blocking (D2).
const canSubmit = computed(() => !!review.value && review.value.host_compat_ok !== false && blockedByPermission.value.length === 0)
const { colon, hpLabel } = useConsentLabels()

function sigLabel(s: string | undefined): string {
  const v = s || 'unknown'
  return tr(`plugins.signature.${v}`, undefined, v)
}

// ------------------------------------------------------------------ "provides" formatting

const capabilityIds = computed(() => capabilityIdsOf(review.value?.capabilities))

// ------------------------------------------------------------------ load / submit

async function load() {
  loading.value = true
  loadError.value = ''
  fromCache.value = false
  packageGone.value = false
  try {
    review.value = await api.get<PluginReview>(
      `/plugins/${encodeURIComponent(key.value)}/versions/${encodeURIComponent(version.value)}/review`
    )
  } catch (e) {
    // The review kept from the upload would offer to approve a package that is gone.
    packageGone.value = isApiError(e) && e.code === 'package_gone'
    if (packageGone.value) dropReview(key.value, version.value)
    const cached = packageGone.value ? undefined : getReview(key.value, version.value)
    if (cached) {
      review.value = cached
      fromCache.value = true
    } else {
      review.value = null
      loadError.value = errorMessage(e)
    }
  } finally {
    loading.value = false
  }
  if (review.value) initChecks()
}

async function submit() {
  const r = review.value
  if (!r || !canSubmit.value) return
  if (missingRequired.value.length) {
    showMissing.value = true
    toast(t('plugins.consent.missingRequired', { list: missingRequired.value.map((x) => hpLabel(x.id)).join(', ') }), 'error')
    return
  }
  // A carried grant left ticked is not sent: the server keeps it.
  const grants = rows.value.filter((x) => checked[x.id] && !x.carried).map((x) => (x.scope ? { permission: x.id, scope: x.scope } : { permission: x.id }))
  const denied = rows.value.filter((x) => !checked[x.id]).map((x) => x.id)
  submitting.value = true
  try {
    const res = await api.post<ConsentResult>(`/plugins/${encodeURIComponent(key.value)}/versions/${encodeURIComponent(version.value)}/consent`, {
      grants,
      denied,
      new_permission_roles: consent.payload()
    })
    dropReview(key.value, version.value)
    settled.value = true
    toast(t('plugins.consent.approved', { name: lt(r.name) || r.plugin_key, version: r.version }), 'success')
    if (res?.withheld?.length) toast(t('plugins.matrix.withheld', { n: res.withheld.length }), 'info')
    router.push({ path: `/plugins/${encodeURIComponent(key.value)}`, query: { consented: version.value } })
  } catch (e) {
    // The matrix locks critical rows for a non-super caller; a refusal still says why (D3).
    if (isApiError(e) && e.details?.reason === 'critical') toast(t('plugins.matrix.criticalOnly'), 'error')
    else notifyError(e)
  } finally {
    submitting.value = false
  }
}

async function reject() {
  const ok = await confirm({
    title: t('plugins.consent.rejectTitle'),
    message: t('plugins.consent.rejectConfirm', { name: lt(review.value?.name) || key.value, version: version.value }),
    danger: true,
    confirmText: t('plugins.consent.reject')
  })
  if (!ok) return
  rejecting.value = true
  try {
    await api.post(`/plugins/${encodeURIComponent(key.value)}/versions/${encodeURIComponent(version.value)}/reject`)
    dropReview(key.value, version.value)
    settled.value = true
    toast(t('plugins.consent.rejected'), 'success')
    router.push('/plugins')
  } catch (e) {
    notifyError(e)
  } finally {
    rejecting.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-5xl pb-24">
    <SPageHeader :title="review ? `${isUpgrade ? t('plugins.consent.titleUpgrade') : t('plugins.consent.titleInstall')}${colon}${lt(review.name) || review.plugin_key}` : t('nav.crumbs.pluginConsent')">
      <template v-if="review" #title-extra>
        <span class="font-mono text-sm text-gray-500">v{{ review.version }}</span>
      </template>
    </SPageHeader>

    <div v-if="loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!review">
      <SEmpty icon="warning" :text="packageGone ? t('plugins.consent.packageGone', { version }) : t('plugins.consent.notFound')" data-testid="consent-unavailable">
        <SHint v-if="loadError && !packageGone" size="xs" tone="danger">{{ loadError }}</SHint>
        <SButton size="sm" @click="load">{{ t('common.refresh') }}</SButton>
      </SEmpty>
    </SCard>

    <template v-else>
      <!-- header -->
      <SCard>
        <div class="flex flex-wrap items-start gap-4">
          <PluginAvatar :name="lt(review.name)" :plugin-key="review.plugin_key" size="lg" />
          <div class="min-w-0 flex-1">
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
            <SHint v-if="fromCache" size="xs" class="mt-2">{{ t('plugins.consent.fromCache') }}</SHint>
          </div>
        </div>
      </SCard>

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
      <HostPermissionTable :rows="rows" :checked="checked" :show-missing="showMissing" @toggle="toggle" />

      <!-- footer -->
      <StickyActionBar class="mt-4">
        <p v-if="!review.host_compat_ok" class="text-danger-600 dark:text-danger-400">{{ t('plugins.consent.incompatibleTitle') }}</p>
        <p v-else-if="blockedByPermission.length" class="text-warning-700 dark:text-warning-300">
          {{ t('plugins.consent.blockedByPermission', { list: blockedByPermission.map((x) => hpLabel(x.id)).join(', ') }) }}
        </p>
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
          <SButton variant="danger" :loading="rejecting" :disabled="submitting" @click="reject">{{ t('plugins.consent.reject') }}</SButton>
          <SButton variant="primary" :loading="submitting" :disabled="!canSubmit || rejecting" icon="shield" data-testid="consent-approve" @click="submit">
            {{ isUpgrade ? t('plugins.consent.approveUpgrade') : t('plugins.consent.approve') }}
          </SButton>
        </template>
      </StickyActionBar>
    </template>
  </div>
</template>
