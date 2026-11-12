import { isAuditorType, MAX_STAGE_AUDITORS, MAX_STAGES, TOTAL_TIMEOUT_MAX_MS, TOTAL_TIMEOUT_MIN_MS, type AuditorTypeOf } from './flowOps'
import { SCOPES, type Spec, type Stage } from './types'

// Validation of a flow draft before it is published (CONTRACTS §72.7),
// mirroring ParseSpec + Spec.Validate of internal/moderation/flow.go (ported
// from new-api web/shared/src/content-moderation/validate.ts).

type SpecIssueCode =
  | 'unknown_field'
  | 'invalid_field'
  | 'invalid_model_filter'
  | 'models_required'
  | 'invalid_sample_rate'
  | 'invalid_scope'
  | 'invalid_total_timeout'
  | 'no_stages'
  | 'too_many_stages'
  | 'empty_stage'
  | 'too_many_auditors'
  | 'duplicate_auditor'
  | 'unknown_auditor'
  | 'unsupported_auditor_type'
  | 'invalid_combine'
  | 'invalid_n'
  | 'fallback_first'
  | 'fallback_order'

export interface SpecIssue {
  code: SpecIssueCode
  /** stage index of a stage-level issue */
  stage?: number
  auditor?: number
  field?: string
}

const SPEC_KEYS = ['model_filter', 'scope', 'sample_rate', 'total_timeout_ms', 'stages']
const MODEL_FILTER_KEYS = ['type', 'models']
const STAGE_KEYS = ['auditors', 'combine', 'n', 'fallback']
const MODEL_FILTER_TYPES = new Set(['all', 'only', 'exclude'])
const COMBINE_VALUES = new Set(['', 'any', 'all', 'at_least'])

const unknownKeys = (value: object, known: readonly string[]) => Object.keys(value).filter((k) => !known.includes(k))

/** Validates a draft; `typeOf` resolves auditor ids. Empty = publishable. */
export function validateSpec(spec: Spec, typeOf: AuditorTypeOf): SpecIssue[] {
  const issues: SpecIssue[] = []
  for (const field of unknownKeys(spec, SPEC_KEYS)) issues.push({ code: 'unknown_field', field })

  const filter = spec.model_filter ?? { type: '', models: [] }
  for (const field of unknownKeys(filter, MODEL_FILTER_KEYS)) issues.push({ code: 'unknown_field', field: `model_filter.${field}` })
  const models = filter.models ?? []
  if (!MODEL_FILTER_TYPES.has(filter.type) || !Array.isArray(models)) issues.push({ code: 'invalid_model_filter' })
  else if (filter.type !== 'all' && !models.length) issues.push({ code: 'models_required' })

  if (spec.scope !== undefined && !(SCOPES as readonly string[]).includes(spec.scope)) issues.push({ code: 'invalid_scope' })

  const rate = spec.sample_rate ?? 0
  if (typeof rate !== 'number' || !(rate >= 0 && rate <= 100)) issues.push({ code: 'invalid_sample_rate' })

  const timeout = spec.total_timeout_ms ?? 0
  if (!(Number.isInteger(timeout) && (timeout === 0 || (timeout >= TOTAL_TIMEOUT_MIN_MS && timeout <= TOTAL_TIMEOUT_MAX_MS)))) {
    issues.push({ code: 'invalid_total_timeout' })
  }

  const stages = spec.stages ?? []
  if (!Array.isArray(stages)) {
    issues.push({ code: 'invalid_field', field: 'stages' })
    return issues
  }
  if (!stages.length) issues.push({ code: 'no_stages' })
  if (stages.length > MAX_STAGES) issues.push({ code: 'too_many_stages' })
  // The first stage is a review stage and no review stage follows a fallback stage (Spec.Validate).
  if (stages[0]?.fallback) issues.push({ code: 'fallback_first', stage: 0 })
  stages.forEach((st, i) => {
    if (i > 0 && stages[i - 1]?.fallback && !st?.fallback) issues.push({ code: 'fallback_order', stage: i })
    issues.push(...validateStage(st, i, typeOf))
  })
  return issues
}

function validateStage(stage: Stage, index: number, typeOf: AuditorTypeOf): SpecIssue[] {
  if (typeof stage !== 'object' || stage === null) return [{ code: 'invalid_field', stage: index, field: 'stages' }]
  const issues: SpecIssue[] = []
  for (const field of unknownKeys(stage, STAGE_KEYS)) issues.push({ code: 'unknown_field', stage: index, field })
  const auditors = stage.auditors ?? []
  if (!Array.isArray(auditors)) return [...issues, { code: 'invalid_field', stage: index, field: 'auditors' }]
  if (!auditors.length) issues.push({ code: 'empty_stage', stage: index })
  if (auditors.length > MAX_STAGE_AUDITORS) issues.push({ code: 'too_many_auditors', stage: index })

  const seen = new Set<number>()
  for (const id of auditors) {
    if (seen.has(id)) issues.push({ code: 'duplicate_auditor', stage: index, auditor: id })
    seen.add(id)
    const type = Number.isInteger(id) ? typeOf(id) : undefined
    if (type === undefined) issues.push({ code: 'unknown_auditor', stage: index, auditor: id })
    else if (!isAuditorType(type)) issues.push({ code: 'unsupported_auditor_type', stage: index, auditor: id })
  }

  const combine = stage.combine ?? ''
  if (!COMBINE_VALUES.has(combine)) issues.push({ code: 'invalid_combine', stage: index })
  const n = stage.n ?? 0
  if (!Number.isInteger(n) || (combine === 'at_least' && n < 1)) issues.push({ code: 'invalid_n', stage: index })
  if (stage.fallback !== undefined && typeof stage.fallback !== 'boolean') issues.push({ code: 'invalid_field', stage: index, field: 'fallback' })
  return issues
}
