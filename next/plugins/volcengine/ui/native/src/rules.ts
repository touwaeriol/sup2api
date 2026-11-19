import type { Rule } from '@sub2api/ui'

// The constraints the account fields share (the Go side, ValidateCredentials,
// is the check that counts; these give feedback while typing).

/** access_key, secret_key: at most 256 visible ASCII characters. */
export const ASSET_KEY: Rule = { max: 256, pattern: /^[!-~]*$/ }
/** asset_region: a region id such as cn-beijing. */
export const ASSET_REGION: Rule = { max: 64, pattern: /^[a-z0-9-]*$/ }
/** asset_base_url: an optional http(s) URL without query or fragment. */
export const OPTIONAL_URL: Rule = { pattern: /^(https?:\/\/[^\s?#]+)?$/ }
/** api_prefix, video_api_prefix: "/" or a path below one slash. */
export const PATH_PREFIX: Rule = { max: 128, pattern: /^(\/|\/[^/\s?#][^\s?#]*)?$/ }
/** video_endpoint, asset_endpoint: an http(s) URL, or a path below one slash. */
export const ENDPOINT: Rule = { max: 2048, pattern: /^((https?:\/\/[^\s?#]+)|(\/[^/\s?#][^\s?#]*|\/))?$/ }
