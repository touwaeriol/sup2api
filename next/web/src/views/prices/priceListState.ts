import { reactive } from 'vue'

/**
 * The search, mode filter and page of the model list beside the price editor
 * (PriceModelList). The page is built anew for each model it opens, so they
 * live here, for as long as the console is open.
 */
export const priceListState = reactive({ q: '', mode: '', page: 1 })
