import { apiBase, isApiError, listAll, pluginApiBase, requestRaw, useList, type ApiClient, type ListResult, type PageInfo, type PluginHost, type Query } from '@sub2api/host'
import type {
  Auditor,
  AuditorDraft,
  Binding,
  BindingsView,
  Block,
  Channel,
  ChannelTestResult,
  CategoryStats,
  CoreUser,
  Costs,
  Defaults,
  EventInput,
  Flow,
  FlowVersionDetail,
  FlowVersionItem,
  GroupConflict,
  Group,
  ImportResult,
  JevQuestions,
  ModEvent,
  ModEventDetail,
  PerformanceResult,
  PluginSettings,
  ReviewStatus,
  SimulateBody,
  SourceFilter,
  Spec,
  StatsQuery,
  Status,
  TestResult,
  TrendResult,
  TryResult,
  UserDetail,
  Watch,
  WatchChange,
  WatchEvent,
  WatchItem,
  WatchListMeta
} from './types'

// Plugin-scoped host handed to register(); shared by all moderation components.
// The API calls (CONTRACTS §72.11) live in this file, their types in types.ts.
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useModHost(): PluginHost {
  if (!host) throw new Error('moderation UI used before register(host)')
  return host
}

/** Manifest userPermission (host.can prefixes it with plugin.moderation:). */
export function canManage(): boolean {
  return useModHost().can('moderation:manage')
}

/**
 * May edit the plugin settings (the settings tab of this page, §72.15):
 * core plugin:manage, or moderation:manage (manifest ui.settings.permission,
 * CONTRACTS §75.1).
 */
export function canManageSettings(): boolean {
  const h = useModHost()
  return h.permissions.superuser() || h.permissions.has('plugin:manage') || canManage()
}

// ------------------------------------------------------------------ API

const api = () => useModHost().pluginApi

// ---- request body size: the core forwards at most 1 MiB per plugin request
// (server internal/plugin/routes MaxBodyBytes) and answers 413
// payload_too_large past it. Images may take 900 KiB of data URL
// (ImageInput.vue), so the text and a draft share what is left.

export const MAX_PLUGIN_BODY_BYTES = 1 << 20
/** Bytes of a JSON body as sent (UTF-8). */
export const jsonBytes = (body: unknown): number => new TextEncoder().encode(JSON.stringify(body) ?? '').length

/** True when the error is the core's 413 for a body over MAX_PLUGIN_BODY_BYTES. */
export const isBodyTooLarge = (e: unknown): boolean => isApiError(e) && (e.status === 413 || e.code === 'payload_too_large')

/** The message of a body too large: its size against the cap, in KiB. */
export function bodyTooLargeText(bytes?: number): string {
  const h = useModHost()
  const max = MAX_PLUGIN_BODY_BYTES >> 10
  return bytes ? h.t('bodyTooLarge', { kb: Math.ceil(bytes / 1024), max }) : h.t('bodyTooLargeServer', { max })
}

/** POST with the size checked first: a body the core would refuse fails here with bodyTooLargeText. */
async function postChecked<T>(path: string, body: object): Promise<T> {
  const n = jsonBytes(body)
  if (n > MAX_PLUGIN_BODY_BYTES) throw new Error(bodyTooLargeText(n))
  return api().post<T>(path, body)
}

/** List paths of useList (records and watch list). */
export const EVENTS_PATH = '/events'
export const WATCHLIST_PATH = '/watchlist'

/** Paged records (GET /events) for a filter query. */
export const useEventList = (query: Query) => useList<ModEvent>(api(), EVENTS_PATH, query)

export const fetchEvent = (id: number) => api().get<ModEventDetail>(`${EVENTS_PATH}/${id}`)
export const fetchEventInput = (id: number) => api().get<EventInput>(`${EVENTS_PATH}/${id}/input`)
export const deleteEvent = (id: number) => api().del(`${EVENTS_PATH}/${id}`)
/** What POST /events/review answers: the records updated and, of those, the ones whose cached verdict was brought in line (0.8.0). */
export interface ReviewResult {
  updated: number
  cache_synced?: number
}
/** Only records whose result is hit or block are updated (`updated` counts them). */
export const reviewEvents = (ids: number[], status: ReviewStatus) => api().post<ReviewResult>(`${EVENTS_PATH}/review`, { ids, status })
/** The toast of a review: the records updated (else `asked`), and the cached verdicts synced when there are any. */
export function reviewDoneText(r: ReviewResult | null | undefined, asked: number): string {
  const h = useModHost()
  const done = h.t('review.done', { n: r?.updated ?? asked })
  return r?.cache_synced ? done + h.t('clauseSep') + h.t('review.cacheSynced', { n: r.cache_synced }) : done
}

