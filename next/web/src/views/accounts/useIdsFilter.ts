// The account.list.toolbar slot (CONTRACTS §23.3 UI 3.6) narrows the list to
// the ids plugins name. Each plugin keeps its own set and chip label (a
// plugin's `ids` prop is that set; an empty array or null clears it); the list
// gets the intersection of every set (`?ids=1,2`, at most MAX_ACCOUNT_BATCH:
// the rest are dropped and the chips say so). Sets without a common id ask
// for id 0, which no account has, so the list comes back empty.
import { computed, ref, watch } from 'vue'
import { MAX_ACCOUNT_BATCH } from '@sub2api/host'
import type { SlotEntry } from '@/stores/plugins'

interface IdsFilter {
  ids: number[]
  label: string
}

/** The ids common to every set (null without any), cut to MAX_ACCOUNT_BATCH. */
function commonIds(sets: number[][]) {
  if (!sets.length) return null
  const common = sets.reduce((acc, ids) => {
    const own = new Set(ids)
    return acc.filter((id) => own.has(id))
  })
  return { ids: common.slice(0, MAX_ACCOUNT_BATCH), truncated: common.length > MAX_ACCOUNT_BATCH }
}

/** `filters.ids` is the list's ?ids=, which this keeps. */
export function useIdsFilter(filters: Record<string, string>) {
  const idsFilters = ref<Record<string, IdsFilter>>({})
  /** Clears one plugin's id set, or every set. */
  function clearIds(pluginKey?: string) {
    if (pluginKey) delete idsFilters.value[pluginKey]
    else idsFilters.value = {}
  }
  const toolbarProps = (entry: SlotEntry) => ({
    ids: idsFilters.value[entry.pluginKey]?.ids ?? null,
    setIds(ids: number[] | null, label: string) {
      // Plugin input is clamped on the way in: positive integers only, no repeats.
      const clean = [...new Set((ids ?? []).filter((id) => Number.isInteger(id) && id > 0))]
      if (clean.length) idsFilters.value[entry.pluginKey] = { ids: clean, label }
      else clearIds(entry.pluginKey)
    }
  })
  const idsCommon = computed(() => commonIds(Object.values(idsFilters.value).map((f) => f.ids)))
  watch(idsCommon, (c) => {
    filters.ids = !c ? '' : c.ids.length ? c.ids.join(',') : '0'
  })
  return { idsFilters, idsCommon, toolbarProps, clearIds }
}
