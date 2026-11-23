<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import { fetchAllPages, fetchFirstPage } from '@/composables/lookups'
import {
  SStatusBadge,
  SDateTime,
  SBadge,
  SBulkBar,
  SButton,
  SChoiceCards,
  SDropdown,
  SField,
  SHint,
  SInput,
  SFormModal,
  SPageHeader,
  SPagination,
  SSelect,
  SSwitch,
  STable,
  confirm,
  toast,
  type ChoiceOption,
  type MenuAction,
  type TableColumn
} from '@sub2api/ui'
import type { Role, User } from '@/api/types'
import { deleteWithConfirm, useStatusOptions } from '@/composables/crud'
import { activeFilterCount, filterFlag, useList } from '@/composables/useList'
import { useSelection } from '@/composables/useSelection'
import { DEFAULT_ROLE, SUPPLIER_ROLE, isPlainUser, useAssignableRoles, withSupplierUser } from '@/composables/useAssignableRoles'
import { useScope } from '@/composables/useScope'
import { useAuthStore } from '@/stores/auth'
import { lt } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { formatMoney } from '@/utils/format'
import EntityPicker from '@/components/EntityPicker.vue'
import UserRef from '@/components/entity/UserRef.vue'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import AdjustBalanceModal from '@/views/ledger/AdjustBalanceModal.vue'
import ResetPasswordDialog from './ResetPasswordDialog.vue'
import TransferSuperAdminDialog from './TransferSuperAdminDialog.vue'
import UserCreateDialog from './UserCreateDialog.vue'
import UserPricingDrawer from './UserPricingDrawer.vue'
import UserSegmentMoveDialog from './UserSegmentMoveDialog.vue'
import { segmentFilterOptions, useNamedSegments } from './userSegments'
import { OWNER_ERRORS } from './userOwner'
import { useScopeLabels } from '@/components/permission/scopeLabels'
import { P, type PermRef } from '@/generated/permissions'

const { t } = useI18n()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
/** A filter the address bar may set: other pages link here with ?role= or ?segment=. */
const fromQuery = (key: string) => (typeof route.query[key] === 'string' ? route.query[key] : '')
const scope = useScope()
// The users in reach of user:read (CONTRACTS §103.3): every user, or the
// caller's team; owner_id narrows to those directly under a user, direct to
// those directly under the caller.
const list = useList<User>('/users', { q: '', status: '', role: fromQuery('role'), segment: fromQuery('segment'), owner_id: '', direct: '' })
const teamOnly = computed(() => scope.level(P.UserRead) === 'team')
const onlyDirect = filterFlag(list.filters, 'direct')

// ------------------------------------------------------------------ roles lookup

const roles = ref<Role[]>([])
async function loadRoles() {
  if (!auth.has('role:read') || roles.value.length) return
  try {
    roles.value = await fetchFirstPage<Role>('/roles')
  } catch (e) {
    notifyError(e)
  }
}
loadRoles()

function roleName(key: string) {
  const r = roles.value.find((x) => x.key === key)
  return r ? lt(r.name) || key : key
}

const scopeLabels = useScopeLabels()
/** The resource scopes of a role, for its badge's tooltip (CONTRACTS §21); undefined without any. */
function roleScopes(key: string): string | undefined {
  const text = scopeLabels.summary(roles.value.find((x) => x.key === key)?.permission_keys ?? [])
  return text ? t('users.roleScopes', { list: text }) : undefined
}

// ------------------------------------------------------------------ user groups (segments)

// Moving users takes the user groups to choose from (segment:read) and the right to change who is in them.
const canManageSegments = computed(() => auth.has('segment:read') && auth.has('segment:manage'))
const segments = useNamedSegments()
const segmentOptions = computed(() => segmentFilterOptions(segments.value, { all: t('users.allSegments'), personal: t('pricing.segment.personal') }))

// ------------------------------------------------------------------ table

const showBalance = computed(() => list.items.value.some((u) => u.balance !== undefined && u.balance !== null))

