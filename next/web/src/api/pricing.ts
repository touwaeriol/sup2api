// Group pricing: user groups ("segments" in code, to stay clear of the
// user_groups grant table and of the length tiers of a price), who may use a
// private account group ("group" in code), and what a call costs there.
//
//   paid = list price × rate
//
// Both sides resolve on their own:
//   list price: the group's model price -> the global price
//   rate:       one applies, never a product; the most specific first:
//               segment × group × model -> segment × group -> group × model -> group base rate
//
// A segment prices no model: it only sets rates. A group's model price and
// rates, and a segment's rates, are written with the group or the segment
// itself (POST/PATCH /groups, POST/PATCH /segments), all in one request.
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
  /** New users join it; it cannot be deleted. */
  is_default: boolean
  member_count: number
  /** Private groups it is granted. */
  granted_groups: Array<{ id: number; name: string }>
  /** The groups whose rate the segment sets itself, with that rate (by group id). */
  rate_overrides: Array<{ id: number; name: string; rate: string }>
  /** Personal only. */
  user?: SegmentUser
  /** Personal only: the segment the user was in before. */
  origin?: SegmentRef | null
  created_at?: string
}

/** Where a rate comes from, the most specific first: the segment's for the model in the group, the segment's in the group, the group's for the model, the group's base rate. */
export type RateSource = 'segment_model' | 'segment' | 'group_model' | 'group'
/** Where a list price comes from. */
export type PriceSource = 'group' | 'global'

export interface EffectiveRate {
  value: Money
  source: RateSource
  /** The group's base rate. */
  group_default: Money
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
  /** The multiplier of a multiplier rule, on records priced before migration 0124 turned those rules into model rates. */
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
  /** The rate of this segment in this group; null = follow the group. */
  rate_override: Money | null
  /** The rate at the level of the group (no model): the override, or the group's base rate. */
  rate: EffectiveRate
  /** The segment's model rates in this group, by model: those models are paid at these instead. */
  model_rates: ModelRate[]
  /** Members of the segment (1 for a personal one). */
  member_count?: number
}

/** A model paid at a rate of its own (of a group, or of a segment in a group). */
export interface ModelRate {
  model: string
  rate: Money
}

/**
 * The model price of a group: a price of its own, written as a global price
 * is (visual or expression), whatever the global price is. `expression` is
 * read of an expression price only; the server always returns it, generated
 * from `config` for a visual one.
 */
export type CustomPrice =
  | { mode: 'visual'; config: VisualConfig; expression?: string }
  | { mode: 'expression'; config?: Record<string, never>; expression: string }

export interface PriceDef {
  mode: PriceMode
  config: Record<string, any> | null
  expression: string
}

/** A model of a group's pricing table: one the group provides, or one it sets a price or a rate on. */
export interface ModelPriceRow {
  model: string
  /** Whether the group provides the model now; false on a price or a rate over a model it does not, which no request reaches yet. */
  offered: boolean
  /** The global price ("original price"); null = the model has none and cannot be billed. */
  base: PriceDef | null
  /** The group's model price; null = the original price. */
  price: CustomPrice | null
  /** The group's model rate; null = the group's base rate. */
  rate: Money | null
}

/** GET /groups/:id/model-prices. */
export interface GroupModelPrices {
  group: { id: number; name: string }
  /** The group's base rate. */
  rate: EffectiveRate
  /** By model. */
  models: ModelPriceRow[]
}

/** An item of `model_prices` (POST/PATCH /groups): `rule` null removes the model price. It takes a model with a global price, whether the group provides it or not. */
export interface ModelPricePatch {
  model: string
  rule: CustomPrice | null
}

/** An item of `model_rates` (POST/PATCH /groups): `rate` is required, null removes the model rate. */
export interface ModelRatePatch {
  model: string
  rate: Money | null
}

/**
 * An item of `segment_rates` (POST/PATCH /groups) or, from the other side,
 * `group_rates` (POST/PATCH /segments): one segment in one group. `granted`
 * left out stays; `rate` is required, null follows the group. An item of
 * `segment_rates` may instead `remove` everything the segment has in the
 * group: its grant, its rate and its model rates there.
 */
export type SegmentRatePatch = { segment_id: number; granted?: boolean; rate: Money | null } | { segment_id: number; remove: true }
export interface GroupRatePatch {
  group_id: number
  granted?: boolean
  rate: Money | null
}

/** An item of `model_rates` (POST/PATCH /segments): the segment's rate for a model in a group (the model has an enabled global price); `rate` is required, null removes it. */
export interface SegmentModelRatePatch {
  group_id: number
  model: string
  rate: Money | null
}

/**
 * What POST /groups and PATCH /groups/:id take besides the group's own
 * fields; PATCH sends only what changed. Checked as one: any item that is
 * wrong changes nothing, and its field error names it (`model_prices.<i>.rule`,
 * `model_rates.<i>.rate`, `segment_rates.<i>.rate`, `<i>` its position).
 */
export interface GroupPricingWrite {
  model_prices?: ModelPricePatch[]
  model_rates?: ModelRatePatch[]
  segment_rates?: SegmentRatePatch[]
}

/** POST /segments and PATCH /segments/:id, checked as one like GroupPricingWrite (`group_rates.<i>.rate`, `model_rates.<i>.rate`). A personal segment has no name or description. */
export interface SegmentWrite {
  name?: string
  description?: string
  group_rates?: GroupRatePatch[]
  model_rates?: SegmentModelRatePatch[]
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
  /** The group's model price, as configured; it applies when `price_source` is group. */
  price_levels: { group: CustomPrice | null }
  rate: EffectiveRate
  /** Every level of the rate, most specific first; null = not set. The one `rate.source` names applies. */
  rate_levels: { segment_model: Money | null; segment: Money | null; group_model: Money | null; group: Money }
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
  /** Models the group sets a price or a model rate of its own on, provided or not. */
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
  /** With a group: the rate the caller pays for the model there, of the four levels, as billing takes it. */
  rate?: Money
  /** With a group: where `rate` comes from. */
  rate_source?: RateSource
}

/** GET /me/prices[?group_id=]: every model at its global price, or what one group provides at the caller's price there. The rate on top is the group's, without a model ('1' without a group). */
export interface MyPrices {
  group: { id: number; name: string } | null
  rate_multiplier: Money
  rate_source?: RateSource
  prices: MyPriceRow[]
}
