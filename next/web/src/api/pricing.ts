// Group pricing: user groups ("segments" in code, to stay clear of the
// user_groups grant table and of the length tiers of a price), who may use a
// private group, and what a call costs there.
//
//   paid = list price × rate
//
// Both sides resolve on their own, first hit wins:
//   list price: segment × group × model rule -> group × model rule -> global model price
//   rate:       segment × group override     -> group default × segment discount -> group default
//
// Every user belongs to exactly one segment. A personal segment is a segment of
// kind "personal" with that single user as its member: the system creates it
// when an admin prices a user individually and deletes it when the user is
// moved back to a named segment.
import type { Money, User } from './types'
import type { PriceMode, VisualConfig } from '@/views/prices/priceExpr'

export type SegmentKind = 'named' | 'personal'

/** A segment as other rows refer to it. `name` of a personal segment is its user's email. */
export interface SegmentRef {
  id: number
  kind: SegmentKind
  name: string
  /** The member of a personal segment. */
  user_id?: number
}

export interface SegmentUser {
  id: number
  email: string
  display_name: string
}

/** What the personal-segment dialogs need of a user. */
export type PersonalDialogUser = Pick<User, 'id' | 'email' | 'display_name' | 'segment'>

export interface Segment extends SegmentRef {
  description: string
  /** Multiplies the default rate of every group; "1" = none. An override of a group ignores it. */
  discount: Money
  /** New users join it; it cannot be deleted. */
  is_default: boolean
  member_count: number
  /** Private groups it is granted. */
  granted_groups: Array<{ id: number; name: string }>
  /** Groups whose rate it overrides. */
  rate_override_count: number
  /** Its own model prices, over all groups. */
  price_rule_count: number
  /** Personal only. */
  user?: SegmentUser
  /** Personal only: the segment the user was in before. */
  origin?: SegmentRef | null
  created_at?: string
}

/** Where a rate comes from. */
export type RateSource = 'segment' | 'discount' | 'group'
/** Where a list price comes from. */
export type PriceSource = 'segment' | 'group' | 'global'

export interface EffectiveRate {
  value: Money
  source: RateSource
  /** The group's default rate. */
  group_default: Money
  /** The segment's discount. */
  discount: Money
}

/**
 * billing_detail.inputs.pricing of a usage record: how group pricing priced
 * it, frozen with the record. Left out at the global price and the group's
 * default rate.
 */
export interface UsagePricing {
  segment_id?: number
  rate_source?: RateSource
  price_source?: PriceSource
  /** The multiplier of a multiplier rule. */
  scale?: Money
  /** The global price a custom rule replaced. */
  base?: { expression: string; expr_hash: string }
}

/** The settings of one segment in one group. */
export interface SegmentGroupSetting {
  segment: SegmentRef
  group: { id: number; name: string; description: string; status: string; visibility: 'public' | 'restricted'; rate_multiplier: Money; platforms?: string[] }
  /** Public, or granted. */
  accessible: boolean
  /** The explicit grant; only matters for a private group. */
  granted: boolean
  /** The rate of this segment in this group; null = inherit. */
  rate_override: Money | null
  rate: EffectiveRate
  /** Model prices of this segment in this group. */
  price_rule_count: number
  /** Members of the segment (1 for a personal one). */
  member_count?: number
}

/** PUT /segments/:id/groups and PUT /groups/:id/segments: one changed pair. */
export interface SegmentGroupPatch {
  segment_id?: number
  group_id?: number
  granted?: boolean
  rate_override?: Money | null
}

/** A model price of a group, or of a segment in a group. */
export type PriceRule =
  /** The global price times `multiplier`; follows the global price when it changes. */
  | { kind: 'multiplier'; multiplier: Money }
  /**
   * A price of its own: a visual price of one tier (by tokens or by the
   * request) and no rules. Only a global price of that shape takes it; any
   * other takes a multiplier only.
   */
  | { kind: 'custom'; mode: 'visual'; config: VisualConfig }