const columns = computed<TableColumn[]>(() => {
  const cols: TableColumn[] = [
    { key: 'id', label: t('common.id'), width: '64px' },
    { key: 'email', label: t('users.email'), mobile: 'title' },
    { key: 'display_name', label: t('users.displayName'), mobile: 'subtitle' },
    { key: 'roles', label: t('users.roles'), mobile: 'meta' },
    { key: 'segment', label: t('users.segment'), mobile: 'meta' },
    // The owner (所属) always: user:read reaches every user or a team, past one's own, as the accounts list's does.
    { key: 'owner', label: t('users.owner'), mobile: 'meta' }
  ]
  if (showBalance.value) cols.push({ key: 'balance', label: t('users.balance'), align: 'right', mobile: 'meta' })
  cols.push(
    { key: 'status', label: t('common.status'), mobile: 'status' },
    { key: 'last_login_at', label: t('users.lastLogin'), mobile: 'meta' },
    { key: 'created_at', label: t('common.createdAt') },
    { key: 'actions', label: t('common.actions'), align: 'right' }
  )
  return cols
})

const statusFilterOptions = useStatusOptions(true)
const statusOptions = useStatusOptions()


const roleOptions = computed(() => [
  { value: '', label: t('users.allRoles') },
  ...roles.value.map((r) => ({ value: r.key, label: lt(r.name) || r.key }))
])

const isSuper = (u: User) => !!u.roles?.includes('super_admin')

// The users the caller may act on (CONTRACTS §43.6): actions that change a
// user are only offered on them; the server decides either way.
const managed = useAssignableRoles()

// The super admin can only be changed by itself (CONTRACTS §43.6): no actions
// on its row for others; its own row only hands the role over.
function rowActions(u: User): MenuAction[] {
  const self = u.id === auth.me?.id
  if (isSuper(u)) return self ? [{ key: 'transfer', label: t('users.transfer.action'), danger: true }] : []
  const manageable = managed.manageable(u.roles)
  const may = (perm: PermRef) => auth.has(perm) && manageable
  return [
    // user:update at any scope: the list holds only the users in reach. The
    // password and delete need the unscoped keys, never the team's (CONTRACTS §103.1).
    { key: 'edit', label: t('common.edit'), hidden: !(manageable && auth.hasAction(P.UserUpdate)) },
    { key: 'roles', label: t('users.assignRoles'), hidden: !may('role:assign') },
    { key: 'pricing', label: t('users.pricing.action'), hidden: !auth.has('segment:read') },
    { key: 'ledger', label: t('users.ledger'), hidden: !auth.has('balance:all:read') },
    // PATCH /users/:id needs user:update, a password for another user also user:password:reset.
    { key: 'password', label: t('users.resetPassword.action'), hidden: !may('user:update') || !auth.has('user:password:reset') || self },
    // One's own second factor is reset under /me with a verification.
    { key: 'security', label: t('users.resetSecurity'), hidden: !may('user:password:reset') || self },
    // balance:adjust reaches plain users; users holding other roles also take balance:adjust:any.
    { key: 'balance', label: t('users.adjustBalance'), hidden: !may('balance:adjust') || !(isPlainUser(u.roles) || auth.has('balance:adjust:any')) },
    // Nobody disables or deletes themselves (a lock-out guard, not a permission).
    { key: 'delete', label: t('common.delete'), danger: true, hidden: !may('user:delete') || self }
  ]
}

function hasAnyAction(u: User) {
  return rowActions(u).some((a) => !a.hidden)
}

async function resetSecurity(u: User) {
  const ok = await confirm({ title: t('users.resetSecurity'), message: t('users.resetSecurityConfirm', { name: u.email }), danger: true })
  if (!ok) return
  try {
    await api.del(`/users/${u.id}/security`)
    toast(t('users.resetSecurityDone'), 'success')
  } catch (e) {
    notifyError(e)
  }
}

