// The channel rules the console checks before the core does (plugin/market validBranch; CONTRACTS §137.6).

/** A branch is one git ref name segment: letters, digits, ".", "_" and "-", at most 64, no "..", not starting with "." or ending in ".lock". */
export function validBranch(branch: string): boolean {
  const b = branch.trim()
  return /^[A-Za-z0-9._-]{1,64}$/.test(b) && !b.includes('..') && !b.startsWith('.') && !b.endsWith('.lock')
}
