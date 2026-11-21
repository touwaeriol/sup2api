import { describe, expect, it } from 'vitest'
import { formatExpr, highlight, offsetOf, tokenize } from './exprSyntax'

describe('exprSyntax', () => {
  it('formats a tier chain with rules one factor per line', () => {
    const src =
      '(len <= 200000 ? tier("standard", p*3 + c*15) : tier("long_context", p*6 + c*22.5)) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1) * (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1)'
    expect(formatExpr(src)).toBe(
      [
        '(len <= 200000 ? tier("standard", p*3 + c*15)',
        '  : tier("long_context", p*6 + c*22.5))',
        '* (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)',
        '* (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1)'
      ].join('\n')
    )
  })
  it('formats a bare tier chain and is idempotent', () => {
    const src = 'len <= 32000 ? tier("a", p*1) : len <= 128000 ? tier("b", p*2) : tier("c", p*3)'
    const f = formatExpr(src)
    expect(f).toBe('len <= 32000 ? tier("a", p*1)\n  : len <= 128000 ? tier("b", p*2)\n  : tier("c", p*3)')
    expect(formatExpr(f)).toBe(f)
  })
  it('leaves unbalanced input alone', () => {
    expect(formatExpr('tier("a", p*3')).toBe('tier("a", p*3')
  })
  it('classifies tokens and underlines the error', () => {
    const kinds = tokenize('prams("x") + p').filter((t) => t.kind !== 'ws').map((t) => t.kind)
    expect(kinds).toEqual(['fn', 'punc', 'str', 'punc', 'op', 'var'])
    expect(highlight('a < b', 0)).toContain('decoration-wavy')
    expect(highlight('x\n')).toMatch(/ $/)
  })
  it('maps line and column to an offset', () => {
    expect(offsetOf('ab\ncd', 2, 2)).toBe(4)
    expect(offsetOf('ab', 9, 9)).toBe(2)
  })
})
