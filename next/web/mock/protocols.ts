// Mock of the protocol binding (docs/DESIGN-2026-11-17-PROTOCOL-BINDING.md):
// what an account type serves (native, adapted within a platform, converted
// from another platform, count_tokens estimated locally), what an account and
// a group serve with their `protocols` / `model_protocols`, and the field
// errors of the two. The rules, the field paths and the error codes mirror
// the core's (CONTRACTS §110.3, §110.6, §110.7); model ids are not checked
// and the grades are made up.
import type { AccountServedProtocol as Served, AccountTypeEndpoint as TypeEndpoint, ModelProtocolRule as Rule, ServedVia } from '../src/api/types'
import { COUNT_TOKENS_FAMILY, REST, viaOf } from '../src/views/accounts/protocols/protocolBinding'
import { activePlatforms } from './platforms'

/** Protocols the core converts between. */
const TEXT = ['anthropic.messages', 'openai.chat', 'openai.responses', 'gemini.generate', 'gemini.stream_generate']
const platformOf = (protocol: string) => protocol.split('.')[0]

/**
 * The endpoints of GET /account-types for a type supporting `supports`:
 * `subset` (protocols) narrows what it serves natively, the other text
 * endpoints of its platforms are adapted to the first one it has. Text
 * endpoints of other platforms are converted (Anthropic and Gemini reach each
 * other in two hops, through OpenAI chat), and their count_tokens estimated.
 */
export function typeEndpoints(supports: string[], subset?: string[]): TypeEndpoint[] {
  const all = activePlatforms().flatMap((p) => p.endpoints.map((e) => ({ method: e.method, path: e.path, protocol: e.protocol, platform: p.id })))
  const native = all.filter((e) => supports.includes(e.platform) && (!subset || subset.includes(e.protocol)))
  const target = native.find((e) => TEXT.includes(e.protocol))?.protocol
  const out: TypeEndpoint[] = native.map((e) => ({ ...e, native: true, grade: 'native' }))
  for (const e of all) {
    if (native.includes(e) || !target || !TEXT.includes(e.protocol)) continue
    const twoHop = !supports.includes(e.platform) && [platformOf(e.protocol), platformOf(target)].sort().join() === 'anthropic,gemini'
    out.push({ ...e, native: false, grade: supports.includes(e.platform) ? 'good' : twoHop ? 'discouraged' : 'fair', via: twoHop ? [e.protocol, 'openai.chat', target] : [e.protocol, target] })
  }
  const served = new Set(out.map((e) => e.protocol))
  for (const e of all) if (!served.has(e.protocol) && served.has(COUNT_TOKENS_FAMILY[e.protocol])) out.push({ ...e, native: false, grade: 'local' })
  return out.sort((a, b) => Number(b.native) - Number(a.native) || a.platform.localeCompare(b.platform) || a.path.localeCompare(b.path))
}

interface Binding {
  /** The account's model list; empty = every model. */
  models?: string[]
  protocols?: string[] | null
  model_protocols?: Rule[]
}
type TypeView = { platforms: Array<{ id: string }>; endpoints: TypeEndpoint[] }

/** The protocols of a type, each once, with how the type serves it. */
function typeProtocols(at: TypeView): Array<TypeEndpoint & { how: ServedVia }> {
  const declared = new Set(at.platforms.map((p) => p.id))
  const seen = new Set<string>()
  return at.endpoints
    .filter((e) => !seen.has(e.protocol) && !!seen.add(e.protocol))
    .map((e) => ({ ...e, how: viaOf(e, declared) }))
}

/** The protocols an account can be bound to: no count_tokens, no local estimate. */
const bindable = (at: TypeView) => typeProtocols(at).filter((e) => !(e.protocol in COUNT_TOKENS_FAMILY) && e.grade !== 'local')

/**
 * The models an account serves on a protocol of its type: null when it
 * serves on it the models no rule names (it has some: no model list, or one
 * with a model without a rule), else the models with a rule it serves on it;
 * undefined when it serves none. A count_tokens protocol follows its family.
 */
