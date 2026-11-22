import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { api, type ApiError } from '@sub2api/host'
import type { PriceValidateResult } from '@/api/types'
import { latestOnly } from '@/composables/useLatestRequest'
import { errorMessage } from '@/utils/errors'
import type { Issue } from './issues'

export interface PricePayload {
  mode: string
  config: object
  expression: string
}

export interface PriceValidation {
  ok: boolean
  expression?: string
  errors: Issue[]
  warnings: Issue[]
  cost_per_million_input?: string | number
  samples?: number
  analysis?: PriceAnalysis
}

/** What a compiled expression reads (expr.Analysis). */
export interface PriceAnalysis {
  vars: string[]
  params: string[]
  headers: string[]
  facts: string[]
  tiers: string[]
  rules: string[]
  len_boundaries: number[]
  zones?: string[]
}

type ValidateAnswer = PriceValidateResult & { cost_per_million_input?: string; samples?: number; analysis?: PriceAnalysis }

/** Requests of which only the latest is answered (latestOnly): a newer one, or drop(), drops the answer of any still in flight. */
function latestRequests() {
  const busy = ref(false)
  const begin = latestOnly()
  function drop() {
    begin()
    busy.value = false
  }
  /** Sends a request in place of any in flight; `onAnswer` gets its result, `onError` its error, unless it was dropped first. */
  async function send<T>(request: () => Promise<T>, onAnswer: (r: T) => void, onError: (e: unknown) => void) {
    const latest = begin()
    busy.value = true
    try {
      const r = await request()
      if (latest()) onAnswer(r)
    } catch (e) {
      if (latest()) onError(e)
    } finally {
      if (latest()) busy.value = false
    }
  }
  return { busy, drop, send }
}

/** Calls `changed` at each change of `source` (deep, and once at the start) and `run` 500 ms after the last; stops with the component. */
function debounced(source: Ref<unknown>, changed: () => void, run: () => void) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const restart = () => {
    clearTimeout(timer)
    changed()
    timer = setTimeout(run, 500)
  }
  watch(source, restart, { deep: true, immediate: true })
  onBeforeUnmount(() => clearTimeout(timer))
}

/** The check an answer of /prices/validate reports, its lists never absent. */
const validationOf = (r: ValidateAnswer): PriceValidation => ({ ...r, errors: r?.errors || [], warnings: r?.warnings || [] })

/** The issues an invalid_argument answer of a save carries (`details`); null when it carries none. */
function saveIssues(d: ApiError['details']): PriceValidation | null {
  if (!Array.isArray(d.errors) && !Array.isArray(d.warnings)) return null
  return { ok: !d.errors?.length, expression: d.expression, errors: d.errors || [], warnings: d.warnings || [] }
}

/** The state of the check: the last check and the error, as an answer, a failure or a refused save leaves them. */
function checkState() {
  const validation = ref<PriceValidation | null>(null)
  const error = ref('')
  function onAnswer(r: ValidateAnswer) {
    validation.value = validationOf(r)
    error.value = ''
  }
  function onError(e: unknown) {
    validation.value = null
    error.value = errorMessage(e)
  }
  /** Shows the issues an invalid_argument answer of a save carries. */
  function showSaveIssues(d: ApiError['details']) {
    const v = saveIssues(d)
    if (v) validation.value = v
  }
  return { validation, error, onAnswer, onError, showSaveIssues }
}

/** The server-normalized expression when it differs from the one sent. */
function normalizedExpr(v: PriceValidation | null, sent: PricePayload): string {
  const e = v?.expression
  return e && e.trim() !== sent.expression.trim() ? e : ''
}

/**
 * Server-side check (POST /prices/validate) of the price being edited,
 * debounced 500 ms after each change. Skipped while `localIssues` has entries.
 * Only the latest request writes the state: a change, a skipped run and a
 * reset drop the answer of any request still in flight.
 */
export function usePriceValidation(payload: Ref<PricePayload>, localIssues: Ref<string[]>) {
  const { validation, error, onAnswer, onError, showSaveIssues } = checkState()
  const requests = latestRequests()

  function run() {
    const body = { ...payload.value }
    if (!body.expression.trim() || localIssues.value.length) {
      requests.drop()
      validation.value = null
      return
    }
    return requests.send(() => api.post<ValidateAnswer>('/prices/validate', body), onAnswer, onError)
  }
  debounced(payload, requests.drop, run)

  function reset() {
    requests.drop()
    validation.value = null
    error.value = ''
  }

  const serverExpr = computed(() => normalizedExpr(validation.value, payload.value))
  return { validation, error, validating: requests.busy, serverExpr, showSaveIssues, reset }
}
