// Protocol binding of an account (DESIGN-2026-11-17-PROTOCOL-BINDING §3, §5):
// `protocols` opens protocols of the account type's own platforms (null = all),
// `model_protocols` lists the protocols of chosen models, conversions to other
// platforms included. Everything here is pure: the columns an account type
// offers, the per-model rules of the editor, what is saved, the checks and the
// summaries. The editor state lives in useProtocolEdit.
import type { Account, AccountType, AccountTypeEndpoint, ConversionGrade, ModelProtocolRule, ServedVia } from '@/api/types'

/** The entry of `model_protocols` standing for every model no other entry lists. */
export const REST = '*'

/** A count_tokens protocol follows the generation protocol of its family and is never listed on its own (§3.3). */
export const COUNT_TOKENS_FAMILY: Readonly<Record<string, string>> = {
  'anthropic.count_tokens': 'anthropic.messages',
  'gemini.count_tokens': 'gemini.generate'
}

/** Protocols shown and ticked as one; each id is still saved. */
const MERGED: readonly (readonly string[])[] = [['gemini.generate', 'gemini.stream_generate']]

/** One protocol the editor offers: a protocol id, or the ids ticked together. */
export interface ProtocolColumn {
  /** The first protocol id. */
  key: string
  /** The protocol ids saved for the column. */
  ids: string[]
  platform: string
  method: string
  path: string
  via: ServedVia
  grade: ConversionGrade
  /** Protocols passed through when adapted or converted, client first. */
  hops?: string[]
}

/** The protocols of an account type: of its own platforms, and those it reaches through a conversion. */
export interface ProtocolColumns {
  own: ProtocolColumn[]
  cross: ProtocolColumn[]
}

type TypeProtocols = Pick<AccountType, 'platforms' | 'endpoints'> | null | undefined

/** How a type serves an endpoint, given the platforms it declares. */
export const viaOf = (e: AccountTypeEndpoint, declared: Set<string>): ServedVia => (e.native ? 'native' : declared.has(e.platform) ? 'adapted' : 'converted')

/** Folds the columns of a merged set into the first of them. */
function merged(columns: ProtocolColumn[]): ProtocolColumn[] {
  const out: ProtocolColumn[] = []
  for (const c of columns) {
    const set = MERGED.find(ids => ids.includes(c.key))
    const first = set && out.find(x => set.includes(x.key))
    if (first) first.ids.push(c.key)
    else out.push(c)
  }
  return out
}

/**
 * The protocol columns of an account type, from the endpoints it serves: one
 * per protocol, without count_tokens and local estimates. A protocol of a
 * platform the type declares is its own (native or adapted to another
 * endpoint of the platform); any other is a conversion.
 */
export function protocolColumns(at: TypeProtocols): ProtocolColumns {
  const declared = new Set((at?.platforms ?? []).map(p => p.id))
  const seen = new Set<string>()
  const own: ProtocolColumn[] = []
  const cross: ProtocolColumn[] = []
  for (const e of at?.endpoints ?? []) {
    if (!e.protocol || seen.has(e.protocol) || e.protocol in COUNT_TOKENS_FAMILY || e.grade === 'local') continue
    seen.add(e.protocol)
    const via = viaOf(e, declared)
    const column: ProtocolColumn = { key: e.protocol, ids: [e.protocol], platform: e.platform, method: e.method, path: e.path, via, grade: e.grade, hops: e.via }
    ;(via === 'converted' ? cross : own).push(column)
  }
  return { own: merged(own), cross: merged(cross) }
}

export const allColumns = (c: ProtocolColumns) => [...c.own, ...c.cross]
const idsOf = (columns: ProtocolColumn[]) => columns.flatMap(c => c.ids)

/** Whether two account types offer the same protocols, own and converted. */
export function sameProtocolColumns(a: TypeProtocols, b: TypeProtocols): boolean {
  const key = (t: TypeProtocols) => allColumns(protocolColumns(t)).map(c => `${c.via}:${c.ids.join('+')}`).join(',')
  return key(a) === key(b)
}

