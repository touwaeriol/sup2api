<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { SAlert, SBadge, SButton, SCard, SChip, SDropdown, SEmpty, SField, SGrid, SHint, SIcon, SInput, SLink, SFormModal, SPageHeader, SRadioGroup, SSelect, SSpinner, STextarea, confirm, toast, type SelectOption } from '@sub2api/ui'
import type { PermissionModule, User } from '@/api/types'
import { roleMemberCount, type RoleRow } from '@/api/admin'
import { deleteWithConfirm } from '@/composables/crud'
import { fetchFirstPage } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { useAssignableRoles } from '@/composables/useAssignableRoles'
import { inAllLocales, lt } from '@/i18n'
import { fieldErrors, notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { isVersionConflict, missingText } from '@/utils/permErrors'
import { actionOf, newRiskyGrants, replayCells, sameKeys } from '@/authz/pluginGrants'
import { copyGrants, type CopyMode, type CopyRules } from '@/authz/copyGrants'
import { useEditorGrants } from '@/components/permission/useEditorGrants'
import { confirmRiskyGrants } from '@/components/permission/confirmRiskyGrants'
import PermissionTree from './PermissionTree.vue'
import RoleAssignable from './RoleAssignable.vue'
import { ROLE_KEY_PATTERN } from '@/generated/permissions'

const { t } = useI18n()
const auth = useAuthStore()
const managed = useAssignableRoles()
const canDefine = computed(() => auth.has('role:define'))

const roles = ref<RoleRow[]>([])
const modules = ref<PermissionModule[]>([])
const loading = ref(true)
const selectedId = ref<number | null>(null)
const selected = computed(() => roles.value.find((r) => r.id === selectedId.value) || null)
/** Every permission of the catalog by key. */
const catalog = computed(() => new Map(modules.value.flatMap((m) => m.permissions.map((p) => [p.key, p]))))
/** What the editor may grant: its keys in the grant view (a disabled plugin's included, a removed permission's not). */
const editor = useEditorGrants(() => roles.value, (action) => catalog.value.get(action)?.status === 'removed')

// ------------------------------------------------------------------ loading

async function loadRoles() {
  roles.value = await fetchFirstPage<RoleRow>('/roles')
}

async function init() {
  loading.value = true
  try {
    const [, perms] = await Promise.all([loadRoles(), api.get<PermissionModule[]>('/permissions')])
    modules.value = perms || []
    if (roles.value.length) selectRole(roles.value[0], true)
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}
init()

// ------------------------------------------------------------------ editor state

// The name and description are entered once, in the console's language, and
// saved the same in every locale (inAllLocales).
const form = reactive({ name: '', description: '' })
const perms = ref(new Set<string>())
const assignable = ref(new Set<string>())
const assignCustom = ref(false)
const original = ref({ name: '', description: '', perms: [] as string[], assignable: [] as string[], assignCustom: false })
const saving = ref(false)
/** What the last save lacked (403 details.missing / missing_assignable). */
const saveMissing = ref('')
/** Field errors of the last save (PATCH /roles/:id invalid_argument). */
const editErrors = ref<Record<string, string>>({})

/**
 * A built-in role is changed by whoever manages it: on the caller's assignable list and within the
 * caller (CONTRACTS §43.6; the server decides); the super admin role by nobody.
 */
const builtinLocked = computed(
  () => !!selected.value?.builtin && !selected.value.superuser && !auth.me?.superuser && !managed.assignable(selected.value.key)
)
const readOnly = computed(() => !canDefine.value || !!selected.value?.superuser || builtinLocked.value)
const permsMissing = computed(() => !!selected.value && !selected.value.superuser && !Array.isArray(selected.value.permission_keys))

// Compared trimmed, as saved: stray spaces alone do not rewrite a text in every locale.
const nameDirty = computed(() => form.name.trim() !== original.value.name)
const descriptionDirty = computed(() => form.description.trim() !== original.value.description)
const metaDirty = computed(() => nameDirty.value || descriptionDirty.value)
const permsDirty = computed(() => [...perms.value].sort().join() !== original.value.perms.join())
const assignableDirty = computed(
  () => [...assignable.value].sort().join() !== original.value.assignable.join() || assignCustom.value !== original.value.assignCustom
)
const dirty = computed(() => metaDirty.value || permsDirty.value || assignableDirty.value)

async function selectRole(r: RoleRow, force = false) {
  if (!force && r.id === selectedId.value) return
  if (!force && dirty.value && !readOnly.value && !(await confirm({ message: t('roles.discardChanges') }))) return
  selectedId.value = r.id
  const parts = partsOf(r)
  Object.assign(form, { name: parts.name, description: parts.description })
  perms.value = new Set(parts.perms)
  assignable.value = new Set(parts.assignable)
  assignCustom.value = parts.assignCustom
  saveMissing.value = ''
  copyNote.value = []
  editErrors.value = {}
  original.value = parts
  loadMembers(r)
}

// ------------------------------------------------------------------ members (GET /users?role=<key>)

const MEMBER_PREVIEW = 20
const members = ref<User[]>([])
const membersTotal = ref(0)
const membersLoading = ref(false)
let membersSeq = 0

async function loadMembers(r: RoleRow) {
  members.value = []
  membersTotal.value = 0
  if (!auth.has('user:read')) return
  const my = ++membersSeq
  membersLoading.value = true
  try {
    const res = await api.list<User>('/users', { role: r.key, page_size: MEMBER_PREVIEW })
    if (my !== membersSeq) return
    members.value = res.items
    membersTotal.value = res.page.total ?? res.items.length
  } catch {
    /* members are informational only */
  } finally {
    if (my === membersSeq) membersLoading.value = false
  }
}

// ------------------------------------------------------------------ save

function save() {
  return persist(false)
}

/** Saves the changed parts; resent: the save again after a 409 where nobody else changed what it saves (asked about already). */
async function persist(resent: boolean): Promise<void> {
  const r = selected.value
  if (!r || readOnly.value) return
  editErrors.value = {}
  if (nameDirty.value && !form.name.trim()) {
    editErrors.value.name = t('common.required')
    return
  }
  if (!resent && permsDirty.value && permsMissing.value && !(await confirm({ message: t('roles.overwriteUnknown'), danger: true }))) return
  if (!resent && permsDirty.value && !(await confirmRisky(r))) return
  // Each changed part has its own endpoint; they run in order and stop at the
  // first refusal. Each carries the role's revision (C1): the one read for
  // the first, then the one the previous step answered with.
  let revision = r.revision
  const rev = () => (revision !== undefined ? { revision } : {})
  const steps = [
    permsDirty.value && (() => api.put<RoleRow>(`/roles/${r.id}/permissions`, { permission_keys: [...perms.value].sort(), ...rev() })),
    metaDirty.value && (() => api.patch<RoleRow>(`/roles/${r.id}`, { ...metaPatch(), ...rev() })),
    assignableDirty.value && (() => api.put<RoleRow>(`/roles/${r.id}/assignable`, { role_keys: [...assignable.value].sort(), assign_custom: assignCustom.value, ...rev() }))
  ].filter((step) => !!step)
  saving.value = true
  saveMissing.value = ''
  let done = 0
  let missing = ''
  try {
    for (const step of steps) {
      revision = (await step())?.revision ?? revision
      done++
    }
    toast(t('common.saved'), 'success')
  } catch (e) {
    if (isVersionConflict(e)) {
      saving.value = false
      return rebase(r, resent).catch(notifyError)
    }
    missing = missingText(e, t) || ownerLostText(e)
    showSaveError(e)
  }
  // Something was written (all of it, or the parts before a refusal): show what the role now is.
  if (done) await showSaved(r, done === steps.length).catch(notifyError)
  saveMissing.value = missing
  saving.value = false
}

/** The sensitive and critical permissions this save grants the role, confirmed first. */
function confirmRisky(r: RoleRow) {
  const all = [...catalog.value.values()].map((p) => ({ action: p.key, risk: p.risk }))
  const risky = newRiskyGrants({ [r.key]: original.value.perms }, { [r.key]: [...perms.value] }, all)
  return confirmRiskyGrants(risky, (action) => catalog.value.get(action)?.label, () => lt(r.name) || r.key)
}

/**
 * A 409 version_conflict: reads the roles again (a failed read keeps the
 * edits for another try) and takes in each part of the role (permissions,
 * name, description, managed roles): one the editor left as it was takes
 * what is read; one the editor changed stays as edited, on top of what is
 * read when someone else changed it too (the permissions cell by cell,
 * replayCells; the managed roles role by role). When nobody else changed a
 * part the editor changed (the role changed otherwise, or this save's
 * earlier steps went through) it is saved again at once; else it waits for
 * another save, naming what both changed.
 */
async function rebase(r: RoleRow, resent: boolean): Promise<void> {
  const base = original.value
  const edited = { perms: [...perms.value].sort(), assignable: [...assignable.value].sort(), assignCustom: assignCustom.value }
  await loadRoles()
  const fresh = roles.value.find((x) => x.id === r.id)
  if (!fresh) return
  const read = partsOf(fresh)
  const clashes: string[] = []
  let moved = false // a part the editor changed, someone else changed too
  for (const [field, label] of [['name', t('common.name')], ['description', t('common.description')]] as const) {
    const mine = form[field].trim()
    if (mine === base[field]) form[field] = read[field]
    else if (read[field] !== base[field] && read[field] !== mine) {
      moved = true
      clashes.push(label)
    }
  }
  if (sameKeys(edited.perms, base.perms)) perms.value = new Set(read.perms)
  else if (!sameKeys(read.perms, base.perms) && !sameKeys(read.perms, edited.perms)) {
    moved = true
    const actions = [...new Set([...base.perms, ...edited.perms, ...read.perms].map(actionOf))]
    const merged = replayCells({ [r.key]: base.perms }, { [r.key]: edited.perms }, { [r.key]: read.perms }, actions, [r.key])
    perms.value = new Set(merged.value[r.key])
    clashes.push(...merged.clashes.map((c) => lt(catalog.value.get(c.action)?.label) || c.action))
  }
  const sameList = (a: Managed, b: Managed) => a.assignable.join() === b.assignable.join() && a.assignCustom === b.assignCustom
  if (sameList(edited, base)) {
    assignable.value = new Set(read.assignable)
    assignCustom.value = read.assignCustom
  } else if (!sameList(read, base) && !sameList(read, edited)) {
    moved = true
    // Role by role: each the editor added or took away, on what is read.
    const next = new Set(read.assignable)
    base.assignable.filter((k) => !edited.assignable.includes(k)).forEach((k) => next.delete(k))
    edited.assignable.filter((k) => !base.assignable.includes(k)).forEach((k) => next.add(k))
    assignable.value = next
    assignCustom.value = edited.assignCustom !== base.assignCustom ? edited.assignCustom : read.assignCustom
  }
  original.value = read
  if (!moved && !resent) return persist(true)
  const list = clashes.join(t('roles.listSep'))
  toast(list ? t('roles.roleConflictClashes', { list }) : t('roles.roleConflictMerged'), 'info')
}

/** The managed roles of a role, sorted, and its "every custom role" flag. */
interface Managed {
  assignable: string[]
  assignCustom: boolean
}

/** The parts of r the editor edits, as original holds them. */
function partsOf(r: RoleRow) {
  return {
    name: lt(r.name),
    description: lt(r.description),
    perms: [...(r.permission_keys ?? [])].sort(),
    assignable: [...(r.assignable_role_keys ?? [])].sort(),
    assignCustom: !!r.assign_custom
  }
}

/**
 * The PATCH /roles/:id body: only the changed texts, so an untouched built-in
 * name keeps its default in every locale.
 */
function metaPatch() {
  return {
    ...(nameDirty.value && { name: inAllLocales(form.name.trim()) }),
    ...(descriptionDirty.value && { description: inAllLocales(form.description.trim()) })
  }
}

async function showSaved(r: RoleRow, complete: boolean) {
  await loadRoles()
  const fresh = roles.value.find((x) => x.id === r.id)
  if (fresh) {
    // Keep the saved permissions if the list endpoint omits permission_keys.
    if (!Array.isArray(fresh.permission_keys) && complete) fresh.permission_keys = [...perms.value]
    selectRole(fresh, true)
  }
  if (auth.me?.roles.includes(r.key)) auth.refreshPermissions().catch(() => undefined)
}

/** Editor fields that show their own errors (PATCH /roles/:id runs first); any other refusal is a toast. */
const EDITOR_FIELDS = ['key', 'name']

function showSaveError(e: unknown) {
  const fields = Object.entries(fieldErrors(e, undefined, ROLE_FIELD_CODES)).filter(([k]) => EDITOR_FIELDS.includes(k))
  editErrors.value = Object.fromEntries(fields)
  if (!fields.length && !ownerLostText(e)) notifyError(e)
}

/** A change some holder's owner could no longer manage (owner_cannot_manage, CONTRACTS §103.3), shown on the page like a missing grant; '' for any other error. */
function ownerLostText(e: unknown): string {
  if (!isApiError(e) || !Object.values(e.fieldCodes).includes('owner_cannot_manage')) return ''
  return t('roles.ownerCannotManage', { id: String(e.details.user_id ?? '') })
}

function resetEditor() {
  if (selected.value) selectRole(selected.value, true)
}

// ------------------------------------------------------------------ copy from another role

// Fills the editor's ticks from another role's saved keys (merge or overwrite,
// authz/copyGrants); nothing is written until Save, which checks the result
// like any edit. What a copy leaves out (beyond the editor, critical,
// removed) is named in a note that stays until the editor reloads.
const copyOpen = ref(false)
const copyFrom = ref<number | null>(null)
const copyMode = ref<CopyMode>('merge')
const copySources = computed(() =>
  roles.value.filter((r) => r.id !== selectedId.value && !r.superuser && Array.isArray(r.permission_keys))
)
const copySourceOptions = computed<SelectOption[]>(() => copySources.value.map((r) => ({ value: r.id, label: lt(r.name) || r.key, hint: r.key })))
const copyModeOptions = computed<SelectOption[]>(() => [
  { value: 'merge', label: t('roles.copyMerge') },
  { value: 'overwrite', label: t('roles.copyOverwrite') }
])
const copyRules: CopyRules = {
  mayGrant: (key) => editor.mayGrant(key),
  gone: (action) => (catalog.value.get(action)?.status ?? 'removed') === 'removed',
  critical: (action) => catalog.value.get(action)?.risk === 'critical'
}
const copyNote = ref<string[]>([])

function openCopy() {
  copyFrom.value = null
  copyMode.value = 'merge'
  copyOpen.value = true
}

function applyCopy() {
  const source = copySources.value.find((r) => r.id === copyFrom.value)
  if (!source) return
  const res = copyGrants([...perms.value], source.permission_keys ?? [], copyMode.value, copyRules)
  const changed = !sameKeys([...perms.value], res.keys)
  perms.value = new Set(res.keys)
  copyOpen.value = false
  const left = (key: string, list: string[]) => (list.length ? [t(key, { n: list.length, list: list.join(t('roles.listSep')) }, list.length)] : [])
  copyNote.value = [...left('roles.copyLeftNotGrantable', res.notGrantable), ...left('roles.copyLeftCritical', res.critical), ...left('roles.copyLeftGone', res.gone)]
  toast(changed ? t('roles.copied') : t('roles.copyNoChange'), changed ? 'success' : 'info')
}

// ------------------------------------------------------------------ create / delete

/** Field error codes of POST and PATCH /roles shown in the console's language. */
const ROLE_FIELD_CODES = { reserved: 'roles.keyReserved', immutable: 'roles.keyImmutable' }

const createOpen = ref(false)
const creating = ref(false)
const createErrors = ref<Record<string, string>>({})
const createForm = reactive({ key: '', name: '', description: '' })

function openCreate() {
  Object.assign(createForm, { key: '', name: '', description: '' })
  createErrors.value = {}
  createOpen.value = true
}

async function submitCreate() {
  createErrors.value = {}
  const key = createForm.key.trim()
  // Same rule as roleKeyRe in server/internal/authz/roles.go.
  if (!ROLE_KEY_PATTERN.test(key)) createErrors.value.key = t('roles.keyInvalid')
  const name = createForm.name.trim()
  if (!name) createErrors.value.name = t('common.required')
  if (Object.keys(createErrors.value).length) return
  creating.value = true
  try {
    const created = await api.post<RoleRow>('/roles', { key, name: inAllLocales(name), description: inAllLocales(createForm.description.trim()) })
    toast(t('common.created'), 'success')
    createOpen.value = false
    await loadRoles()
    const r = roles.value.find((x) => (created?.id ? x.id === created.id : x.key === key))
    if (r) {
      if (!Array.isArray(r.permission_keys)) r.permission_keys = []
      selectRole(r, true)
    }
  } catch (e) {
    applyFormError(e, createErrors, ROLE_FIELD_CODES)
  } finally {
    creating.value = false
  }
}

function removeRole() {
  const r = selected.value
  if (!r || r.builtin) return
  return deleteWithConfirm(`/roles/${r.id}`, t('common.confirmDelete', { name: lt(r.name) || r.key }), async () => {
    await loadRoles()
    selectedId.value = null
    if (roles.value.length) selectRole(roles.value[0], true)
  })
}

const roleActions = computed(() => [{ key: 'delete', label: t('common.delete'), danger: true }])

// POST /roles/:id/reset: name, description, core permissions and managed
// roles back to the built-in defaults; plugin permissions stay.
async function restoreDefaults() {
  const r = selected.value
  if (!r?.builtin || r.superuser) return
  if (!(await confirm({ message: t('roles.restoreConfirm', { name: lt(r.name) || r.key }), danger: true }))) return
  saving.value = true
  try {
    // With the revision read, when there is one (C1).
    await (r.revision !== undefined ? api.post(`/roles/${r.id}/reset`, { revision: r.revision }) : api.post(`/roles/${r.id}/reset`))
    toast(t('roles.restored'), 'success')
    await loadRoles()
    const fresh = roles.value.find((x) => x.id === r.id)
    if (fresh) selectRole(fresh, true)
    if (auth.me?.roles.includes(r.key)) auth.refreshPermissions().catch(() => undefined)
  } catch (e) {
    notifyError(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div>
    <SPageHeader :title="t('roles.title')" :description="t('roles.description')" />

    <SSpinner v-if="loading" block />

    <SGrid v-else :cols="1" lg-template="260px 1fr">
      <!-- role list -->
      <SCard :padded="false" class="self-start">
        <div class="p-2">
          <SEmpty v-if="!roles.length" />
          <button
            v-for="r in roles"
            :key="r.id"
            type="button"
            class="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm transition-colors"
            :class="r.id === selectedId ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-fg hover:bg-surface-2'"
            @click="selectRole(r)"
          >
            <span class="min-w-0 flex-1">
              <span class="block truncate">{{ lt(r.name) || r.key }}</span>
              <SHint inline size="xs" class="block truncate font-mono">{{ r.key }}</SHint>
            </span>
            <span v-if="r.superuser" class="shrink-0 text-accent-500 dark:text-accent-400" :title="t('roles.superuserHint')" :aria-label="t('roles.superuser')" role="img"><SIcon name="lock" class="h-4 w-4" /></span>
            <SBadge v-else-if="r.builtin" tone="gray">{{ t('roles.builtin') }}</SBadge>
            <SHint v-if="roleMemberCount(r) !== undefined" inline size="xs" class="shrink-0">{{ roleMemberCount(r) }}</SHint>
          </button>
        </div>
        <template v-if="canDefine" #footer>
          <SButton block icon="plus" data-testid="role-create" @click="openCreate">{{ t('roles.create') }}</SButton>
        </template>
      </SCard>

      <!-- editor -->
      <SCard v-if="selected">
        <template #title>
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <h3 class="truncate text-base font-semibold text-fg">{{ lt(selected.name) || selected.key }}</h3>
            <SHint inline size="xs" class="font-mono">{{ selected.key }}</SHint>
            <SBadge v-if="selected.superuser" tone="accent"><SIcon name="lock" class="h-3 w-3" />{{ t('roles.superuser') }}</SBadge>
            <SBadge v-else-if="selected.builtin" tone="gray">{{ t('roles.builtin') }}</SBadge>
            <SHint v-if="roleMemberCount(selected) !== undefined" inline size="xs">{{ t('roles.members', { n: roleMemberCount(selected) }) }}</SHint>
          </div>
        </template>
        <template v-if="!readOnly" #actions>
          <SButton v-if="selected.builtin" size="sm" icon="refresh" :disabled="saving" data-testid="role-restore" @click="restoreDefaults">{{ t('roles.restoreDefaults') }}</SButton>
          <SButton v-if="!permsMissing" size="sm" icon="copy" :disabled="saving || !copySources.length" data-testid="role-copy" @click="openCopy">{{ t('roles.copyFrom') }}</SButton>
          <SButton size="sm" :disabled="!dirty || saving" @click="resetEditor">{{ t('common.reset') }}</SButton>
          <SButton variant="primary" size="sm" :disabled="!dirty" :loading="saving" data-testid="role-save" @click="save">{{ t('common.save') }}</SButton>
          <SDropdown v-if="!selected.builtin" :actions="roleActions" data-testid="role-menu" @select="removeRole" />
        </template>

        <div class="space-y-5">
          <SAlert v-if="selected.superuser" tone="accent" icon="lock">{{ t('roles.superuserHint') }}</SAlert>
          <SAlert v-else-if="builtinLocked && canDefine" tone="info" icon="lock" data-testid="role-builtin-locked">{{ t('roles.builtinLocked') }}</SAlert>
          <SAlert v-else-if="permsMissing" tone="warning">{{ t('roles.permsNotLoaded') }}</SAlert>
          <SAlert v-if="saveMissing" tone="danger" data-testid="role-save-missing">{{ saveMissing }}</SAlert>
          <SAlert v-if="copyNote.length" tone="info" data-testid="role-copy-note"><p v-for="line in copyNote" :key="line">{{ line }}</p></SAlert>

          <SGrid :cols="1" :md-cols="3">
            <SField :label="t('roles.key')" :hint="t('roles.keyLocked')" :error="editErrors.key">
              <SInput :model-value="selected.key" mono disabled data-testid="role-key" />
            </SField>
            <SField :label="t('common.name')" :error="editErrors.name">
              <SInput v-model="form.name" :disabled="readOnly" data-testid="role-name" />
            </SField>
            <SField :label="t('common.description')">
              <SInput v-model="form.description" :disabled="readOnly" data-testid="role-description" />
            </SField>
          </SGrid>

          <PermissionTree
            v-model:granted="perms"
            :modules="modules"
            :all-granted="selected.superuser"
            :read-only="readOnly"
            :scope-limit="editor.scopes"
            :role-key="selected.key"
            :grantable="editor.mayGrant"
          />

          <RoleAssignable v-if="!selected.superuser && Array.isArray(selected.assignable_role_keys)" v-model:keys="assignable" v-model:assign-custom="assignCustom" :roles="roles" :read-only="readOnly" />

          <div v-if="auth.has('user:read')" class="flex flex-wrap items-center gap-2 border-t border-line pt-4 text-sm">
            <SHint inline class="shrink-0">{{ t('roles.membersLabel') }}:</SHint>
            <SSpinner v-if="membersLoading" size="sm" />
            <template v-else-if="members.length">
              <SChip v-for="u in members" :key="u.id" size="xs" :title="u.email">{{ u.display_name || u.email }}</SChip>
              <SHint v-if="membersTotal > members.length" inline size="xs">{{ t('roles.moreMembers', { n: membersTotal - members.length }) }}</SHint>
            </template>
            <SHint v-else inline>{{ t('roles.noMembers') }}</SHint>
            <SLink :to="{ path: '/users', query: { role: selected.key } }" class="ml-auto text-xs">{{ t('roles.manageMembers') }}</SLink>
          </div>
        </div>
      </SCard>
      <SCard v-else :padded="false"><SEmpty :text="t('roles.selectRole')" /></SCard>
    </SGrid>

    <SFormModal
      v-model:open="copyOpen"
      :title="t('roles.copyTitle')"
      :submit-text="t('roles.copyApply')"
      :submit-disabled="copyFrom === null"
      :submit-attrs="{ 'data-testid': 'role-copy-apply' }"
      data-testid="role-copy-modal"
      @submit="applyCopy"
    >
      <SField :label="t('roles.copySource')" required>
        <SSelect v-model="copyFrom" :options="copySourceOptions" searchable :placeholder="t('roles.copySourcePlaceholder')" :aria-label="t('roles.copySource')" data-testid="role-copy-source" />
      </SField>
      <SField :label="t('roles.copyMode')" :hint="copyMode === 'merge' ? t('roles.copyMergeHint') : t('roles.copyOverwriteHint')">
        <SRadioGroup v-model="copyMode" :options="copyModeOptions" inline :aria-label="t('roles.copyMode')" data-testid="role-copy-mode" />
      </SField>
      <SHint>{{ t('roles.copyNotSaved') }}</SHint>
    </SFormModal>

    <SFormModal v-model:open="createOpen" :title="t('roles.create')" :loading="creating" :submit-text="t('common.create')" @submit="submitCreate">
      <SField :label="t('roles.key')" required :hint="t('roles.keyHint')" :error="createErrors.key">
        <SInput v-model="createForm.key" mono placeholder="auditor" data-testid="role-create-key" />
      </SField>
      <SField :label="t('common.name')" required :error="createErrors.name">
        <SInput v-model="createForm.name" :placeholder="t('roles.namePlaceholder')" data-testid="role-create-name" />
      </SField>
      <SField :label="t('common.description')" :error="createErrors.description">
        <STextarea v-model="createForm.description" :rows="2" />
      </SField>
    </SFormModal>
  </div>
</template>
