// The checks every plugin's messages.ts takes, as one call in its spec:
//   describe('messages', () => checkMessages(messages, { sources: import.meta.glob(…) }))
import { expect, it } from 'vitest'

type Messages = { zh: Record<string, unknown>; en: Record<string, unknown> }

const keys = (o: unknown, prefix = ''): string[] =>
  o && typeof o === 'object' ? Object.entries(o).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k)) : [prefix]
const leaves = (o: unknown): string[] => (o && typeof o === 'object' ? Object.values(o).flatMap(leaves) : [String(o)])

/** What checkMessages refuses: keys of one language only, texts with a special character, unused keys. */
export function messageProblems(messages: Messages, opts: { sources: Record<string, string>; dynamic?: string[] }) {
  const zh = keys(messages.zh)
  const en = keys(messages.en)
  const sources = Object.values(opts.sources).join('\n')
  const dynamic = opts.dynamic ?? []
  return {
    onlyZh: zh.filter((k) => !en.includes(k)),
    onlyEn: en.filter((k) => !zh.includes(k)),
    special: [messages.zh, messages.en].flatMap((lang) => leaves(lang).filter((s) => /[@|{}]/.test(s.replace(/\{\w+\}/g, '')))),
    unused: en.filter((k) => !dynamic.some((d) => k.startsWith(d)) && !sources.includes(`'${k}'`))
  }
}

/**
 * Adds the tests: zh and en have the same keys; no text holds a vue-i18n
 * special character (@, |, a brace outside a {name} placeholder); every key
 * is used: quoted ('a.b') in one of `sources` (the raw text of the code
 * files, import.meta.glob with query '?raw'), or under a `dynamic` prefix
 * (keys looked up with a computed suffix, t(`tabs.${f}`)).
 */
export function checkMessages(messages: Messages, opts: { sources: Record<string, string>; dynamic?: string[] }) {
  const problems = () => messageProblems(messages, opts)
  it('zh and en have the same keys', () => {
    const { onlyZh, onlyEn } = problems()
    expect({ onlyZh, onlyEn }).toEqual({ onlyZh: [], onlyEn: [] })
  })

  it('keeps vue-i18n special characters out of plain text', () => {
    expect(problems().special).toEqual([])
  })

  it('has no key the code never uses', () => {
    expect(Object.keys(opts.sources)).not.toEqual([])
    expect(problems().unused).toEqual([])
  })
}