/** A conversion through two converters. */
const isTwoHop = (c: Pick<ProtocolColumn, 'hops'>) => (c.hops?.length ?? 0) > 2

type ColumnState = 'on' | 'partial' | 'off'

/** Whether the ids hold the column: all of its ids, some (hand-written data) or none. */
export function columnState(ids: readonly string[] | null, c: ProtocolColumn): ColumnState {
  if (ids === null) return 'on'
  const n = c.ids.filter(id => ids.includes(id)).length
  return n === c.ids.length ? 'on' : n ? 'partial' : 'off'
}

/** The ids with the column switched: every id of a column that is fully on is removed, otherwise all are added. */
export function toggleColumn(ids: readonly string[], c: ProtocolColumn): string[] {
  if (columnState(ids, c) === 'on') return ids.filter(id => !c.ids.includes(id))
  return [...ids, ...c.ids.filter(id => !ids.includes(id))]
}

/** The own columns the account opens (in part counts). */
export const openColumns = (c: ProtocolColumns, protocols: readonly string[] | null) => c.own.filter(x => columnState(protocols, x) !== 'off')

/** The own protocols every account opens, from their `protocols` (null or absent: all of them); null when every one opens all. */
export function sharedOwnProtocols(lists: ReadonlyArray<readonly string[] | null | undefined>): string[] | null {
  const chosen = lists.filter((l): l is readonly string[] => Array.isArray(l))
  return chosen.length ? chosen[0].filter(id => chosen.every(l => l.includes(id))) : null
}

/** The protocols of a model without a rule: the own protocols the account opens, no conversion. */
export function defaultProtocols(c: ProtocolColumns, protocols: readonly string[] | null): string[] {
  const own = idsOf(c.own)
  return protocols === null ? own : own.filter(id => protocols.includes(id))
}

// ---------------------------------------------------------------- rules: model -> protocols

/** The protocols of each model with a rule (REST for the other models); a model without an entry is served by default. */
export type ModelRules = Record<string, string[]>

/** Rules by model, from the saved entries; a model listed twice keeps its first entry. */
export function rulesByModel(saved: readonly ModelProtocolRule[] | null | undefined): ModelRules {
  const out: ModelRules = {}
  for (const r of saved ?? []) {
    for (const m of r.models ?? []) if (!(m in out)) out[m] = [...(r.protocols ?? [])]
  }
  return out
}

/** Protocol ids in the order of `order`, unknown ones last by name. */
function sorted(ids: readonly string[], order: readonly string[]): string[] {
  const rank = (id: string) => (order.includes(id) ? order.indexOf(id) : order.length)
  return [...new Set(ids)].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
}

/**
 * The entries of the rules: the models with the same protocols form one
 * entry, in the order they first appear; the other models (REST) are always
 * an entry of their own, last. Protocols follow the columns' order.
 */
export function modelProtocolRules(rules: ModelRules, c: ProtocolColumns): ModelProtocolRule[] {
  const order = idsOf(allColumns(c))
  const byKey = new Map<string, ModelProtocolRule>()
  for (const [model, ids] of Object.entries(rules)) {
    if (model === REST) continue
    const protocols = sorted(ids, order)
    const key = protocols.join(',')
    const entry = byKey.get(key)
    if (entry) entry.models.push(model)
    else byKey.set(key, { models: [model], protocols })
  }
  const out = [...byKey.values()]
  if (rules[REST]) out.push({ models: [REST], protocols: sorted(rules[REST], order) })
  return out
}

/** Rows of the matrix: the account's models, then the models that only have a rule (REST excluded: it is the fixed last row). */
export function matrixModels(models: readonly string[], rules: ModelRules): string[] {
  return [...new Set([...models, ...Object.keys(rules).filter(m => m !== REST)])]
}

/**
 * The protocols a model is served on, as the server judges it (CONTRACTS
 * §110.3): those of its own rule, else of the rule of the other models, else
 * the default.
 */
