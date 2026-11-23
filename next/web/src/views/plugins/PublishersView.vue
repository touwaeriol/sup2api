<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import {
  SBadge,
  SButton,
  SCode,
  SCopyButton,
  SDropdown,
  SField,
  SGrid,
  SHint,
  SIcon,
  SInput,
  SFormModal,
  SPageHeader,
  SPagination,
  SSelect,
  SStatusBadge,
  STable,
  STextarea,
  toast,
  type MenuAction,
  type TableColumn,
  type Tone
} from '@sub2api/ui'
import type { Publisher } from '@/api/types'
import { fromLocalInput } from '@/api/admin'
import { confirmAction } from '@/composables/crud'
import { applyFormError } from '@/utils/formErrors'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/format'

type PublisherKey = NonNullable<Publisher['keys']>[number]

const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('publisher:manage'))
const list = useList<Publisher>('/publishers')

const TRUST_LEVELS = ['official', 'verified', 'community'] as const

function trustTone(level: string): Tone {
  return level === 'official' ? 'purple' : level === 'verified' ? 'primary' : 'gray'
}

function trustLabel(level: string) {
  const k = `publishers.trust_.${level}`
  const v = t(k)
  return v === k ? level : v
}

function keyState(k: PublisherKey): string {
  if (k.status && k.status !== 'active') return k.status
  const now = Date.now()
  if (k.not_after && new Date(k.not_after).getTime() < now) return 'expired'
  if (k.not_before && new Date(k.not_before).getTime() > now) return 'pending'
  return k.status || 'active'
}

const keyColumns = computed<TableColumn[]>(() => {
  const cols: TableColumn[] = [
    { key: 'key_id', label: t('publishers.keyId') },
    { key: 'public_key', label: t('publishers.publicKey') },
    { key: 'status', label: t('common.status') },
    { key: 'not_before', label: t('publishers.notBefore') },
    { key: 'not_after', label: t('publishers.notAfter') },
    { key: 'created_at', label: t('common.createdAt') }
  ]
  if (canManage.value) cols.push({ key: 'actions', label: t('common.actions'), align: 'right' })
  return cols
})

const columns = computed<TableColumn[]>(() => {
  const cols: TableColumn[] = [
    { key: 'id', label: t('common.id'), width: '64px' },
    { key: 'name', label: t('common.name'), mobile: 'title' },
    { key: 'trust_level', label: t('publishers.trustLevel'), mobile: 'meta' },
    { key: 'status', label: t('common.status'), mobile: 'status' },
    { key: 'keys', label: t('publishers.keys'), align: 'right', mobile: 'meta' },
    { key: 'created_at', label: t('common.createdAt'), mobile: 'meta' }
  ]
  if (canManage.value) cols.push({ key: 'actions', label: t('common.actions'), align: 'right' })
  return cols
})

function activeKeys(p: Publisher) {
  return (p.keys || []).filter((k) => keyState(k) === 'active').length
}

function rowActions(p: Publisher): MenuAction[] {
  const revoked = p.status === 'revoked'
  return [
    { key: 'add-key', label: t('publishers.addKey'), disabled: revoked },
    { key: 'revoke', label: t('publishers.revoke'), danger: true, hidden: revoked }
  ]
}

async function onAction(p: Publisher, action: string) {
  if (action === 'add-key') openAddKey(p)
  else if (action === 'revoke') revokePublisher(p)
}

// ------------------------------------------------------------------ create publisher

const createOpen = ref(false)
const creating = ref(false)
const createErrors = ref<Record<string, string>>({})
const createForm = reactive({ name: '', trust_level: 'community' as string })
const trustOptions = computed(() => TRUST_LEVELS.map((l) => ({ value: l, label: trustLabel(l) })))

function openCreate() {
  Object.assign(createForm, { name: '', trust_level: 'community' })
  createErrors.value = {}
  createOpen.value = true
}

async function submitCreate() {
  createErrors.value = {}
  if (!createForm.name.trim()) {
    createErrors.value.name = t('common.required')
    return
  }
  creating.value = true
  try {
    await api.post('/publishers', { name: createForm.name.trim(), trust_level: createForm.trust_level })
    toast(t('common.created'), 'success')
    createOpen.value = false
    list.reload()
  } catch (e) {
    applyFormError(e, createErrors)
  } finally {
    creating.value = false
  }
}

