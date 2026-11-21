// An expression back into the visual config it was generated from (design
// 2026-11-22 §0): parsed into a small tree, read as tiers and rules, then
// generated again. Only an expression the generator writes the same way
// (whitespace outside string literals aside) is visual; any other, written
// or edited by hand, is not. Pure TS, as priceExpr.
import {
  CMP_OPS,
  COND_VARS,
  NUMERIC_OPS,
  PRICE_KEYS,
  checkVisualConfig,
  isTimeVar,
  visualExpr,
  type CmpOp,
  type CondLeaf,
  type CondOp,
  type CondVar,
  type FieldCond,
  type MarkupRule,
  type PriceKey,
  type RuleCond,
  type TierCond,
  type VisualConfig,
  type VisualTier
} from './priceExpr'

// ------------------------------------------------------------------ tokens

type Token = { k: 'num'; v: number } | { k: 'str'; v: string } | { k: 'id'; v: string } | { k: 'op'; v: string }

const GO_ESCAPES: Record<string, string> = { a: '\x07', b: '\b', f: '\f', n: '\n', r: '\r', t: '\t', v: '\v', '\\': '\\', '"': '"' }
const HEX_WIDTH: Record<string, number> = { x: 2, u: 4, U: 8 }

/** The text of a Go string literal (its body between the quotes). */
function unquote(body: string): string {
  return body.replace(/\\(?:([abfnrtv\\"])|([xuU])([0-9a-fA-F]+))/g, (m, esc: string, kind: string, digits: string) => {
    if (esc) return GO_ESCAPES[esc]
    if (digits.length < HEX_WIDTH[kind]) throw new SyntaxError(m)
    return String.fromCodePoint(parseInt(digits.slice(0, HEX_WIDTH[kind]), 16)) + digits.slice(HEX_WIDTH[kind])
  })
}

const TOKEN = /\s*(?:("(?:[^"\\]|\\.)*")|(\d+(?:\.\d+)?)|([A-Za-z_][A-Za-z0-9_]*)|(&&|\|\||==|!=|<=|>=|\?\?|[<>!(),?:*+-]))/y

function tokenize(src: string): Token[] {
  const out: Token[] = []
  const end = src.trimEnd().length
  TOKEN.lastIndex = 0
  while (TOKEN.lastIndex < end) {
    const m = TOKEN.exec(src)
    if (!m) throw new SyntaxError(`at ${TOKEN.lastIndex}`)
    const [, str, n, id, op] = m
    if (str) out.push({ k: 'str', v: unquote(str.slice(1, -1)) })
    else if (n) out.push({ k: 'num', v: Number(n) })
    else if (id) out.push({ k: 'id', v: id })
    else out.push({ k: 'op', v: op })
  }
  return out
}

// ------------------------------------------------------------------ tree

type Node =
  | { t: 'num'; v: number }
  | { t: 'str'; v: string }
  | { t: 'id'; v: string }
  | { t: 'call'; f: string; args: Node[] }
  | { t: 'paren'; e: Node }
  | { t: 'not'; e: Node }
  | { t: 'bin'; op: string; l: Node; r: Node }
  | { t: 'tern'; c: Node; a: Node; b: Node }

/** Binding power of the binary operators the generator writes. */
const BP: Record<string, number> = { '||': 1, '&&': 2, '==': 3, '!=': 3, '<': 3, '<=': 3, '>': 3, '>=': 3, '??': 4, '+': 5, '*': 6 }

function parse(src: string): Node {
  const toks = tokenize(src)
  let i = 0
  const peek = (v: string) => toks[i]?.k === 'op' && toks[i].v === v
  const expect = (v: string) => {
    if (!peek(v)) throw new SyntaxError(`want ${v}`)
    i++
  }
  function ternary(): Node {
    const c = binary(0)
    if (!peek('?')) return c
    i++
    const a = ternary()
    expect(':')
    return { t: 'tern', c, a, b: ternary() }
  }
  function binary(min: number): Node {
    let l = unary()
    for (let tok = toks[i]; tok?.k === 'op' && BP[tok.v] >= min; tok = toks[i]) {
      i++
      l = { t: 'bin', op: tok.v, l, r: binary(BP[tok.v] + 1) }
    }
    return l
  }
  function unary(): Node {
    const tok = toks[i++]
    if (!tok) throw new SyntaxError('unexpected end')
    if (tok.k === 'num' || tok.k === 'str') return { t: tok.k, v: tok.v } as Node
    if (tok.k === 'id') return peek('(') ? { t: 'call', f: tok.v, args: callArgs() } : { t: 'id', v: tok.v }
    if (tok.v === '!') return { t: 'not', e: unary() }
    if (tok.v === '-' && toks[i]?.k === 'num') return { t: 'num', v: -(toks[i++] as { v: number }).v }
    if (tok.v !== '(') throw new SyntaxError(`unexpected ${tok.v}`)
    const e = ternary()
    expect(')')
    return { t: 'paren', e }
  }
  function callArgs(): Node[] {
    expect('(')
    const args = [ternary()]
    while (peek(',')) {
      i++
      args.push(ternary())
    }
    expect(')')
    return args
  }
  const root = ternary()
  if (i < toks.length) throw new SyntaxError('trailing input')
  return root
}

// ------------------------------------------------------------------ tree → config

function fail(): never {
  throw new SyntaxError('not visual')
}
const unparen = (n: Node): Node => (n.t === 'paren' ? unparen(n.e) : n)
const numOf = (n: Node): number => (n.t === 'num' ? n.v : fail())
const strOf = (n: Node): string => (n.t === 'str' ? n.v : fail())
const callOf = (n: Node, f: string, arity: number) => (n.t === 'call' && n.f === f && n.args.length === arity ? n.args : fail())

/** The operands of a chain of one operator written without parentheses (`a && b && c`). */
const operands = (n: Node, op: string): Node[] => (n.t === 'bin' && n.op === op ? [...operands(n.l, op), ...operands(n.r, op)] : [n])

function tierOf(n: Node): VisualTier {
  const [name, body] = callOf(n, 'tier', 2)
  if (body.t === 'call') return { name: strOf(name), unit: 'request', price: numOf(callOf(body, 'flat', 1)[0]) }
  const prices = operands(body, '+').map(term => {
    if (term.t !== 'bin' || term.op !== '*' || term.l.t !== 'id' || !PRICE_KEYS.includes(term.l.v as PriceKey)) fail()
    return [term.l.v, numOf(term.r)]
  })
  return { name: strOf(name), unit: 'token', ...Object.fromEntries(prices) }
}

function leafOf(n: Extract<Node, { t: 'bin' }>): CondLeaf {
  if (!CMP_OPS.includes(n.op as CmpOp)) fail()
  const op = n.op as CmpOp
  const value = numOf(n.r)
  if (n.l.t === 'id' && !isTimeVar(n.l.v) && COND_VARS.includes(n.l.v as CondVar)) return { var: n.l.v as CondVar, op, value }
  if (n.l.t !== 'call' || !isTimeVar(n.l.f)) fail()
  return { var: n.l.f, op, value, tz: strOf(callOf(n.l, n.l.f, 1)[0]) }
}

function whenOf(n: Node): TierCond {
  const e = unparen(n)
  if (e.t === 'not') return { not: whenOf(e.e) }
  if (e.t !== 'bin') fail()
  if (e.op === '&&') return { and: operands(e, '&&').map(whenOf) }
  if (e.op === '||') return { or: operands(e, '||').map(whenOf) }
  return leafOf(e)
}

/** The tiers of `c1 ? T1 : (c2 ? T2 : T3)`, in order. */
function chainOf(n: Node): VisualTier[] {
  const e = unparen(n)
  return e.t === 'tern' ? [{ ...tierOf(e.a), when: whenOf(e.c) }, ...chainOf(e.b)] : [tierOf(e)]
}

const READS: Record<string, FieldCond['source']> = { param: 'param', header: 'header', u: 'usage' }
const OPS: Record<string, CondOp> = { '==': 'eq', '!=': 'ne', '>': 'gt', '>=': 'gte', '<': 'lt', '<=': 'lte' }
/** What `!= x` compares a source with when it tests presence. */
const ABSENT: Record<FieldCond['source'], (n: Node) => boolean> = {
  param: n => n.t === 'id' && n.v === 'nil',
  header: n => n.t === 'str' && n.v === '',
  usage: n => n.t === 'num' && n.v === 0
}

function readOf(n: Node): Pick<FieldCond, 'source' | 'key'> {
  if (n.t !== 'call' || !READS[n.f]) fail()
  return { source: READS[n.f], key: strOf(callOf(n, n.f, 1)[0]) }
}

function literalOf(n: Node): FieldCond['value'] {
  if (n.t === 'num' || n.t === 'str') return n.v
  return n.t === 'id' && (n.v === 'true' || n.v === 'false') ? n.v === 'true' : fail()
}

function fieldCondOf(n: Node): FieldCond {
  if (n.t === 'call' && n.f === 'has') {
    const [read, sub] = callOf(n, 'has', 2)
    return { ...readOf(read), op: 'contains', value: strOf(sub) }
  }
  if (n.t !== 'bin' || !OPS[n.op]) fail()
  const op = OPS[n.op]
  // A parameter compared as a number reads 0 when absent: `(param("k") ?? 0) > n`.
  const lhs = n.l.t === 'paren' && n.l.e.t === 'bin' && n.l.e.op === '??' && NUMERIC_OPS.includes(op) ? n.l.e.l : n.l
  const read = readOf(lhs)
  if (op === 'ne' && ABSENT[read.source](n.r)) return { ...read, op: 'exists' }
  return { ...read, op, value: literalOf(n.r) }
}

/** `hour("tz") >= a` → [tz, a]; null for anything else. */
function hourBound(n: Node, op: string): [string, number] | null {
  if (n.t !== 'bin' || n.op !== op || n.l.t !== 'call' || n.l.f !== 'hour' || n.r.t !== 'num') return null
  return [strOf(callOf(n.l, 'hour', 1)[0]), n.r.v]
}

/** The time window of `hour >= a && hour < b` (two operands) or `(hour >= a || hour < b)`; null for anything else. */
function windowOf(from: Node, to: Node | undefined): RuleCond | null {
  const [a, b] = [hourBound(from, '>='), to && hourBound(to, '<')]
  return a && b && a[0] === b[0] ? { source: 'time', tz: a[0], from_hour: a[1], to_hour: b[1] } : null
}

function condsOf(n: Node): RuleCond[] {
  const terms = operands(n, '&&')
  const out: RuleCond[] = []
  for (let i = 0; i < terms.length; i++) {
    const term = terms[i]
    const inner = term.t === 'paren' && term.e.t === 'bin' && term.e.op === '||' ? term.e : null
    const range = inner ? windowOf(inner.l, inner.r) : windowOf(term, terms[i + 1])
    if (range && !inner) i++
    out.push(range ?? fieldCondOf(term))
  }
  return out
}

function ruleOf(n: Node): MarkupRule {
  if (n.t !== 'paren' || n.e.t !== 'tern' || numOf(n.e.b) !== 1) fail()
  return { name: '', multiplier: numOf(n.e.a), conds: condsOf(n.e.c) }
}

function configOf(root: Node): VisualConfig {
  const factors = operands(root, '*')
  if (factors.length === 1) return { tiers: chainOf(root), rules: [] }
  const [base, ...rules] = factors
  return { tiers: chainOf(base.t === 'paren' ? base.e : fail()), rules: rules.map(ruleOf) }
}

// ------------------------------------------------------------------ the round trip

/** An expression without the whitespace outside its string literals. */
const squash = (src: string) => src.replace(/("(?:[^"\\]|\\.)*")|\s+/g, (_, str: string | undefined) => str ?? '')

/** Whether two expressions are the same but for whitespace outside string literals. */
export const sameExpr = (a: string, b: string) => squash(a) === squash(b)

/**
 * The visual config an expression was generated from: parsed, read back and
 * generated again, it must be the same expression but for whitespace outside
 * string literals, and a config the editor saves. null for any other
 * expression. Rule names are not in an expression: a rule comes back unnamed.
 */
export function visualOf(src: string): VisualConfig | null {
  let cfg: VisualConfig
  try {
    cfg = configOf(parse(src))
  } catch {
    return null
  }
  return sameExpr(visualExpr(cfg), src) && !checkVisualConfig(cfg).length ? cfg : null
}