function onAction(u: User, key: string) {
  if (key === 'edit') openEdit(u)
  else if (key === 'roles') openRoles(u)
  else if (key === 'pricing') openPricing(u)
  else if (key === 'ledger') router.push({ path: '/ledger', query: { user_id: String(u.id) } })
  else if (key === 'balance') openBalance(u)
  else if (key === 'password') openPassword(u)
  else if (key === 'security') resetSecurity(u)
  else if (key === 'transfer') transferOpen.value = true
  else if (key === 'delete') remove(u)
}

// ------------------------------------------------------------------ selection + move to a user group

const selection = useSelection<User>({ items: list.items, key: (u) => u.id, selectable: () => canManageSegments.value, loadAll: () => fetchAllPages<User>('/users', { ...list.filters }) })
const selectionKeys = computed({ get: () => selection.keys.value as unknown[], set: (v: unknown[]) => selection.update(v) })
// A changed filter starts a new selection: rows outside it would be acted on unseen.
watch(() => ({ ...list.filters }), () => selection.clear(), { deep: true })
const moveOpen = ref(false)

function onMoved() {
  selection.clear()
  list.reload()
}

// ------------------------------------------------------------------ create

const createOpen = ref(false)

function openCreate() {
  loadRoles()
  createOpen.value = true
}

// ------------------------------------------------------------------ edit

const editUser = ref<User | null>(null)
const editOpen = ref(false)
const saving = ref(false)
const editErrors = ref<Record<string, string>>({})
const editForm = reactive({ display_name: '', status: 'active', owner_id: '' })
// Naming the owner takes user:update over every user; a team holder's users stay under it.
const canSetOwner = computed(() => auth.has(P.UserUpdate))

function openEdit(u: User) {
  editUser.value = u
  Object.assign(editForm, { display_name: u.display_name, status: u.status, owner_id: u.owner_id ? String(u.owner_id) : '' })
  editErrors.value = {}
  editOpen.value = true
}

async function submitEdit() {
  if (!editUser.value) return
  saving.value = true
  editErrors.value = {}
  // Sent only when changed; 0 takes the owner away.
  const owner = Number(editForm.owner_id) || 0
  try {
    await api.patch(`/users/${editUser.value.id}`, {
      display_name: editForm.display_name.trim(),
      status: editForm.status,
      owner_id: canSetOwner.value && owner !== (editUser.value.owner_id ?? 0) ? owner : undefined
    })
    toast(t('common.updated'), 'success')
    editOpen.value = false
    list.reload()
  } catch (e) {
    applyFormError(e, editErrors, OWNER_ERRORS)
  } finally {
    saving.value = false
  }
}

// ------------------------------------------------------------------ roles

const rolesOpen = ref(false)
const rolesUser = ref<User | null>(null)
const rolesSel = ref<ChoiceOption['value'][]>([])

// The managed roles, plus the roles the user holds that are not: those stay
// checked and locked and go back unchanged (the server checks the difference).
// A managed role the caller may not grant can be kept but not added.
const roleChoices = computed<ChoiceOption[]>(() => {
  const held = rolesUser.value?.roles || []
  const locked = held.filter((k) => !managed.get(k))
  return [
    ...managed.roles.value.map((r) => ({
      value: r.key,
      label: lt(r.name) || r.key,
      description: r.grantable ? undefined : t('users.roleNotGrantable', { keys: [...(r.missing || []), ...(r.missing_assignable || [])].join(', ') || r.key }),
      disabled: !r.grantable && !held.includes(r.key)
    })),
    ...locked.map((k) => ({ value: k, label: roleName(k), description: t('users.roleLocked'), disabled: true }))
  ]
})

function openRoles(u: User) {
  rolesUser.value = u
  rolesSel.value = [...(u.roles || [])]
  loadRoles()
  void managed.load()
  rolesOpen.value = true
}