/** A rule as a row of model prices carries it. */
export type ListedPriceRule = PriceRule & {
  /** A custom price the global price no longer takes (another mode, or tiers or rules now): it is skipped, and the next level prices the model. */
  inactive?: boolean
}

export interface PriceDef {
  mode: PriceMode
  config: Record<string, any> | null
  expression: string
  /**
   * Only on the list price a multiplier rule gives, 0 and 1 included: the
   * list price is this price, which is the global one as it is, times it.
   * A global price and a custom price have none.
   */
  multiplier?: Money
}

/** One model a group provides, with the rules on it. */
export interface ModelPriceRow {
  model: string
  /** The global price; null = the model has none and cannot be billed. */
  base: PriceDef | null
  group_rule: ListedPriceRule | null
  /** Only when a segment was asked for. */
  segment_rule?: ListedPriceRule | null
  /** Segments with a rule of their own on this model (asked without a segment). */
  segment_rule_count?: number
  /** The price after the first rule in effect (a multiplier rule: the global price with `multiplier`); null without a global price. */
  list_price: PriceDef | null
  source: PriceSource
}

/** GET /groups/:id/model-prices[?segment_id=]. */
export interface GroupModelPrices {
  group: { id: number; name: string }
  segment?: SegmentRef
  /** The rate of the segment asked for, else the group's default. */
  rate: EffectiveRate
  models: ModelPriceRow[]
}

/** PUT /groups/:id/model-prices: `rule` null removes it; `segment_id` null = the group's own rule. */
export interface PriceRulePatch {
  segment_id: number | null
  model: string
  rule: PriceRule | null
}

/** The largest token count a quote takes (core.MaxUsageTokens). */
export const QUOTE_MAX_TOKENS = 1_000_000_000_000

/** POST /pricing/quote: what a user (or any member of a segment) pays for a sample call. Exactly one of `user_id` and `segment_id` is given. */
export interface PricingQuoteRequest {
  user_id?: number
  segment_id?: number
  group_id: number
  model: string
  usage: { p: number; c: number; cr?: number; cc?: number; cc1h?: number }
}

export interface PricingQuote {
  segment: SegmentRef
  accessible: boolean
  access_source: 'public' | 'grant' | 'none'
  base: PriceDef | null
  list_price: PriceDef | null
  price_source: PriceSource
  /** The two rule levels of the list price, as configured; the one in `price_source` applies, the global price when it says so. */
  price_levels: { segment: ListedPriceRule | null; group: ListedPriceRule | null }
  rate: EffectiveRate
  /** The override of this segment in this group, if any. */
  rate_override: Money | null
  /** list price × usage. */
  list_cost: Money
  /** list cost × rate. */
  cost: Money
}

/** A row of GET /me/groups: what the caller gets in the group. */
export interface MyGroupPricing {
  effective_rate: Money
  rate_source: RateSource
  /** A private group granted to the caller's segment. */
  exclusive: boolean
}

/** What GET /groups adds to a group row. */
export interface GroupPricingSummary {
  /** Who may use a private group: the named segments granted, and how many personal ones. */
  access: { segments: SegmentRef[]; personal_count: number }
  /** Models the group prices itself. */
  price_rule_count: number
  /** Model prices of segments in the group. */
  segment_rule_count: number
  /** Models the group provides. */
  model_count: number
}

/** A row of GET /me/prices. */
export interface MyPriceRow extends PriceDef {
  model: string
  /** Without a group: the caller's groups that provide the model. */
  groups?: Array<{ id: number; name: string }>
  /** With a group: where the list price comes from. */
  source?: PriceSource
}

/** GET /me/prices[?group_id=]: every model at its global price, or what one group provides at the caller's price there. */
export interface MyPrices {
  group: { id: number; name: string } | null
  rate_multiplier: Money
  rate_source?: RateSource
  prices: MyPriceRow[]
}
