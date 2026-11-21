// Tokenizer, highlighter and formatter for price expressions (the expr-lang
// subset the core accepts). Used by the expression editor only: the server
// is the judge of what compiles.

import { PRICE_KEYS } from './priceExpr'

export type TokKind = 'ws' | 'str' | 'num' | 'var' | 'fn' | 'kw' | 'ident' | 'op' | 'punc' | 'bad'
export interface Tok {
  kind: TokKind
  text: string
  /** 0-based offset in the source. */
  at: number
}

const VARS = new Set<string>([...PRICE_KEYS, 'ai', 'ao', 'len'])
const KEYWORDS = new Set(['nil', 'true', 'false', 'and', 'or', 'not', 'in'])

const RE =
  /(\s+)|("(?:[^"\\\n]|\\.)*"?|'(?:[^'\\\n]|\\.)*'?)|(\d+(?:_\d+)*(?:\.\d+)?(?:[eE][+-]?\d+)?)|([A-Za-z_$][A-Za-z0-9_]*)|(\?\?|&&|\|\||==|!=|<=|>=|\*\*|[-+*/%<>!?:])|([(),.[\]{}])|(.)/gy

export function tokenize(src: string): Tok[] {
  const out: Tok[] = []
  RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = RE.exec(src))) {
    const at = m.index
    const [text, ws, str, num, id, op, punc] = m
    let kind: TokKind = 'bad'
    if (ws) kind = 'ws'
    else if (str) kind = 'str'
    else if (num) kind = 'num'
    else if (id) kind = VARS.has(id) ? 'var' : KEYWORDS.has(id) ? 'kw' : /^\s*\(/.test(src.slice(at + id.length)) ? 'fn' : 'ident'
    else if (op) kind = 'op'
    else if (punc) kind = 'punc'
    out.push({ kind, text, at })
  }
  return out
}

const CLASS: Partial<Record<TokKind, string>> = {
  str: 'text-success-700 dark:text-success-400',
  num: 'text-warning-700 dark:text-warning-400',
  var: 'text-info-700 dark:text-info-400',
  fn: 'text-accent-700 dark:text-accent-400',
  kw: 'text-danger-600 dark:text-danger-400',
  op: 'text-fg-muted',
  ident: 'text-danger-600 underline decoration-wavy decoration-danger-500',
  bad: 'text-danger-600 underline decoration-wavy decoration-danger-500'
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/**
 * HTML for the highlight layer. `errorAt` (0-based offset) underlines the
 * token there. A trailing newline gets a space so the layer keeps the
 * textarea's height. `placeholders` shows unknown names (费用, a, x in the
 * guide's signatures) as plain placeholders instead of errors.
 */
export function highlight(src: string, errorAt = -1, placeholders = false): string {
  let html = ''
  for (const t of tokenize(src)) {
    if (placeholders && (t.kind === 'ident' || t.kind === 'bad')) {
      html += `<span class="italic text-fg-muted">${esc(t.text)}</span>`
      continue
    }
    const hit = errorAt >= t.at && errorAt < t.at + Math.max(t.text.length, 1) && t.kind !== 'ws'
    const cls = [CLASS[t.kind], hit ? 'underline decoration-wavy decoration-danger-500 bg-danger-500/10' : ''].filter(Boolean).join(' ')
    html += cls ? `<span class="${cls}">${esc(t.text)}</span>` : esc(t.text)
  }
  return src.endsWith('\n') ? html + ' ' : html
}

/** 0-based offset of a 1-based line and column, clamped to the source. */
export function offsetOf(src: string, line: number, column: number): number {
  const lines = src.split('\n')
  let at = 0
  for (let i = 0; i < Math.min(line - 1, lines.length - 1); i++) at += lines[i].length + 1
  return Math.min(at + Math.max(column - 1, 0), src.length)
}

/**
 * Lays an expression out one factor per line: the top-level products
 * `(A) * (B)` break before each `*`, and a ternary chain that picks between
 * tiers breaks before each `:`, down the groups it nests in its last branch
 * (`a ? A : (b ? B : C)`, as tiers are generated from the third). Surcharge
 * rules `(cond ? 2 : 1)` stay on one line. Whitespace inside strings is
 * untouched.
 */
export function formatExpr(src: string): string {
  const all = tokenize(src.trim())
  const toks: Tok[] = []
  const spaced: boolean[] = [] // whitespace before the token in the source
  for (const t of all) {
    if (t.kind === 'ws') {
      if (toks.length) spaced[toks.length] = true
      continue
    }
    toks.push(t)
  }
  if (!toks.length) return src
  // The group each token is in: the index of its opening bracket, -1 at the top.
  const group: number[] = []
  const open: number[] = []
  for (const [i, t] of toks.entries()) {
    if ((t.text === ')' || t.text === ']') && open.pop() === undefined) return src // unbalanced: leave it to the server's error
    group.push(open.length ? open[open.length - 1] : -1)
    if (t.text === '(' || t.text === '[') open.push(i)
  }
  if (open.length) return src

  const topProduct = (i: number) => toks[i].text === '*' && group[i] === -1 && toks[i - 1]?.text === ')' && toks[i + 1]?.text === '('
  // A product of groups at the top: break before each `*`.
  const product = toks.some((_, i) => topProduct(i))
  // The groups the tier chain runs in: the first group of a product, else the top; and the group each of its last branches opens.
  const chain = new Set([product && toks[0].text === '(' ? 0 : -1])
  // A ':' whose branch is a bare number closing the group is a rule's ": 1".
  const ruleColon = (i: number) => toks[i + 1]?.kind === 'num' && (toks[i + 2] === undefined || toks[i + 2].text === ')')

  let out = ''
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i]
    if (topProduct(i)) {
      out += '\n* '
      continue
    }
    if (t.text === ':' && t.kind === 'op' && chain.has(group[i]) && !ruleColon(i)) {
      if (toks[i + 1]?.text === '(') chain.add(i + 1)
      out += '\n  : '
      continue
    }
    out += (spaced[i] && !/[ \n]$/.test(out) ? ' ' : '') + t.text
  }
  return out
}
