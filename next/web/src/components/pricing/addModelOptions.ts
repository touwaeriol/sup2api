// What "Add model" offers in the pricing views (the price table of an
// account group, the model rates of a user group there), in this order: the
// models the account group's accounts offer, then every model with a global
// price, then (greyed, so a typed name that is already there is not added
// again) the models already listed. AddSelect's free entry comes last.
import type { SelectOption } from '@sub2api/ui'
import type { CatalogPrice, PriceDef } from '@/api/pricing'
import { priceLabel } from './priceLabels'
import type { Translate } from './pricingFormat'

/** A model the accounts offer, at its list price in the account group. */
interface OfferedModel {
  model: string
  price: PriceDef | null
}

type Source = 'offered' | 'catalog'

/** The options, each tagged with where it comes from and hinted with its price; `taken` are the models listed already. */
export function addModelOptions(t: Translate, offered: readonly OfferedModel[], catalog: readonly CatalogPrice[], taken: ReadonlySet<string>): SelectOption[] {
  const option = (model: string, price: PriceDef | null, source: Source): SelectOption => ({ value: model, label: model, tag: t(`pricing.add.source.${source}`), hint: priceLabel(t, price) })
  const fromAccounts = offered.filter((m) => !taken.has(m.model))
  const shown = new Set(fromAccounts.map((m) => m.model))
  const fromPrices = catalog.filter((c) => !taken.has(c.model) && !shown.has(c.model))
  return [
    ...fromAccounts.map((m) => option(m.model, m.price, 'offered')),
    ...fromPrices.map((c) => option(c.model, c.price, 'catalog')),
    ...[...taken].map((model) => ({ value: model, label: model, tag: t('pricing.add.source.added'), disabled: true }))
  ]
}
