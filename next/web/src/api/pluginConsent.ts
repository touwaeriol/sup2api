// The bodies and answers of a plugin consent: POST …/consent on a registered
// version, and an install carrying the consent (upload, install-from-market;
// CONTRACTS §5.7 "先授权再安装").
import type { NewPermissionRoles, PluginReview, PluginRoleGrant } from './types'

/** The body of POST …/consent. */
export interface ConsentRequest {
  grants: Array<{ permission: string; scope?: unknown }>
  denied: string[]
  new_permission_roles: NewPermissionRoles
}

/** The answer of POST …/consent. */
export interface ConsentResult {
  /** The grants this consent held back for a super admin's review (D2). */
  withheld?: PluginRoleGrant[]
}

/** The consent an install carries: what was approved, for the package reviewed (409 package_changed otherwise). */
export interface InstallConsent extends ConsentRequest {
  package_sha256: string
}

/** The answer of an install carrying a consent. */
export interface ConsentedInstall {
  review: PluginReview
  consent: ConsentResult
}