export const protocolsOf = (rules: ModelRules, model: string, defaults: readonly string[]): readonly string[] => rules[model] ?? rules[REST] ?? defaults

/** The rules with a cell switched; a row without a rule first takes the protocols it is served on. */
export function toggleCell(rules: ModelRules, model: string, c: ProtocolColumn, defaults: readonly string[]): ModelRules {
  return { ...rules, [model]: toggleColumn(protocolsOf(rules, model, defaults), c) }
}

/** The rules without these models (back to the default). */
export function withoutModels(rules: ModelRules, models: readonly string[]): ModelRules {
  return Object.fromEntries(Object.entries(rules).filter(([m]) => !models.includes(m)))
}

/** The rules with these models set to the protocols. */
export function withModels(rules: ModelRules, models: readonly string[], protocols: readonly string[]): ModelRules {
  return { ...rules, ...Object.fromEntries(models.map(m => [m, [...protocols]])) }
}

// ---------------------------------------------------------------- cards of the list view

/**
 * An entry while it is edited in the list view. A card keeps its identity
 * (`id`) whatever its models and protocols become: two cards given the same
 * protocols stay two cards, and are one entry only in what is saved
 * (modelProtocolRules).
 */
export interface RuleCard extends ModelProtocolRule {
  id: number
}

const isRestCard = (c: ModelProtocolRule) => c.models[0] === REST

/** The cards of the rules: one per entry, numbered from `nextId`. */
export function ruleCards(rules: ModelRules, c: ProtocolColumns, nextId = 1): RuleCard[] {
  return modelProtocolRules(rules, c).map((e, i) => ({ ...e, id: nextId + i }))
}

/** The rules the cards stand for. */
export function cardRules(cards: readonly ModelProtocolRule[]): ModelRules {
  return Object.fromEntries(cards.flatMap(c => c.models.map(m => [m, [...c.protocols]])))
}

/**
 * The cards with card `id` holding `models`: a model is in one card only, so
 * the others lose those it takes, and a card left without a model goes.
 */
export function withCardModels(cards: readonly RuleCard[], id: number, models: readonly string[]): RuleCard[] {
  const kept = models.filter(m => m !== REST)
  return cards.map(c => ({ ...c, models: c.id === id ? kept : c.models.filter(m => !kept.includes(m)) })).filter(c => c.models.length)
}

/** The cards with a new one for `models` (taken out of the cards they were in), before the card of the other models. */
export function withNewCard(cards: readonly RuleCard[], models: readonly string[], protocols: readonly string[]): RuleCard[] {
  const fresh: RuleCard = { id: Math.max(0, ...cards.map(c => c.id)) + 1, models: [...models], protocols: [...protocols] }
  const others = cards.map(c => ({ ...c, models: c.models.filter(m => !models.includes(m)) })).filter(c => c.models.length)
  return isRestCard(fresh) ? [...others, fresh] : [...others.filter(c => !isRestCard(c)), fresh, ...others.filter(isRestCard)]
}

// ---------------------------------------------------------------- what is edited and saved

/** The two account fields. */
export interface ProtocolBinding {
  protocols: string[] | null
  model_protocols: ModelProtocolRule[]
}

const sameList = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((x, i) => x === b[i])

/** Whether two values of `protocols` open the same protocols. */
export function sameProtocols(a: readonly string[] | null | undefined, b: readonly string[] | null | undefined): boolean {
  if (a == null || b == null) return a == null && b == null
  return sameList([...a].sort(), [...b].sort())
}

/** Whether two values of `model_protocols` give every model the same protocols. */
export function sameModelProtocols(a: readonly ModelProtocolRule[] | null | undefined, b: readonly ModelProtocolRule[] | null | undefined): boolean {
  const x = rulesByModel(a)
  const y = rulesByModel(b)
  const models = Object.keys(x)
  return models.length === Object.keys(y).length && models.every(m => m in y && sameProtocols(x[m], y[m]))
}

