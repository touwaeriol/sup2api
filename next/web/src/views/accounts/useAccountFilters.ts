// The filters of the accounts list over its query (`filters`, useList): the
// switches of the advanced filters, the one select of the account type
// (?plugin_key= and ?type=), the options of the selects, how many filters
// are on and clearing them all (`clearIds` clears the plugins' id sets too).
import { computed, ref } from 'vue'
import type { SelectOption } from '@sub2api/ui'
import { activeFilterCount, filterFlag } from '@/composables/useList'
import { i18n, lt } from '@/i18n'
import { accountStatusKeys } from './accountStatus'
import { typeKey, useAccountTypes } from './accountTypes'

const ADVANCED = ['model', 'owner_id', 'created_by', 'mine', 'subordinates', 'orphaned']

export function useAccountFilters(filters: Record<string, string>, clearIds: () => void) {
  const advancedOpen = ref(false)
  return {
    ...switches(filters),
    ...selectOptions(),
    typeFilter: typeFilterOf(filters),
    advancedOpen,
    filterCount: computed(() => activeFilterCount(filters, ['plugin_key'])),
    hasAdvanced: computed(() => ADVANCED.some((key) => !!filters[key])),
    reset() {
      for (const key of Object.keys(filters)) filters[key] = ''
      clearIds()
    }
  }
}

/** Only mine, only my team's, and the accounts of disabled or uninstalled plugins (hidden by default; on asks for `orphaned=all`). */
function switches(filters: Record<string, string>) {
  return {
    onlyMine: filterFlag(filters, 'mine'),
    onlySubordinates: filterFlag(filters, 'subordinates'),
    showOrphaned: computed({
      get: () => filters.orphaned === 'all',
      set: (v: boolean) => {
        filters.orphaned = v ? 'all' : ''
      }
    })
  }
}

/** One select drives both ?plugin_key= and ?type= (account type identity). */
function typeFilterOf(filters: Record<string, string>) {
  return computed({
    get: () => (filters.plugin_key && filters.type ? typeKey(filters.plugin_key, filters.type) : ''),
    set: (raw: SelectOption['value']) => {
      const v = typeof raw === 'string' ? raw : ''
      const i = v.indexOf('/')
      filters.plugin_key = i > 0 ? v.slice(0, i) : ''
      filters.type = i > 0 ? v.slice(i + 1) : ''
    }
  })
}

/** The statuses, and the account types grouped per plugin (<optgroup>), "all" first in each. */
function selectOptions() {
  const { t } = i18n.global
  const accountTypes = useAccountTypes()
  return {
    statusOptions: computed<SelectOption[]>(() => [{ value: '', label: t('accounts.allStatus') }, ...accountStatusKeys.map((s) => ({ value: s, label: t(`accounts.status.${s}`) }))]),
    typeOptions: computed<SelectOption[]>(() => [
      { value: '', label: t('accounts.allTypes') },
      ...accountTypes.grouped.value.map((g) => ({
        value: g.plugin_key,
        label: lt(g.plugin_name) || g.plugin_key,
        options: g.types.map((at) => ({ value: typeKey(at.plugin_key, at.type), label: lt(at.label) || at.type }))
      }))
    ])
  }
}
