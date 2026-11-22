// How group pricing priced a usage record (billing_detail.inputs.pricing),
// as the record's billing facts read it. It also reads what only records
// priced before the current rules carry: a user group's model price
// (price_source "segment", before migration 0123) and the multiplier of a
// multiplier rule (`scale`, before migration 0124). This is the one place
// those are read.
import type { UsagePricing } from '@/api/pricing'
import type { Translate } from '@/components/pricing/pricingFormat'
import type { UsageRow } from './usage'

type Detail = NonNullable<UsageRow['billing_detail']>

/** The group pricing of one priced part of a record; a record at the global price and the group's default rate has none. */
export function pricingOf(detail: Detail): UsagePricing {
  return detail.inputs?.pricing || {}
}

/** The price that replaced the original price, when one did: whose it is and what it made of the original price; null when none did. */
export function listRuleOf(t: Translate, pricing: UsagePricing): { source: 'group' | 'segment'; label: string; text: string } | null {
  const { price_source: source, scale, base } = pricing
  if (!source) return null
  const multiplier = scale === undefined ? '' : t('usage.billing.listRule.scale', { rate: Number(scale) })
  return { source, label: t(`usage.billing.listRule.${source}`), text: base ? t('pricing.editor.customText') : multiplier }
}