/** Checking supplier checks the default role with it, as when creating a user. */
function setRolesSel(v: ChoiceOption['value'][]) {
  const added = v.includes(SUPPLIER_ROLE) && !rolesSel.value.includes(SUPPLIER_ROLE)
  rolesSel.value = added ? withSupplierUser(v.map(String), managed.assignable(DEFAULT_ROLE)) : v
}

async function submitRoles() {
  if (!rolesUser.value) return
  saving.value = true
  try {
    await api.put(`/users/${rolesUser.value.id}/roles`, { role_keys: rolesSel.value.map(String) })
    toast(t('common.updated'), 'success')
    rolesOpen.value = false
    list.reload()
  } catch (e) {
    notifyError(e)
  } finally {
    saving.value = false
  }
}

// ------------------------------------------------------------------ groups and pricing

const pricingOpen = ref(false)
const pricingUser = ref<User | null>(null)

function openPricing(u: User) {
  pricingUser.value = u
  pricingOpen.value = true
}

// ------------------------------------------------------------------ balance

const balanceOpen = ref(false)
const balanceUser = ref<User | null>(null)

function openBalance(u: User) {
  balanceUser.value = u
  balanceOpen.value = true
}

// ------------------------------------------------------------------ password reset, super admin transfer

const passwordOpen = ref(false)
const passwordUser = ref<User | null>(null)
const transferOpen = ref(false)

function openPassword(u: User) {
  passwordUser.value = u
  passwordOpen.value = true
}

// ------------------------------------------------------------------ delete

function remove(u: User) {
  return deleteWithConfirm(`/users/${u.id}`, t('common.confirmDelete', { name: u.email }), list.reload)
}
</script>