export type BindingProblem = 'json' | 'shape' | 'star' | 'duplicate'

const isStrings = (v: unknown): v is string[] => Array.isArray(v) && v.every(x => typeof x === 'string' && x !== '')

/** The entries of a parsed `model_protocols`, or what is wrong with it (and the model, for a duplicate). */
function parseRules(v: unknown): ModelProtocolRule[] | { problem: BindingProblem; model?: string } {
  if (!Array.isArray(v)) return { problem: 'shape' }
  const seen = new Set<string>()
  const out: ModelProtocolRule[] = []
  for (const r of v) {
    if (!r || typeof r !== 'object' || !isStrings(r.models) || !isStrings(r.protocols) || !r.models.length) return { problem: 'shape' }
    if (r.models.includes(REST) && r.models.length > 1) return { problem: 'star' }
    for (const m of r.models) {
      if (seen.has(m)) return { problem: 'duplicate', model: m }
      seen.add(m)
    }
    out.push({ models: [...r.models], protocols: [...r.protocols] })
  }
  return out
}

/** Parses the JSON view: `{"protocols": null | [...], "model_protocols": [{models, protocols}]}`; an absent field is its default. */
export function parseBinding(text: string): ProtocolBinding | { problem: BindingProblem; model?: string } {
  let v: unknown
  try {
    v = text.trim() ? JSON.parse(text) : {}
  } catch {
    return { problem: 'json' }
  }
  if (!v || typeof v !== 'object' || Array.isArray(v)) return { problem: 'shape' }
  const { protocols = null, model_protocols = [] } = v as Record<string, unknown>
  if (protocols !== null && !isStrings(protocols)) return { problem: 'shape' }
  const rules = parseRules(model_protocols)
  return Array.isArray(rules) ? { protocols: protocols === null ? null : [...protocols], model_protocols: rules } : rules
}

/** The JSON view's text: one entry per line. */
export function bindingJSON(b: ProtocolBinding): string {
  const spaced = (v: unknown) => JSON.stringify(v).replace(/","/g, '", "').replace(/":/g, '": ').replace(/\],"/g, '], "')
  const rules = b.model_protocols.length ? `[\n${b.model_protocols.map(r => `    ${spaced(r)}`).join(',\n')}\n  ]` : '[]'
  return `{\n  "protocols": ${spaced(b.protocols)},\n  "model_protocols": ${rules}\n}`
}

// ---------------------------------------------------------------- checks and summaries

type IssueCode = 'empty_rule' | 'no_protocol' | 'closed_protocol' | 'unknown_protocol' | 'rest_conversion' | 'two_hop'

/** A finding of the editor: errors block saving, warnings and notes do not. */
export interface BindingIssue {
  level: 'error' | 'warning' | 'info'
  code: IssueCode
  /** The model of the rule (REST for the other models). */
  model?: string
  /** The column the finding is about. */
  column?: ProtocolColumn
  /** A protocol id no column has. */
  protocol?: string
}

/** Errors first, notes last. */
const LEVELS: BindingIssue['level'][] = ['error', 'warning', 'info']

/** Columns holding any of the ids. */
const columnsOf = (columns: ProtocolColumn[], ids: readonly string[]) => columns.filter(c => columnState(ids, c) !== 'off')

/** Whether a rule turns a conversion on. */
const converts = (c: ProtocolColumns, ids: readonly string[]) => columnsOf(c.cross, ids).length > 0

/**
 * The ids of `protocols` that are no own protocol of the type (its endpoint
 * is gone, or it was written by hand): the server refuses them. The own
 * protocols field shows them; they are no binding issue.
 */
export function unknownOwnProtocols(protocols: readonly string[] | null, c: ProtocolColumns): string[] {
  const own = idsOf(c.own)
  return (protocols ?? []).filter(id => !own.includes(id))
}

