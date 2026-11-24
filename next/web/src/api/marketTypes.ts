// The plugin store's types: its stores (/market/stores), their channels
// (/market/sources) and listings (GET /market/plugins); CONTRACTS §11.1, §136, §137.
import type { LText, Trust } from './types'

/**
 * A configured plugin store (GET /market/stores; CONTRACTS §136): a GitHub
 * repository whose branches are its channels. The official store is seeded,
 * cannot be deleted and keeps its repository; the market shows only the name.
 */
export interface MarketStore {
  id: number
  name: string
  /** GitHub repository, owner/name: shown on the store settings page only. */
  repo: string
  official: boolean
}

/** POST /market/stores: a third-party store and the branch of its default channel. */
export interface MarketStoreCreate {
  name: string
  repo: string
  branch: string
}

/**
 * A channel of a store (GET /market/sources?store_id=): a branch of the
 * store repository, whose index CI publishes to index-<branch>. Each store
 * has one default channel, which cannot be disabled or deleted.
 */
export interface MarketSource {
  id: number
  store_id: number
  /** Display name. */
  name: string
  branch: string
  /** The channel's index URL, derived from the store base and the branch (not shown). */
  url: string
  enabled: boolean
  is_default: boolean
}

/** POST /market/stores/:id/sources: a new channel of the store. */
export interface MarketSourceCreate {
  name: string
  branch: string
}

/** PATCH /market/sources/:id: what a channel's edit changes. */
export type MarketSourcePatch = Partial<Pick<MarketSource, 'name' | 'enabled' | 'is_default'>>

/** GET /market/plugins: the listing of a channel and the running core's version. */
export interface MarketListing {
  data: MarketPlugin[]
  host_version: string
  /** When the index was generated and fetched; rendered only when the core sends them. */
  generated_at?: string
  fetched_at?: string
}

/** Deprecation of a market plugin or version: still installable, never the latest (CONTRACTS §136). */
export interface MarketDeprecation {
  reason: LText
}

/** Publisher status of a market plugin: only an active publisher's plugins install (CONTRACTS §136). */
export type MarketPublisherStatus = 'active' | 'revoked' | 'missing'

/** A plugin of GET /market/plugins (CONTRACTS §11.1, §136). */
export interface MarketPlugin {
  source_id: number
  source_name: string
  /** The plugin id (as PluginSummary.key). */
  id: string
  /** The manifest key, unique per publisher. */
  key: string
  /** manifest.DisplayID of id. */
  display_id?: string
  name: LText
  description?: LText
  /** Publisher id of the index (the manifest's publisher). */
  publisher: string
  publisher_name?: LText
  publisher_homepage?: string
  /** Trust level the index gives the publisher; '' when it does not list it. */
  trust: Trust
  publisher_status: MarketPublisherStatus
  /** The publisher's GitHub logins (the registry's publishers/<id>.json); rendered only when the core sends them. */
  publisher_github?: string[]
  /** GitHub repository (owner/name) the packages are released from. */
  repo?: string
  /** Source code URL. */
  source_url?: string
  builtin: boolean
  deprecated?: MarketDeprecation
  /** Newest compatible version that is not deprecated ('' when none). */
  latest_version: string
  versions: MarketVersion[]
  installed_version?: string
  installed_status?: string
  update_available: boolean
}

/** A version of a market plugin (CONTRACTS §11.1, §14.3). */
export interface MarketVersion {
  version: string
  size?: number
  host_compat?: string
  /** host_compat satisfied by the running core version. */
  compatible: boolean
  /** GitHub release page of the version (release notes). */
  release_url?: string
  /** The package's sha256 in the index; rendered only when the core sends it. */
  sha256?: string
  deprecated?: MarketDeprecation
}
