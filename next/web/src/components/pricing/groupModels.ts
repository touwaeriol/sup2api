import { api } from '@sub2api/host'
import type { CatalogPrice, GroupModelPrices, ModelPriceRow } from '@/api/pricing'
import { useLatestRequest } from '@/composables/useLatestRequest'

/**
 * The models an account group may set rates on, as its saved price table
 * lists them (GET /groups/:id/model-prices); for one not created yet (0),
 * every enabled global price.
 */
export async function fetchGroupModels(gid: number): Promise<ModelPriceRow[]> {
  if (gid) return (await api.get<GroupModelPrices>(`/groups/${gid}/model-prices`)).models
  return (await api.get<CatalogPrice[]>('/pricing/catalog')).map((c) => ({ model: c.model, offered: true, base: c.price, price: null, rate: null }))
}

/** fetchGroupModels for the latest of `gid`; null asks nothing. */
export const useGroupModels = (gid: () => number | null) => useLatestRequest(gid, fetchGroupModels, 0)