/** Empties the verdict cache of every node (POST /verdict-cache/clear, moderation:manage); answers the new cache epoch. */
export const clearVerdictCache = () => api().post<{ epoch: number }>('/verdict-cache/clear', {})

export async function fetchBlocks(): Promise<Block[]> {
  const r = await api().get<Block[] | null>('/blocks')
  return Array.isArray(r) ? r : []
}
export const createBlock = (body: { user_id: number; reason?: string; duration_hours?: number }) => api().post('/blocks', body)
export const deleteBlock = (userId: number) => api().del(`/blocks/${userId}`)

export const createWatch = (body: WatchChange & { user_id: number }) => api().post<Watch>(WATCHLIST_PATH, body)
export const updateWatch = (userId: number, body: WatchChange) => api().patch<Watch>(`${WATCHLIST_PATH}/${userId}`, body)
/** link: the reason of a release, written to the history. */
export const deleteWatch = (userId: number, link?: { reason?: string }) => api().del(`${WATCHLIST_PATH}/${userId}`, link)
export const markWatchSeen = (userId: number) => api().post(`${WATCHLIST_PATH}/${userId}/seen`, {})
export const fetchWatchEvents = (userId: number) => api().get<WatchEvent[]>(`${WATCHLIST_PATH}/${userId}/events`)

export const fetchChannels = () => api().get<Channel[]>('/channels')
export const fetchChannel = (id: number) => api().get<Channel>(`/channels/${id}`)
/** PUT / POST body of a channel; the key and proxy are kept unless the caller sets them. */
export function channelBody(c: Pick<Channel, 'type' | 'protocol' | 'name' | 'base_url' | 'model' | 'timeout_ms' | 'enabled'> & { billing_model?: string }): Record<string, unknown> {
  const body: Record<string, unknown> = { type: c.type, name: c.name, base_url: c.base_url, model: c.model, billing_model: c.billing_model || '', timeout_ms: c.timeout_ms, enabled: c.enabled }
  if (c.type === 'llm') body.protocol = c.protocol
  return body
}
export const saveChannel = (id: number | null, body: Record<string, unknown>) =>
  id ? api().put<Channel>(`/channels/${id}`, body) : api().post<Channel>('/channels', body)
export const deleteChannel = (id: number) => api().del(`/channels/${id}`)
export const testChannel = (id: number) => api().post<ChannelTestResult>(`/channels/${id}/test`, {})

export const fetchAuditors = () => api().get<Auditor[]>('/auditors')
export const fetchAuditor = (id: number) => api().get<Auditor>(`/auditors/${id}`)
export const saveAuditor = (id: number | null, body: AuditorDraft) =>
  id ? api().put<Auditor>(`/auditors/${id}`, body) : api().post<Auditor>('/auditors', body)
export const deleteAuditor = (id: number) => api().del(`/auditors/${id}`)
/** Tries a stored auditor (id), optionally with unsaved edits, or an unsaved one (id null + draft). */
export const tryAuditor = (id: number | null, text: string, images: string[], draft?: AuditorDraft) =>
  postChecked<TryResult>(id ? `/auditors/${id}/try` : '/auditors/try', { text, images, draft })
export const fetchDefaults = () => api().get<Defaults>('/jev/default-questions')

/** A question table as sent: thresholds edited as text become numbers. */
export const jevPayload = (q: JevQuestions): JevQuestions => ({ preamble: q.preamble, items: q.items.map((x) => ({ ...x, threshold: Number(x.threshold) })) })

export const fetchFlows = () => api().get<Flow[]>('/flows')
export const fetchFlow = (id: number) => api().get<Flow>(`/flows/${id}`)
export const saveFlow = (id: number | null, body: { name: string; draft: Spec }) =>
  id ? api().put<Flow>(`/flows/${id}`, body) : api().post<Flow>('/flows', body)
export const deleteFlow = (id: number) => api().del(`/flows/${id}`)
export const setFlowGroups = (id: number, groups: number[], force = false) => api().put<Flow>(`/flows/${id}/groups`, { groups, force })
/** Every binding (GET /bindings); put replaces a binding of another flow only with `replace`. */
export const fetchBindings = () => api().get<BindingsView>('/bindings')
export const putBinding = (b: Omit<Binding, 'kind'>, replace = false) => api().put<BindingsView>('/bindings', { ...b, replace })
export const deleteBinding = (b: Pick<Binding, 'group_id' | 'account_id'>) => api().del<BindingsView>(`/bindings/${b.group_id}/${b.account_id}`)
export const publishFlow = (id: number, expected: number) => api().post<Flow>(`/flows/${id}/publish`, { expected_version: expected })
export const setFlowEnabled = (id: number, enabled: boolean) => api().put<Flow>(`/flows/${id}/enabled`, { enabled })

