// Plugin network access (CONTRACTS §113): the administrator's domains every
// plugin may or may not reach through the egress tunnel. The server is the
// authority on entries; normalizeDomain mirrors its rules so the add dialog
// can point at the line that is wrong before anything is saved.

export type PluginEgressMode = 'allow_all' | 'block_all'

export interface PluginEgressPolicy {
  mode: PluginEgressMode
  blocked_domains: string[]
  allowed_domains: string[]
}

export const PLUGIN_EGRESS_PATH = '/settings/plugin-egress'

/** The list the mode uses: blocked domains when everything else is allowed, allowed domains when everything else is blocked. */
export function listField(mode: PluginEgressMode): 'blocked_domains' | 'allowed_domains' {
  return mode === 'block_all' ? 'allowed_domains' : 'blocked_domains'
}

const LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
const IPV6 = /^\[?[0-9a-f:.]+\]?$/

/**
 * The stored form of one entry, or null when it is not a domain name or IP
 * address: trimmed, lower-case, no trailing dot, punycode for an
 * internationalized name, an IPv6 literal in brackets. A scheme, path, port,
 * wildcard or blank is refused (the server does the same).
 */
export function normalizeDomain(input: string): string | null {
  const s = input.trim().toLowerCase().replace(/\.$/, '')
  if (!s) return null
  if (s.includes(':')) {
    if (!IPV6.test(s)) return null
    try {
      return new URL(`http://[${s.replace(/^\[|\]$/g, '')}]`).hostname
    } catch {
      return null
    }
  }
  if (/[\s/?#@*[\]\\%]/.test(s)) return null
  let host = s
  // Only an internationalized name goes through URL (for punycode): URL
  // would also rewrite names that look numeric ("123" -> "0.0.0.123").
  // eslint-disable-next-line no-control-regex
  if (!/^[\x00-\x7f]*$/.test(s)) {
    try {
      host = new URL(`http://${s}`).hostname
    } catch {
      return null
    }
  }
  if (host.length > 253) return null
  return host.split('.').every(l => LABEL.test(l)) ? host : null
}

/** One line the dialog could not take: its number (from 1) and text. */
export interface DomainLineError {
  line: number
  value: string
}

/**
 * Reads the lines of the add dialog: blank lines are skipped, each other
 * line must be a domain; duplicates (among the lines or already in the list)
 * are dropped. domains is what to add, in order.
 */
export function parseDomainLines(lines: string[], existing: readonly string[] = []): { domains: string[]; errors: DomainLineError[] } {
  const seen = new Set(existing)
  const domains: string[] = []
  const errors: DomainLineError[] = []
  lines.forEach((raw, i) => {
    if (!raw.trim()) return
    const d = normalizeDomain(raw)
    if (!d) {
      errors.push({ line: i + 1, value: raw.trim() })
      return
    }
    if (seen.has(d)) return
    seen.add(d)
    domains.push(d)
  })
  return { domains, errors }
}