/**
 * Checks the binding (design §3.1, §3.2, §5.1): a rule without protocols and
 * an account that serves nothing are errors; an own protocol the account does
 * not open, a protocol the type cannot serve (in `protocols` or in a rule)
 * and a conversion for every other model are warnings; a two-hop conversion
 * is a note.
 */
export function bindingIssues(protocols: readonly string[] | null, rules: ModelRules, c: ProtocolColumns): BindingIssue[] {
  const out: BindingIssue[] = []
  const known = new Set(idsOf(allColumns(c)))
  const open = openColumns(c, protocols)
  for (const [model, ids] of Object.entries(rules)) {
    if (!ids.length) out.push({ level: 'error', code: 'empty_rule', model })
    for (const column of columnsOf(c.own, ids)) if (!open.includes(column)) out.push({ level: 'warning', code: 'closed_protocol', model, column })
    for (const protocol of ids) if (!known.has(protocol)) out.push({ level: 'warning', code: 'unknown_protocol', model, protocol })
    if (model === REST && converts(c, ids)) out.push({ level: 'warning', code: 'rest_conversion', model })
    for (const column of columnsOf(c.cross, ids)) if (isTwoHop(column)) out.push({ level: 'info', code: 'two_hop', model, column })
  }
  if (protocols !== null && !open.length && !Object.values(rules).some(ids => converts(c, ids))) out.push({ level: 'error', code: 'no_protocol' })
  return out.sort((a, b) => LEVELS.indexOf(a.level) - LEVELS.indexOf(b.level))
}

/** Figures of the section summary: own protocols open / offered, models with a rule, models with a conversion. */
export function bindingSummary(protocols: readonly string[] | null, rules: ModelRules, c: ProtocolColumns) {
  return {
    open: openColumns(c, protocols).length,
    total: c.own.length,
    limited: Object.keys(rules).length,
    converting: Object.values(rules).filter(ids => converts(c, ids)).length
  }
}

/** Whether a request may be converted or adapted on this account: a model with a conversion, or an open adapted protocol. */
export function conversionInUse(protocols: readonly string[] | null, rules: ModelRules, c: ProtocolColumns): boolean {
  return Object.values(rules).some(ids => converts(c, ids)) || openColumns(c, protocols).some(x => x.via === 'adapted')
}

// ---------------------------------------------------------------- what an account serves, by platform (account list)

/** A platform an account serves: one of its type's own, or one reached by converting some models. */
export interface PlatformMark {
  platform: string
  /** true: reached through conversion only. */
  converted: boolean
  /** The columns served: own ones the account opens, or conversions some model turns on. */
  columns: ProtocolColumn[]
  /** Own columns of the platform (0 for a converted one). */
  total: number
  /** Models with a conversion to the platform (REST included), for a converted one. */
  models: string[]
}

/**
 * The platforms an account serves (design §5.2): the available platforms of
 * its type with the protocols the account opens, then the platforms some
 * model is converted for.
 */
export function platformMarks(a: Pick<Account, 'protocols' | 'model_protocols'>, at: TypeProtocols): PlatformMark[] {
  const c = protocolColumns(at)
  const protocols = a.protocols ?? null
  const rules = rulesByModel(a.model_protocols)
  const open = openColumns(c, protocols)
  const own = (at?.platforms ?? [])
    .filter(p => p.available !== false)
    .map(p => ({ platform: p.id, converted: false, columns: open.filter(x => x.platform === p.id), total: c.own.filter(x => x.platform === p.id).length, models: [] }))
  const cross: PlatformMark[] = []
  for (const column of c.cross) {
    const models = Object.keys(rules).filter(m => columnState(rules[m], column) !== 'off')
    if (!models.length) continue
    let mark = cross.find(x => x.platform === column.platform)
    if (!mark) cross.push((mark = { platform: column.platform, converted: true, columns: [], total: 0, models: [] }))
    mark.columns.push(column)
    mark.models = [...new Set([...mark.models, ...models])]
  }
  return [...own, ...cross]
}
