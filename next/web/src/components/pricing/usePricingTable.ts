import { computed, ref, shallowRef } from 'vue'
import { api } from '@sub2api/host'
import type { CatalogPrice, GroupModelPrices, ModelPriceRow } from '@/api/pricing'
import { i18n } from '@/i18n'
import { listRequest, withDraft } from './drafts'
import { effectiveRate } from './pricingFormat'
import { catalogPrices, catalogRow, draftedRow, mergeRowDraft, groupModelRatePatches, modelPricePatches, savedPrice, validPrice, type DraftedRow, type RowDraft } from './pricingRows'
import { useDrafts } from './useDrafts'

export interface PricingTableOptions {
  /** The group; null for one not created yet, which provides nothing yet. */
  groupId: () => number | null
  /** The group's base rate as its form has it now. */
  baseRate: () => string
  /** Also fetches the catalog of original prices, to add models from. */
  editable: () => boolean
}

interface Saved {
  rows: ModelPriceRow[]
  /** The original prices, to add a model with. */
  catalog: CatalogPrice[]
}

/** A row as the table shows it: what applies under its draft, the rate included. */
export interface PricingRow extends DraftedRow {
  /** The model rate, or the base rate, as it applies. */
  applied: ReturnType<typeof effectiveRate>
  /** Added here and not saved: it can go again. */
  removable: boolean
  /** It has a price to bill by: the original price, or a model price typed in full; else its requests are refused. */
  priced: boolean
  /** Why the server refused what was sent for it. */
  error?: string
}

/**
 * The pricing table of a group while it is edited: the saved rows (the models
 * it provides and those it sets something on), the models added here, and
 * the drafts on top. body() is what the group's request sends of them.
 */
export function usePricingTable(options: PricingTableOptions) {
  const state = useDrafts<Saved, string, RowDraft>({ empty: () => ({ rows: [], catalog: [] }), fetch: () => fetchSaved(options), problem: incomplete })
  const added = ref<string[]>([])
  const rowErrors = shallowRef<ReadonlyMap<string, string>>(new Map())
  const rows = computed(() => listedRows(state.saved.value, added.value).map(viewRow))

  function viewRow(row: ModelPriceRow): PricingRow {
    const r = draftedRow(row, state.drafts.value.get(row.model))
    const applied = effectiveRate({ group_model: r.rate, group: options.baseRate() })
    const removable = added.value.includes(r.model) && !state.saved.value.rows.some((s) => s.model === r.model)
    return { ...r, applied, removable, priced: !!r.base || (!!r.price && validPrice(r.price)), error: rowErrors.value.get(r.model) }
  }

  /** Why the drafts cannot be sent yet: the first row not filled in right, by its model. */
  function incomplete(): string | null {
    const invalid = rows.value.find((r) => r.invalid)
    return invalid ? i18n.global.t('pricing.table.invalid', { model: invalid.model }) : null
  }

  function change(row: PricingRow, draft: RowDraft) {
    state.drafts.value = mergeRowDraft(state.drafts.value, row, draft)
    rowErrors.value = withDraft(rowErrors.value, row.model, undefined)
  }

  return { ...state, rows, added, rowErrors, change, ...modelChanges(state, added), ...requestOf(state, rowErrors) }
}

/** The group's pricing table, and the catalog when models may be added. */
async function fetchSaved(options: PricingTableOptions): Promise<Saved> {
  const id = options.groupId()
  const [prices, catalog] = await Promise.all([
    id === null ? null : api.get<GroupModelPrices>(`/groups/${id}/model-prices`),
    options.editable() ? api.get<CatalogPrice[]>('/pricing/catalog') : []
  ])
  return { rows: (prices?.models ?? []).map((r) => ({ ...r, price: savedPrice(r.price) })), catalog }
}

/** The saved rows, then the models added here that the server does not list, from the catalog. */
function listedRows(saved: Saved, added: string[]): ModelPriceRow[] {
  const rows = new Map(saved.rows.map((r) => [r.model, r]))
  const prices = catalogPrices(saved.catalog)
  added.filter((m) => !rows.has(m)).forEach((m) => rows.set(m, catalogRow(m, prices.get(m) ?? null)))
  return [...rows.values()]
}

type TableState = ReturnType<typeof useDrafts<Saved, string, RowDraft>>

/** Adding a model and taking an added one away again. */
function modelChanges(state: TableState, added: { value: string[] }) {
  return {
    add: (model: string) => (added.value = [...added.value, model]),
    remove: (model: string) => {
      added.value = added.value.filter((m) => m !== model)
      state.drafts.value = withDraft(state.drafts.value, model, undefined)
    }
  }
}

/** What the group's request sends of the drafts (`model_prices`, `model_rates`), and the field errors of its answer by model. */
function requestOf(state: TableState, rowErrors: { value: ReadonlyMap<string, string> }) {
  const byModel = (item: { model: string }) => item.model
  const lists = [listRequest('model_prices', () => modelPricePatches(state.drafts.value), byModel), listRequest('model_rates', () => groupModelRatePatches(state.drafts.value), byModel)]
  return {
    body: () => Object.assign({}, ...lists.map((l) => l.body())),
    applyErrors: (errors: Record<string, string>) => (rowErrors.value = new Map(lists.flatMap((l) => [...l.errors(errors)])))
  }
}
