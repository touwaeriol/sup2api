import { api } from '@sub2api/host'
import type { CatalogPrice, GroupModelPrices, ModelPriceRow, PriceDef } from '@/api/pricing'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { listPriceOf } from './pricingFormat'

/** What an account group's model rates are set against: its saved price table and every enabled global price. */
export interface GroupModels {
  /** The rows of GET /groups/:id/model-prices; none for a group not created yet. */
  rows: ModelPriceRow[]
  catalog: CatalogPrice[]
}

/** The models of an account group (0: one not created yet, which has no table yet) and the catalog of global prices. */
export async function fetchGroupModels(gid: number): Promise<GroupModels> {
  const [prices, catalog] = await Promise.all([gid ? api.get<GroupModelPrices>(`/groups/${gid}/model-prices`) : null, api.get<CatalogPrice[]>('/pricing/catalog')])
  return { rows: prices?.models ?? [], catalog }
}

/** fetchGroupModels for the latest of `gid`; null asks nothing. */
export const useGroupModels = (gid: () => number | null) => useLatestRequest(gid, fetchGroupModels, 0)

/** The list price of a model in the account group: its row's (the group's model price, else the global price), else the global price. */
export function listPriceIn(models: GroupModels, model: string): PriceDef | null {
  const row = models.rows.find((m) => m.model === model)
  return row ? listPriceOf(row.base, row.price) : (models.catalog.find((c) => c.model === model)?.price ?? null)
}