export const runTest = (text: string, images: string[], flowId?: number) =>
  postChecked<TestResult>('/test', { text, images, flow_id: flowId || undefined })

/** Imports a new-api export (§72.12); preview only counts and validates. */
export const importConfig = (data: unknown, preview: boolean) => api().post<ImportResult>('/import', { data, preview })

// ---- users

export const fetchUserDetail = (userId: number) => api().get<UserDetail>(`/users/${userId}`)
export const fetchUserEvents = (userId: number, source?: SourceFilter | null, pageSize = 50) =>
  api().list<ModEvent>(EVENTS_PATH, { user_id: userId, result: 'suspicious', ...source, page: 1, page_size: pageSize })

/** The window of a user's recent sources (backend user_detail.go recentDays). */
export const RECENT_DAYS = 30
/** Start of the last RECENT_DAYS as RFC 3339 (the `from` of a source's records). */
export const recentFrom = (now = Date.now()) => new Date(now - RECENT_DAYS * 86_400_000).toISOString().replace(/\.\d+Z$/, 'Z')

export const fetchFlowVersions = (id: number) => api().get<{ current_version: number; items: FlowVersionItem[] }>(`/flows/${id}/versions`)
export const fetchFlowVersion = (id: number, version: number) => api().get<FlowVersionDetail>(`/flows/${id}/versions/${version}`)
export const rollbackFlow = (id: number, version: number, expected: number) =>
  api().post<Flow>(`/flows/${id}/rollback`, { version, expected_version: expected })
export const simulateFlow = (id: number, body: SimulateBody) => postChecked<TestResult>(`/flows/${id}/simulate`, body)

// ---- statistics; every call carries the caller's time zone so "today" is local.

/** tz_offset_minutes: minutes the browser is ahead of UTC. */
const withTz = <T extends object>(q?: T) => ({ ...q, tz_offset_minutes: -new Date().getTimezoneOffset() })

export const fetchStatus = () => api().get<Status>('/status', withTz())
export const fetchCosts = (range: string) => api().get<Costs>('/costs', withTz({ range }))
export const fetchTrend = (q: StatsQuery) => api().get<TrendResult>('/stats/trend', withTz(q))
export const fetchPerformance = (q: StatsQuery) => api().get<PerformanceResult>('/stats/performance', withTz(q))
export const fetchCategoryStats = () => api().get<CategoryStats>('/stats/categories', withTz())

// ---- watch list: the envelope carries header counts besides data and page

/** Body of GET /watchlist (ApiClient.list keeps only data and page). */
interface WatchListEnvelope<T> extends Partial<WatchListMeta> {
  data?: T[] | null
  page?: PageInfo | null
}

async function getWatchList<T>(query: Query): Promise<ListResult<T> & WatchListMeta> {
  const raw: unknown = await requestRaw('GET', pluginApiBase(useModHost().plugin.key, apiBase()) + WATCHLIST_PATH, { query })
  const env = (typeof raw === 'object' && raw !== null ? raw : {}) as WatchListEnvelope<T>
  const items = Array.isArray(env.data) ? env.data : []
  return {
    items,
    page: env.page ?? { page: 1, page_size: items.length, total: items.length },
    paged: !!env.page,
    counts: env.counts ?? { watching: 0, watching_with_new: 0, blocking: 0 }
  }
}

export const fetchWatchList = (query: Query) => getWatchList<WatchItem>(query)

/**
 * Paged watch list (GET /watchlist) for a filter query; onMeta gets the
 * envelope's counts of the latest load only (useList drops older responses too).
 */
export function useWatchList(query: Query, onMeta: (m: WatchListMeta) => void) {
  let seq = 0
  const client: ApiClient = {
    ...api(),
    async list<T>(_path: string, q?: Query): Promise<ListResult<T>> {
      const my = ++seq
      const r = await getWatchList<T>(q ?? {})
      if (my === seq) onMeta(r)
      return r
    }
  }
  return useList<WatchItem>(client, WATCHLIST_PATH, query)
}

// ---- core API (host.api): users, groups and this plugin's settings