<template>
  <div>
    <SPageHeader :title="t('users.title')" :description="t('users.description')" :filters-active-count="activeFilterCount(list.filters)">
      <template #actions>
        <SButton v-if="auth.hasAction(P.UserCreate)" variant="primary" icon="plus" @click="openCreate">{{ t('users.create') }}</SButton>
      </template>
      <template #filters-primary>
        <SInput v-model="list.filters.q" class="w-64" icon="search" clearable :aria-label="t('common.search')" :placeholder="t('users.searchPlaceholder')" />
      </template>
      <template #filters>
        <SSelect v-model="list.filters.status" class="w-40" :options="statusFilterOptions" :aria-label="t('common.status')" />
        <SSelect v-if="roles.length" v-model="list.filters.role" class="w-44" :options="roleOptions" :aria-label="t('users.roles')" />
        <SSelect v-model="list.filters.segment" class="w-44" :options="segmentOptions" :aria-label="t('users.segment')" data-testid="user-segment-filter" />
        <EntityPicker v-model:id="list.filters.owner_id" kind="user" class="w-60" :placeholder="t('users.owner')" data-testid="user-owner-filter" />
        <SSwitch v-if="teamOnly" v-model="onlyDirect" :label="t('users.onlyDirect')" data-testid="user-direct-filter" />
      </template>
    </SPageHeader>

    <SBulkBar
      class="mb-3"
      :count="selection.keys.value.length"
      :total="list.total.value"
      :page-selected="selection.pageSelected.value"
      :all-selected="selection.allSelected.value"
      :selecting-all="selection.selectingAll.value"
      data-testid="user-bulk-bar"
      @select-page="selection.selectPage()"
      @select-all="selection.selectAll().catch(notifyError)"
      @clear="selection.clear()"
    >
      <SButton size="sm" variant="primary" data-testid="user-bulk-move" @click="moveOpen = true">{{ t('users.move.action') }}</SButton>
    </SBulkBar>

    <STable v-model:selected="selectionKeys" card :selectable="canManageSegments" :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" @retry="list.reload">
      <template #cell-email="{ row }">
        <span class="font-medium text-fg">{{ row.email }}</span>
        <SBadge v-if="isSuper(row)" tone="accent" size="xs" class="ms-2" data-testid="super-admin-badge">{{ t('users.superAdminBadge') }}</SBadge>
      </template>
      <template #cell-roles="{ row }">
        <div class="flex flex-wrap gap-1">
          <SBadge v-for="r in row.roles || []" :key="r" :tone="r === 'super_admin' ? 'accent' : 'primary'" :title="roleScopes(r)">{{ roleName(r) }}</SBadge>
          <SHint v-if="!row.roles || !row.roles.length" inline>—</SHint>
        </div>
      </template>
      <template #cell-segment="{ row }">
        <SegmentBadge :segment="row.segment" />
      </template>
      <template #cell-owner="{ row }"><UserRef :id="row.owner_id" :email="row.owner_email" /></template>
      <template #cell-balance="{ row }">
        <span class="font-mono">{{ formatMoney(row.balance) }}</span>
      </template>
      <template #cell-status="{ row }">
        <SStatusBadge :status="row.status" />
      </template>
      <template #cell-last_login_at="{ row }">
        <SDateTime mode="absolute" muted :value="row.last_login_at" :empty="t('common.never')" />
      </template>
      <template #cell-created_at="{ row }">
        <SDateTime mode="absolute" muted :value="row.created_at" />
      </template>
      <template #cell-actions="{ row }">
        <SDropdown v-if="hasAnyAction(row)" :actions="rowActions(row)" @select="onAction(row, $event)" />
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <!-- create -->
    <UserCreateDialog v-model:open="createOpen" :roles="roles" @created="list.reload()" />

    <!-- edit -->
    <SFormModal v-model:open="editOpen" :title="t('users.editTitle', { name: editUser?.email || '' })" :loading="saving" @submit="submitEdit">
        <SField :label="t('users.displayName')" :error="editErrors.display_name">
          <SInput v-model="editForm.display_name" />
        </SField>
        <SField :label="t('common.status')" :error="editErrors.status">
          <SSelect v-model="editForm.status" :options="statusOptions" />
        </SField>
        <SField v-if="canSetOwner" :label="t('users.owner')" :hint="t('users.ownerHint')" :error="editErrors.owner_id">
          <EntityPicker v-model:id="editForm.owner_id" kind="user" :exclude="editUser ? [editUser.id] : []" data-testid="user-edit-owner" />
        </SField>
    </SFormModal>

    <!-- roles -->
    <SFormModal v-model:open="rolesOpen" :title="t('users.assignRolesTitle', { name: rolesUser?.email || '' })" :loading="saving" :enter-submit="false" @submit="submitRoles">
      <SChoiceCards v-if="roleChoices.length" :model-value="rolesSel" multiple :cols="1" :options="roleChoices" :aria-label="t('users.roles')" data-testid="user-roles-choices" @update:model-value="setRolesSel($event as ChoiceOption['value'][])">
        <template v-for="o in roleChoices" :key="o.value" #[`option-${o.value}`]>
          <span class="mt-1 flex items-center gap-2">
            <SHint inline size="xs" class="font-mono">{{ o.value }}</SHint>
            <SBadge v-if="roles.find((r) => r.key === o.value)?.superuser" size="xs" tone="accent">{{ t('users.superuser') }}</SBadge>
          </span>
        </template>
      </SChoiceCards>
      <SHint v-else>{{ t('users.noManagedRoles') }}</SHint>
    </SFormModal>

    <!-- move to a user group -->
    <UserSegmentMoveDialog v-if="canManageSegments" v-model:open="moveOpen" :users="selection.rows.value" @done="onMoved" />

    <!-- groups and pricing -->
    <UserPricingDrawer v-model:open="pricingOpen" :user="pricingUser" @changed="list.reload()" />

    <!-- password reset, super admin transfer -->
    <ResetPasswordDialog v-model:open="passwordOpen" :user="passwordUser" />
    <TransferSuperAdminDialog v-model:open="transferOpen" />

    <!-- balance -->
    <AdjustBalanceModal v-model:open="balanceOpen" :user-id="balanceUser?.id ?? null" :current-balance="balanceUser?.balance" @done="list.reload()" />
  </div>
</template>
