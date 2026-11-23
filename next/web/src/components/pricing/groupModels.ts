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

/** Asks the catalog of every enabled global price. */
export type CatalogFetch = () => Promise<CatalogPrice[]>

export const fetchCatalog: CatalogFetch = () => api.get<CatalogPrice[]>('/pricing/catalog')

/** fetchCatalog asked once for every caller (the rows of one editor); a request that failed is asked again by the next caller. */
export function catalogOnce(): CatalogFetch {
  let pending: Promise<CatalogPrice[]> | null = null
  return () => {
    pending ??= fetchCatalog().catch((e) => {
      pending = null
      throw e
    })
    return pending
  }
}

/** The models of an account group (0: one not created yet, which has no table yet) and the catalog `catalog` asks (null: none). */
export async function fetchGroupModels(gid: number, catalog: CatalogFetch | null = fetchCatalog): Promise<GroupModels> {
  const [prices, all] = await Promise.all([gid ? api.get<GroupModelPrices>(`/groups/${gid}/model-prices`) : null, catalog ? catalog() : []])
  return { rows: prices?.models ?? [], catalog: all }
}

/** fetchGroupModels for the latest of `gid`, its catalog asked by `catalog`; null asks nothing. */
export const useGroupModels = (gid: () => number | null, catalog: CatalogFetch = fetchCatalog) => useLatestRequest(gid, (id: number) => fetchGroupModels(id, catalog), 0)

/** The list price of a model in the account group: its row's (the group's model price, else the global price), else the global price. */
export function listPriceIn(models: GroupModels, model: string): PriceDef | null {
  const row = models.rows.find((m) => m.model === model)
  return row ? listPriceOf(row.base, row.price) : (models.catalog.find((c) => c.model === model)?.price ?? null)
}