export const fetchCoreUser = (id: number) => useModHost().api.get<CoreUser>(`/users/${id}`)
/** Users by id through the plugin (GET /users, host GetUsers): for moderators without user:read. */
export const fetchUsers = (ids: number[]) => api().get<CoreUser[]>('/users', { ids: ids.join(',') })
export const searchCoreUsers = (q: string) => useModHost().api.list<CoreUser>('/users', { q, page_size: 50 })

export const SETTINGS_MASK = '******'
const settingsApiPath = () => `/plugins/${encodeURIComponent(useModHost().plugin.key)}/settings`
export const fetchPluginSettings = () => useModHost().api.get<PluginSettings>(settingsApiPath())
export const savePluginSettings = (values: Record<string, unknown>) => useModHost().api.put<PluginSettings>(settingsApiPath(), { values })

/** A setting as stored, else its schema default; undefined when neither is known. */
export const settingValue = (s: PluginSettings | null | undefined, key: string): unknown => s?.values?.[key] ?? s?.schema?.properties?.[key]?.default

/** May read accounts of the console, all or the caller's own: a caller who may not is not sent for a 403. */
const canReadAccounts = () => ['account:read', 'account:own:read'].some((k) => useModHost().permissions.has(k))

/** Ids one lookup request takes (the core's cap, CONTRACTS §96.1). */
const LOOKUP_IDS = 100
/**
 * Names of the console's accounts by id, through the core's lookup (GET
 * /lookup/accounts: account:read, or the caller's own with
 * account:own:read). An account the caller may not see has no name, and none
 * has when the lookup is refused or fails.
 */
export async function fetchAccountNames(ids: readonly number[]): Promise<Map<number, string>> {
  const names = new Map<number, string>()
  if (!canReadAccounts()) return names
  for (let i = 0; i < ids.length; i += LOOKUP_IDS) {
    const found = await useModHost()
      .api.get<{ items?: Array<{ id: number; name: string }> } | null>('/lookup/accounts', { ids: ids.slice(i, i + LOOKUP_IDS).join(',') })
      .catch(() => null)
    for (const a of found?.items ?? []) names.set(a.id, a.name)
  }
  return names
}

/**
 * The groups an account of the console is in (GET /accounts/:id, the same
 * permissions); null when the account is not readable.
 */
export async function fetchAccountGroups(id: number): Promise<number[] | null> {
  if (!canReadAccounts()) return null
  const account = await useModHost()
    .api.get<{ group_ids?: number[] | null } | null>(`/accounts/${id}`)
    .catch(() => null)
  return account?.group_ids ?? null
}

/** Groups of the console (needs group:read); [] when not readable. */
export async function fetchGroups(): Promise<Group[]> {
  // listAll never returns a partial list, which would show the missing
  // groups as unbound: none at all instead.
  return listAll<Group>(useModHost().api, '/groups').catch(() => [])
}

// ------------------------------------------------------------------ helpers

/** Human-readable message of a failed call (ApiError is an Error), else `fallback`. A 413 gets its own text. */
export function errorMessage(e: unknown, fallback: string): string {
  if (isBodyTooLarge(e)) return bodyTooLargeText()
  return e instanceof Error && e.message ? e.message : fallback
}

/** Toasts a failed call: its message, else the plugin message `key`. */
export function toastError(e: unknown, key = 'actionFailed') {
  const h = useModHost()
  h.toast(errorMessage(e, h.t(key)), 'error')
}

/**
 * Asks to confirm a delete, then runs del: "deleted" is toasted and done
 * runs, or the failure is toasted as failText(e) (default: its message).
 */
export async function confirmDelete(message: string, del: () => Promise<unknown>, done: () => void, failText?: (e: unknown) => string) {
  const h = useModHost()
  if (!(await h.confirm({ message, danger: true, confirmText: h.t('delete') }))) return
  try {
    await del()
    h.toast(h.t('deleted'), 'success')
    done()
  } catch (e) {
    h.toast(failText ? failText(e) : errorMessage(e, h.t('actionFailed')), 'error')
  }
}

/** `details` members of the API errors the views read. */
interface ErrorDetails {
  /** 409 of DELETE /channels/:id: the auditors using the channel */
  auditors: Array<{ id: number; name: string }>
  /** 409 group_conflict of PUT /flows/:id/groups */
  conflicts: GroupConflict[]
  /** 400 missing_auditors of POST /flows/:id/rollback */
  auditor_ids: number[]
}

/** A list member of an ApiError's details; [] when the error carries none. */
export function errorDetails<K extends keyof ErrorDetails>(e: unknown, key: K): ErrorDetails[K] {
  const v: unknown = isApiError(e) ? e.details[key] : undefined
  return (Array.isArray(v) ? v : []) as ErrorDetails[K]
}
