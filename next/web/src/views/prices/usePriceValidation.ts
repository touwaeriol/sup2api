import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { api, type ApiError } from '@sub2api/host'
import type { PriceValidateResult } from '@/api/types'
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

/**
 * Server-side check (POST /prices/validate) of the price being edited,
 * debounced 500 ms after each change. Skipped while `localIssues` has entries.
 */
export function usePriceValidation(payload: Ref<PricePayload>, localIssues: Ref<string[]>) {
  const validation = ref<PriceValidation | null>(null)
  const error = ref('')
  const validating = ref(false)
  let seq = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  async function run() {
    const body = { ...payload.value }
    if (!body.expression.trim() || localIssues.value.length) {
      validation.value = null
      return
    }
    const my = ++seq
    validating.value = true
    try {
      const r = await api.post<PriceValidateResult & { cost_per_million_input?: string; samples?: number; analysis?: PriceAnalysis }>('/prices/validate', body)
      if (my !== seq) return
      validation.value = { ...r, errors: r?.errors || [], warnings: r?.warnings || [] }
      error.value = ''
    } catch (e) {
      if (my !== seq) return
      validation.value = null
      error.value = errorMessage(e)
    } finally {
      if (my === seq) validating.value = false
    }
  }

  watch(
    payload,
    () => {
      clearTimeout(timer)
      timer = setTimeout(run, 500)
    },
    { deep: true, immediate: true }
  )
  onBeforeUnmount(() => clearTimeout(timer))

  /** Server-normalized expression when it differs from the locally generated one. */
  const serverExpr = computed(() => {
    const e = validation.value?.expression
    return e && e.trim() !== payload.value.expression.trim() ? e : ''
  })

  /** Shows the issues an invalid_argument answer of a save carries (`details`). */
  function showSaveIssues(d: ApiError['details']) {
    if (!Array.isArray(d.errors) && !Array.isArray(d.warnings)) return
    validation.value = { ok: !d.errors?.length, expression: d.expression, errors: d.errors || [], warnings: d.warnings || [] }
  }

  function reset() {
    validation.value = null
    error.value = ''
  }

  return { validation, error, validating, serverExpr, showSaveIssues, reset }
}
