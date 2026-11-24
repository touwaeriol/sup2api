// The store and channel rules the console checks before the core does (plugin/market validBranch, validRepo; CONTRACTS §137.6).

/** A branch is one git ref name segment: letters, digits, ".", "_" and "-", at most 64, no "..", not starting with "." or ending in ".lock". */
export function validBranch(branch: string): boolean {
  const b = branch.trim()
  return /^[A-Za-z0-9._-]{1,64}$/.test(b) && !b.includes('..') && !b.startsWith('.') && !b.endsWith('.lock')
}

/** A GitHub repository, owner/name: the owner as GitHub allows it, the name not ".", ".." or ending in ".git". */
export function validRepo(repo: string): boolean {
  const m = /^([A-Za-z0-9][A-Za-z0-9-]{0,38})\/([A-Za-z0-9._-]{1,100})$/.exec(repo.trim())
  return !!m && m[2] !== '.' && m[2] !== '..' && !m[2].endsWith('.git')
}