// ------------------------------------------------------------------ add key

const keyOpen = ref(false)
const keyPublisher = ref<Publisher | null>(null)
const keyErrors = ref<Record<string, string>>({})
const keyForm = reactive({ key_id: '', public_key: '', not_before: '', not_after: '' })

function openAddKey(p: Publisher) {
  keyPublisher.value = p
  Object.assign(keyForm, { key_id: '', public_key: '', not_before: '', not_after: '' })
  keyErrors.value = {}
  keyOpen.value = true
}

/** An ed25519 public key is 32 bytes; accept standard or URL-safe base64. */
function validEd25519(b64: string): boolean {
  const s = b64.trim().replace(/-/g, '+').replace(/_/g, '/')
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(s)) return false
  try {
    const padded = s + '='.repeat((4 - (s.length % 4)) % 4)
    return atob(padded).length === 32
  } catch {
    return false
  }
}

async function submitKey() {
  const p = keyPublisher.value
  if (!p) return
  keyErrors.value = {}
  if (!keyForm.key_id.trim()) keyErrors.value.key_id = t('common.required')
  if (!validEd25519(keyForm.public_key)) keyErrors.value.public_key = t('publishers.publicKeyInvalid')
  const nb = fromLocalInput(keyForm.not_before)
  const na = fromLocalInput(keyForm.not_after)
  if (nb && na && new Date(na) <= new Date(nb)) keyErrors.value.not_after = t('publishers.rangeInvalid')
  if (Object.keys(keyErrors.value).length) return
  creating.value = true
  try {
    const body: Record<string, unknown> = { key_id: keyForm.key_id.trim(), public_key: keyForm.public_key.trim() }
    if (nb) body.not_before = nb
    if (na) body.not_after = na
    await api.post(`/publishers/${p.id}/keys`, body)
    toast(t('publishers.keyAdded'), 'success')
    keyOpen.value = false
    list.reload()
  } catch (e) {
    applyFormError(e, keyErrors)
  } finally {
    creating.value = false
  }
}

// ------------------------------------------------------------------ revoke

async function revokePublisher(p: Publisher) {
  const ok = await confirmAction(
    { title: t('publishers.revoke'), message: t('publishers.confirmRevoke', { name: p.name }), danger: true },
    () => api.post(`/publishers/${p.id}/revoke`),
    () => {
      toast(t('publishers.revoked'), 'success')
      list.reload()
    }
  )
  if (!ok) return
}

async function revokeKey(p: Publisher, k: PublisherKey) {
  await confirmAction(
    {
      title: t('publishers.revokeKey'),
      message: t('publishers.confirmRevokeKey', { key: k.key_id, name: p.name }),
      danger: true
    },
    () => api.post(`/publisher-keys/${encodeURIComponent(k.key_id)}/revoke`),
    () => {
      toast(t('publishers.revoked'), 'success')
      list.reload()
    }
  )
}
</script>

