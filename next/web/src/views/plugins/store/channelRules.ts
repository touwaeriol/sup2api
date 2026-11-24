// The store channels' rules the console checks before the core does (CONTRACTS §136).

/** A branch is one git ref name segment: letters, digits, ".", "_" and "-", at most 64, no "..". */
export function validBranch(branch: string): boolean {
  const b = branch.trim()
  return /^[A-Za-z0-9._-]{1,64}$/.test(b) && !b.includes('..')
}