function modelsOn(a: Binding, protocol: string, own: boolean): string[] | null | undefined {
  const p = COUNT_TOKENS_FAMILY[protocol] ?? protocol
  const rules = a.model_protocols ?? []
  const named = new Map(rules.filter((r) => !r.models.includes(REST)).flatMap((r) => r.models.map((m): [string, string[]] => [m, r.protocols])))
  /** Whether a model with these rule protocols (undefined: no rule) is served on the protocol. */
  const serves = (listed?: string[]) => (listed ? listed.includes(p) : own) && (!own || !a.protocols || a.protocols.includes(p))
  const others = !a.models?.length || a.models.some((m) => !named.has(m))
  if (others && serves(rules.find((r) => r.models.includes(REST))?.protocols)) return null
  const models = [...named].filter(([m, listed]) => (!a.models?.length || a.models.includes(m)) && serves(listed)).map(([m]) => m)
  return models.length ? models.sort() : undefined
}

/** served_protocols of GET /accounts/:id: the protocols of the type the account serves now, with the models of each. */
export function servedProtocols(a: Binding, at: TypeView): Served[] {
  const out: Served[] = []
  for (const e of typeProtocols(at)) {
    const models = modelsOn(a, e.protocol, e.how !== 'converted')
    if (models !== undefined) out.push({ protocol: e.protocol, via: e.how, grade: e.grade, hops: e.via, models })
  }
  return out
}

/** served_protocols of GET /groups/:id: each protocol with the number of member accounts serving it. */
export function groupServedProtocols(members: Served[][]): Array<{ protocol: string; accounts: number }> {
  const counts = new Map<string, number>()
  for (const served of members) for (const s of served) counts.set(s.protocol, (counts.get(s.protocol) ?? 0) + 1)
  return [...counts].map(([protocol, accounts]) => ({ protocol, accounts })).sort((a, b) => a.protocol.localeCompare(b.protocol))
}

type FieldError = { field: string; code: string; message: string }

/** Field errors of `protocols` / `model_protocols` (CONTRACTS §110.6), for the account the request would leave. */
export function protocolFieldErrors(b: Binding, at: TypeView): FieldError[] {
  const all = bindable(at)
  const cross = new Set(all.filter((e) => e.how === 'converted').map((e) => e.protocol))
  const out: FieldError[] = []
  const bad = (field: string, code: string, message: string) => out.push({ field, code, message })
  /** Refuses a protocol an account cannot be bound to; ownOnly also one of another platform. */
  const check = (field: string, p: string, ownOnly: boolean) => {
    if (p in COUNT_TOKENS_FAMILY) bad(field, 'invalid', 'a count_tokens protocol is not listed: it follows the generation protocol of its family')
    else if (!all.some((e) => e.protocol === p)) bad(field, 'unsupported', 'the account type does not serve this protocol')
    else if (ownOnly && cross.has(p)) bad(field, 'cross_platform', 'a protocol of another platform is enabled per model, in model_protocols')
  }
  ;(b.protocols ?? []).forEach((p, i) => check(`protocols[${i}]`, p, true))
  const rules = b.model_protocols ?? []
  const owner = new Map<string, number>()
  rules.forEach((r, i) => {
    const field = `model_protocols[${i}]`
    const models: string[] = []
    ;(r.models ?? []).forEach((m, j) => {
      if (models.includes(m)) return
      if (owner.has(m) && owner.get(m) !== i) return bad(`${field}.models[${j}]`, 'duplicate', 'the model is already in another entry')
      owner.set(m, i)
      models.push(m)
    })
    if (!r.models?.length) bad(`${field}.models`, 'required', 'choose at least one model')
    else if (models.length > 1 && models.includes(REST)) bad(`${field}.models`, 'invalid', '* (the other models) must be alone in its entry')
    else if (models.length > 500) bad(`${field}.models`, 'too_many', 'at most 500 models per entry')
    if (!r.protocols?.length) bad(`${field}.protocols`, 'required', 'choose at least one protocol')
    ;(r.protocols ?? []).forEach((p, j) => check(`${field}.protocols[${j}]`, p, false))
  })
  if (rules.length > 50) bad('model_protocols', 'too_many', 'at most 50 entries')
  if (!out.length && b.protocols && !b.protocols.length && !rules.some((r) => r.protocols?.some((p) => cross.has(p)))) {
    bad('protocols', 'no_protocol', 'the account serves no protocol: enable one of its own, or a conversion for a model')
  }
  return out
}