<template>
  <div>
    <SPageHeader :title="t('publishers.title')" :description="t('publishers.description')">
      <template #actions>
        <SButton v-if="canManage" variant="primary" icon="plus" @click="openCreate">{{ t('publishers.create') }}</SButton>
      </template>
    </SPageHeader>

    <STable :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" expandable @retry="list.reload">
      <template #cell-name="{ row }">
        <span class="font-medium text-gray-900 dark:text-white">{{ row.name }}</span>
      </template>
      <template #cell-trust_level="{ row }">
        <SBadge :tone="trustTone(row.trust_level)">{{ trustLabel(row.trust_level) }}</SBadge>
      </template>
      <template #cell-status="{ row }">
        <SStatusBadge :status="row.status" />
        <SHint v-if="row.revoked_at" size="xs" class="mt-0.5">{{ formatDateTime(row.revoked_at) }}</SHint>
      </template>
      <template #cell-keys="{ row }">
        <span v-if="row.keys">{{ activeKeys(row) }}/{{ row.keys.length }}</span>
        <SHint v-else inline>—</SHint>
      </template>
      <template #cell-created_at="{ row }">
        <SHint inline>{{ formatDateTime(row.created_at) }}</SHint>
      </template>
      <template #cell-actions="{ row }">
        <SDropdown :actions="rowActions(row)" @select="onAction(row, $event)" />
      </template>

      <template #expand="{ row }">
        <div class="py-1">
          <div class="mb-2 flex items-center justify-between">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('publishers.signingKeys') }}</h4>
            <SButton v-if="canManage && row.status !== 'revoked'" size="sm" @click="openAddKey(row)">
              <SIcon name="plus" class="h-3.5 w-3.5" />{{ t('publishers.addKey') }}
            </SButton>
          </div>
          <SHint v-if="!row.keys">{{ t('publishers.keysUnavailable') }}</SHint>
          <SHint v-else-if="!row.keys.length">{{ t('publishers.noKeys') }}</SHint>
          <STable v-else :columns="keyColumns" :rows="row.keys" row-key="key_id" dense data-testid="publisher-keys">
            <template #cell-key_id="{ row: k }"><SCode inline :text="k.key_id" /></template>
            <template #cell-public_key="{ row: k }">
              <span class="inline-flex items-center gap-1 max-md:max-w-full">
                <code class="min-w-0 max-w-[16rem] truncate font-mono text-xs" :title="k.public_key">{{ k.public_key }}</code>
                <SCopyButton :text="k.public_key" icon-only :label="t('publishers.copyPublicKey', { key: k.key_id })" />
              </span>
            </template>
            <template #cell-status="{ row: k }">
              <SStatusBadge :status="keyState(k)" :labels="{ pending: t('publishers.notYetValid') }" />
            </template>
            <template #cell-not_before="{ row: k }"><SHint inline>{{ k.not_before ? formatDateTime(k.not_before) : '—' }}</SHint></template>
            <template #cell-not_after="{ row: k }"><SHint inline>{{ k.not_after ? formatDateTime(k.not_after) : '—' }}</SHint></template>
            <template #cell-created_at="{ row: k }"><SHint inline>{{ formatDateTime(k.created_at) }}</SHint></template>
            <template #cell-actions="{ row: k }">
              <SButton v-if="k.status !== 'revoked'" variant="ghost" tone="danger" size="sm" @click="revokeKey(row, k)">
                {{ t('publishers.revoke') }}
              </SButton>
            </template>
          </STable>
        </div>
      </template>
    </STable>
    <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />

    <!-- create publisher -->
    <SFormModal v-model:open="createOpen" :title="t('publishers.create')" :loading="creating" :submit-text="t('common.create')" @submit="submitCreate">
      <SField :label="t('common.name')" required :error="createErrors.name">
        <SInput v-model="createForm.name" placeholder="acme" />
      </SField>
      <SField :label="t('publishers.trustLevel')" :hint="t(`publishers.trustHint_.${createForm.trust_level}`)" :error="createErrors.trust_level">
        <SSelect v-model="createForm.trust_level" :options="trustOptions" />
      </SField>
    </SFormModal>

    <!-- add key -->
    <SFormModal v-model:open="keyOpen" :title="t('publishers.addKeyTitle', { name: keyPublisher?.name || '' })" width="lg" :loading="creating" :submit-text="t('common.add')" @submit="submitKey">
      <SField :label="t('publishers.keyId')" required :hint="t('publishers.keyIdHint')" :error="keyErrors.key_id">
        <SInput v-model="keyForm.key_id" mono placeholder="acme-2026" />
      </SField>
      <SField :label="t('publishers.publicKey')" required :hint="t('publishers.publicKeyHint')" :error="keyErrors.public_key">
        <STextarea v-model="keyForm.public_key" mono class="text-xs" :rows="2" spellcheck="false" />
      </SField>
      <SGrid>
        <SField :label="t('publishers.notBefore')" :hint="t('common.optional')" :error="keyErrors.not_before">
          <SInput v-model="keyForm.not_before" type="datetime-local" />
        </SField>
        <SField :label="t('publishers.notAfter')" :hint="t('common.optional')" :error="keyErrors.not_after">
          <SInput v-model="keyForm.not_after" type="datetime-local" />
        </SField>
      </SGrid>
    </SFormModal>
  </div>
</template>
