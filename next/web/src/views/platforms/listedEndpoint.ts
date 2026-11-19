import type { PlatformEndpoint } from '@/api/types'

/**
 * An endpoint PlatformEndpointList shows: a catalog endpoint, or a protocol
 * the catalog does not describe, which has its id only.
 */
export type ListedEndpoint = PlatformEndpoint | { protocol: string; method?: undefined; path?: undefined; billing?: undefined }

/** The line of an endpoint: "POST /v1/messages", or the protocol id. */
export const endpointLine = (e: ListedEndpoint) => (e.method ? `${e.method} ${e.path}` : e.protocol)
