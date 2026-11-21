// Group pricing: user groups ("segments" in code, to stay clear of the
// user_groups grant table and of the length tiers of a price), who may use a
// private group, and what a call costs there.
//
//   paid = list price × rate
//
// Both sides resolve on their own, first hit wins:
//   list price: group × model rule        -> global model price
//   rate:       segment × group override  -> group default × segment discount -> group default
//
// A segment prices no model: it only sets rates (CONTRACTS §97.10).
//
// Every user belongs to exactly one segment. A personal segment is a segment of
// kind "personal" with that single user as its member: the system creates it
// when an admin prices a user individually and deletes it when the user is
// moved back to a named segment.
import type { Money, User } from './types'
import type { PriceMode, VisualConfig } from '@/views/prices/priceExpr'

export type SegmentKind = 'named' | 'personal'
/** GET /segments?kind=: `all` lists the named segments (the default first), then the personal ones. */
export type SegmentListKind = SegmentKind | 'all'

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
  /** The groups whose rate the segment sets itself, with that rate (by group id). */
  rate_overrides: Array<{ id: number; name: string; rate: string }>
  /** Personal only. */
  user?: SegmentUser
  /** Personal only: the segment the user was in before. */
  origin?: SegmentRef | null
  created_at?: string
}

/** Where a rate comes from. */
export type RateSource = 'segment' | 'discount' | 'group'
/** Where a list price comes from. */
export type PriceSource = 'group' | 'global'

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
  /** Left out at the global price. `segment`: a model price of a segment, only on records priced before migration 0123 deleted them. */
  price_source?: 'group' | 'segment'
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

/** A model price of a group. */
export type PriceRule =
  /** The global price times `multiplier`; follows the global price when it changes. */
  | { kind: 'multiplier'; multiplier: Money }
  /**
   * A price of its own, written as a global price is (visual or expression),
   * whatever the global price is. `expression` is read of an expression price
   * only; the server always returns it, generated from `config` for a visual one.
   */
  | { kind: 'custom'; mode: 'visual'; config: VisualConfig; expression?: string }
  | { kind: 'custom'; mode: 'expression'; config?: Record<string, never>; expression: string }

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

/** A model of a group's pricing table: one the group provides or has a rule on. */
export interface ModelPriceRow {
  model: string
  /** Whether the group provides the model now; false on a rule over a model it does not, which no request reaches yet. */
  offered: boolean
  /** The global price; null = the model has none and cannot be billed. */
  base: PriceDef | null
  group_rule: PriceRule | null
  /** The price after the rule (a multiplier rule: the global price with `multiplier`); null without a global price. */
  list_price: PriceDef | null
  source: PriceSource
}

/** GET and PUT /groups/:id/model-prices. */
export interface GroupModelPrices {
  group: { id: number; name: string }
  /** The group's default rate. */
  rate: EffectiveRate
  /** By model. */
  models: ModelPriceRow[]
}

/** An item of PUT /groups/:id/model-prices: `rule` null removes it. A rule takes a model with a global price, whether the group provides it or not. */
export interface PriceRulePatch {
  model: string
  rule: PriceRule | null
}

/** A row of GET /pricing/catalog: an enabled global price; the rows come by model. */
export interface CatalogPrice {
  model: string
  price: PriceDef
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
  /** disabled: the group is disabled, whoever asks. */
  access_source: 'disabled' | 'public' | 'grant' | 'none'
  base: PriceDef | null
  list_price: PriceDef | null
  price_source: PriceSource
  /** The group's rule, as configured; it applies when `price_source` is group. */
  price_levels: { group: PriceRule | null }
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
  /** Models the group prices itself, provided or not. */
  price_rule_count: number

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
