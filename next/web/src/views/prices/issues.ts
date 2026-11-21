import { lt } from '@/i18n'

/** Validation issue: a string, or {code, message: string | {en, zh}, detail}. */
export type Issue = string | { code?: string; message?: unknown; detail?: unknown; [k: string]: unknown }

export function issueText(x: Issue): string {
  if (typeof x === 'string') return x
  if (x && typeof x === 'object') {
    const m = lt(x.message)
    if (m) return m
    if (x.code) return String(x.code)
  }
  return JSON.stringify(x)
}

/** The 1-based line and column an issue points at (column 1 when it gives none); null when it has no line. */
export function issuePos(x: Issue): { line: number; column: number } | null {
  return typeof x !== 'string' && typeof x?.line === 'number' ? { line: x.line, column: typeof x.column === 'number' && x.column > 0 ? x.column : 1 } : null
}
