// The create / edit dialog of the accounts list: a new account picks its type
// first (step 1), then fills the editor (step 2); an existing one opens on
// the editor once its full row has loaded. /accounts?edit=<id> opens the
// editor of that account, and the query loses the parameter.
import { computed, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { useAccountTypes } from './accountTypes'
import { todayQuery } from './accountUsage'

export function useAccountEditorDialog(saved: () => void) {
  const open = ref(false)
  const step = ref<1 | 2>(1)
  const pickedType = ref<AccountType | null>(null)
  const editing = ref<Account | null>(null)
  const state = { open, step, pickedType, editing }
  const { loading, openEdit } = editLoader(state)
  editLink(openEdit)
  return {
    ...state,
    loading,
    title: titleOf(state),
    openEdit,
    openCreate() {
      editing.value = null
      pickedType.value = null
      step.value = 1
      open.value = true
    },
    pick(at: AccountType) {
      pickedType.value = at
      step.value = 2
    },
    onSaved() {
      saved()
      open.value = false
    }
  }
}

interface DialogState {
  open: Ref<boolean>
  step: Ref<1 | 2>
  pickedType: Ref<AccountType | null>
  editing: Ref<Account | null>
}

/** The dialog's title: the account edited, the type picker, or the new account of the type picked. */
function titleOf({ step, pickedType, editing }: DialogState) {
  const { t } = i18n.global
  const accountTypes = useAccountTypes()
  return computed(() => {
    if (editing.value) return `${t('accounts.editTitle')} · ${editing.value.name}`
    if (step.value === 1) return t('accounts.newStep1')
    return `${t('accounts.newTitle')} · ${pickedType.value ? accountTypes.label(pickedType.value.plugin_key, pickedType.value.type) : ''}`
  })
}

/** Opens the editor of an existing account once its full row and its type have loaded; a failure closes it again. */
function editLoader({ open, step, pickedType, editing }: DialogState) {
  const accountTypes = useAccountTypes()
  const loading = ref(false)
  async function openEdit(a: Pick<Account, 'id'>) {
    loading.value = true
    open.value = true
    step.value = 2
    try {
      const full = await api.get<Account>(`/accounts/${a.id}`, todayQuery())
      editing.value = full
      await accountTypes.load()
      pickedType.value = accountTypes.find(full.plugin_key, full.type) || null
    } catch (e) {
      notifyError(e)
      open.value = false
    } finally {
      loading.value = false
    }
  }
  return { loading, openEdit }
}

/** Deep link (/accounts?edit=<id>): opens the editor, and drops the parameter. */
function editLink(openEdit: (a: Pick<Account, 'id'>) => Promise<void>) {
  const route = useRoute()
  const router = useRouter()
  watch(
    () => route.query.edit,
    (raw) => {
      const id = Number(Array.isArray(raw) ? raw[0] : raw)
      if (!raw || !Number.isInteger(id) || id <= 0) return
      void openEdit({ id })
      const query = { ...route.query }
      delete query.edit
      void router.replace({ query })
    },
    { immediate: true }
  )
}
