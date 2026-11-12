import type { Auditor, AuditorType, Channel, Spec, Stage } from './types'

// Flow editing operations of the canvas editor (CONTRACTS §72.7), ported from
// new-api web/shared/src/content-moderation/ops.ts (the publish check is in
// flowValidate.ts, drag and drop in canvasDrag.ts). Every
// function returns a new spec and never mutates its input; an operation that
// would produce an invalid arrangement (unsupported type, the same auditor
// twice in a stage, past the stage / auditor limits) returns the input
// unchanged, so callers compare by identity to detect a no-op.
//
// Differences from new-api: the spec has no `enabled` (flows.enabled is its
// own column), four auditor types can be placed, at most 8 stages of at
// most 8 auditors (internal/moderation/flow.go).
//
// Stage order: the review stages first, then the fallback stages (补审,
// Stage.fallback). Every operation keeps that order.

export type Combine = Stage['combine']

/** Looks up an auditor's type; undefined when the auditor does not exist. */
export type AuditorTypeOf = (id: number) => string | undefined

/** The auditor types, in the order they are offered. */
export const AUDITOR_TYPES: readonly AuditorType[] = ['openai_moderation', 'typesafe', 'llm_verdict', 'llm_questions']
/** The auditor type judges by a question table (Jev, or an LLM answering yes / no). */
export const hasQuestions = (type: string | undefined) => type === 'typesafe' || type === 'llm_questions'
export const MAX_STAGES = 8
export const MAX_STAGE_AUDITORS = 8
export const TOTAL_TIMEOUT_MIN_MS = 1000
export const TOTAL_TIMEOUT_MAX_MS = 120_000
export const NAME_MAX = 64

/**
 * A manual total timeout typed in seconds, as ms; null when it cannot be a
 * manual value (empty, not a number, 0 or less: 0 means automatic). Values
 * out of 1–120 s are returned, the publish check reports them.
 */
export function manualTimeoutMs(raw: unknown): number | null {
  const s = String(raw ?? '').trim()
  if (s === '') return null
  const ms = Math.round(Number(s) * 1000)
  return Number.isFinite(ms) && ms > 0 ? ms : null
}
export const COMBINES: readonly Combine[] = ['any', 'all', 'at_least']

/** Channel timeout used when a channel stores 0 (§72.6 defaults by type). */
export const DEFAULT_CHANNEL_TIMEOUT_MS: Record<string, number> = {
  openai_moderation: 3000,
  typesafe: 8000,
  llm: 15000,
  llm_verdict: 15000,
  llm_questions: 15000
}

export function isAuditorType(type: string | undefined): type is AuditorType {
  return (AUDITOR_TYPES as readonly (string | undefined)[]).includes(type)
}

/** A blank draft: all models, 100 % sampling, timeout computed on publish. */
export function newSpec(): Spec {
  return { model_filter: { type: 'all', models: [] }, scope: 'input', sample_rate: 100, total_timeout_ms: 0, stages: [] }
}

const withStages = (spec: Spec, stages: Stage[]): Spec => ({ ...spec, stages })

/**
 * The stages left by a removal: when no review stage is left, the first
 * stage becomes one (the first stage cannot be a fallback stage, and its
 * card has no switch to change that).
 */
function keepReviewFirst(stages: Stage[]): Stage[] {
  if (!stages.length || stages.some((s) => !s.fallback)) return stages
  const { fallback: _drop, ...first } = stages[0]
  return [first, ...stages.slice(1)]
}

/** How many review stages lead the list: the index of the first fallback stage (all of them when there is none). */
export function reviewStageCount(stages: readonly Pick<Stage, 'fallback'>[]): number {
  const first = stages.findIndex((s) => s.fallback)
  return first < 0 ? stages.length : first
}

/** Inserts a review stage holding one auditor at `at` (default: last); it never lands behind a fallback stage. */
export function addStage(spec: Spec, auditor: number, typeOf: AuditorTypeOf, at: number = spec.stages.length): Spec {
  if (!isAuditorType(typeOf(auditor)) || spec.stages.length >= MAX_STAGES) return spec
  const stages = [...spec.stages]
  stages.splice(Math.min(at, reviewStageCount(stages)), 0, { auditors: [auditor], combine: 'any' })
  return withStages(spec, stages)
}

/** Removes a stage; when only fallback stages are left, the first of them becomes a review stage. */
export function removeStage(spec: Spec, index: number): Spec {
  if (!spec.stages[index]) return spec
  return withStages(spec, keepReviewFirst(spec.stages.filter((_, i) => i !== index)))
}

/**
 * Moves a stage to another position (stage order is the check order). A
 * review stage stays among the review stages and a fallback stage among the
 * fallback stages: a target across that border is a no-op (setFallback
 * changes what a stage is).
 */
export function moveStage(spec: Spec, from: number, to: number): Spec {
  if (from === to || !spec.stages[from] || to < 0 || to >= spec.stages.length) return spec
  if (!!spec.stages[from].fallback !== !!spec.stages[to].fallback) return spec
  const stages = [...spec.stages]
  const [stage] = stages.splice(from, 1)
  stages.splice(to, 0, stage)
  return withStages(spec, stages)
}

/** Adds an auditor to a stage (in parallel); no duplicates, allowed types only. */
export function addAuditor(spec: Spec, stageIndex: number, auditor: number, typeOf: AuditorTypeOf): Spec {
  const stage = spec.stages[stageIndex]
  if (!stage || stage.auditors.includes(auditor) || stage.auditors.length >= MAX_STAGE_AUDITORS) return spec
  if (!isAuditorType(typeOf(auditor))) return spec
  return withStages(
    spec,
    spec.stages.map((s, i) => (i === stageIndex ? { ...s, auditors: [...s.auditors, auditor] } : s))
  )
}

/** Removes the auditor at `auditorIndex`; a stage left empty is removed too (removeStage). */
export function removeAuditor(spec: Spec, stageIndex: number, auditorIndex: number): Spec {
  const stage = spec.stages[stageIndex]
  if (!stage || auditorIndex < 0 || auditorIndex >= stage.auditors.length) return spec
  const auditors = stage.auditors.filter((_, i) => i !== auditorIndex)
  if (!auditors.length) return removeStage(spec, stageIndex)
  return withStages(
    spec,
    spec.stages.map((s, i) => (i === stageIndex ? { ...s, auditors } : s))
  )
}

/**
 * Moves an auditor within or across stages. `to.stage === stages.length`
 * creates a new review stage, the last one before the fallback stages;
 * `to.index` defaults to the end of the target.
 * A source stage left empty is removed (when only fallback stages are left
 * then, the first of them becomes a review stage). A target that already
 * has the auditor (or is full) is a no-op.
 */
export function moveAuditor(spec: Spec, from: { stage: number; index: number }, to: { stage: number; index?: number }): Spec {
  const auditor = spec.stages[from.stage]?.auditors[from.index]
  if (auditor === undefined) return spec
  if (to.stage < 0 || to.stage > spec.stages.length) return spec
  const target = spec.stages[to.stage]
  if (to.stage !== from.stage && target && (target.auditors.includes(auditor) || target.auditors.length >= MAX_STAGE_AUDITORS)) return spec
  // A new stage is only refused when the source stage stays (it would add one).
  if (!target && spec.stages.length >= MAX_STAGES && spec.stages[from.stage].auditors.length > 1) return spec

  const stages: Stage[] = spec.stages.map((s) => ({ ...s, auditors: [...s.auditors] }))
  stages[from.stage].auditors.splice(from.index, 1)
  if (to.stage === stages.length) stages.splice(reviewStageCount(stages), 0, { auditors: [auditor], combine: 'any' })
  else {
    const list = stages[to.stage].auditors
    list.splice(Math.min(to.index ?? list.length, list.length), 0, auditor)
  }
  return withStages(spec, keepReviewFirst(stages.filter((s) => s.auditors.length > 0)))
}

/**
 * any / all drop `n`; at_least keeps an integer n ≥ 1 (default 1). An `n`
 * above the auditor count is allowed and counts as the auditor count. A
 * fallback stage stays one.
 */
function normalizeCombine(stage: Stage): Stage {
  const fallback = stage.fallback ? { fallback: true } : {}
  if (stage.combine !== 'at_least') return { auditors: stage.auditors, combine: stage.combine, ...fallback }
  const n = Math.max(1, Math.floor(Number(stage.n ?? 1)) || 1)
  return { auditors: stage.auditors, combine: 'at_least', n, ...fallback }
}

/**
 * Makes a stage a fallback stage (补审) or a review stage again. A fallback
 * stage moves behind every review stage and a review stage in front of every
 * fallback stage, so the spec keeps the order the backend requires. The
 * first stage cannot be a fallback stage: with no review stage left in front
 * of it the spec is returned unchanged.
 */
export function setFallback(spec: Spec, stageIndex: number, fallback: boolean): Spec {
  const stage = spec.stages[stageIndex]
  if (!stage || !!stage.fallback === fallback) return spec
  const { fallback: _drop, ...rest } = stage
  const changed: Stage = fallback ? { ...rest, fallback: true } : rest
  const others = spec.stages.filter((_, i) => i !== stageIndex)
  const review = others.filter((s) => !s.fallback)
  if (fallback && !review.length) return spec
  return withStages(spec, [...review, changed, ...others.filter((s) => s.fallback)])
}

export function setCombine(spec: Spec, stageIndex: number, combine: Combine, n?: number): Spec {
  if (!spec.stages[stageIndex]) return spec
  return withStages(
    spec,
    spec.stages.map((s, i) => (i === stageIndex ? normalizeCombine({ ...s, combine, n: n ?? s.n }) : s))
  )
}

/**
 * The draft as it is sent and compared: fixed key order, models always an
 * array, `n` only on at_least, `fallback` only when true. Leaves invalid
 * values as they are so that validation still reports them.
 */
export function canonicalSpec(spec: Spec): Spec {
  return {
    model_filter: { type: spec.model_filter?.type, models: [...(spec.model_filter?.models || [])] },
    scope: spec.scope ?? 'all',
    sample_rate: spec.sample_rate,
    total_timeout_ms: spec.total_timeout_ms,
    stages: (spec.stages || []).map((s) => {
      const st: Stage = { auditors: [...(s.auditors || [])], combine: s.combine || 'any' }
      if (st.combine === 'at_least') st.n = s.n
      if (s.fallback) st.fallback = s.fallback
      return st
    })
  }
}

export const sameSpec = (a: Spec, b: Spec) => JSON.stringify(canonicalSpec(a)) === JSON.stringify(canonicalSpec(b))

// ---- templates

/**
 * first_behavior = first review (OpenAI) → behavior review (Jev);
 * single = one OpenAI stage. Each stage preselects the only auditor of its
 * type; with none or several the stage stays empty for the user to pick
 * (validation reports it until then).
 */
export type FlowTemplate = 'first_behavior' | 'single'
export const FLOW_TEMPLATES: readonly FlowTemplate[] = ['first_behavior', 'single']

export function applyTemplate(spec: Spec, template: FlowTemplate, auditors: readonly { id: number; type: string }[]): Spec {
  const onlyOf = (type: AuditorType): number[] => {
    const matches = auditors.filter((a) => a.type === type)
    return matches.length === 1 ? [matches[0].id] : []
  }
  const types: AuditorType[] = template === 'single' ? ['openai_moderation'] : ['openai_moderation', 'typesafe']
  return withStages(
    spec,
    types.map((type) => ({ auditors: onlyOf(type), combine: 'any' as const }))
  )
}

/**
 * Where a pass of review stage `index` leads (flow.go runFlow), as the key
 * of its outcome line. A pass ends the chain: with an earlier hit it
 * overturns that hit and allows; without one (the first stage, or every
 * stage before failed or was skipped) the fallback stages run. So without
 * fallback stages a pass always allows, the first stage's pass goes to the
 * fallback stages, and a later stage's pass does either.
 */
export function passOutcome(index: number, reviewCount: number, count: number): 'passAllow' | 'passFallback' | 'passOverturn' {
  if (reviewCount >= count) return 'passAllow'
  return index === 0 ? 'passFallback' : 'passOverturn'
}

/**
 * Where a hit of review stage `index` leads, as the key of its outcome line
 * (with n = the number of the next stage): the last review stage's hit is
 * the verdict, an earlier one hands over to the next stage.
 */
export function hitOutcome(index: number, reviewCount: number): { key: 'hitSuspicious' | 'hitNextN'; n: number } {
  return { key: index === reviewCount - 1 ? 'hitSuspicious' : 'hitNextN', n: index + 2 }
}

export type StageRole = 'first' | 'behavior' | 'review' | 'fallback'

/** A fallback stage = fallback (补审); stage 1 = first (初审); a later stage holding a Jev auditor = behavior; else review (analysis_prompt.go stageRole). */
export function stageRole(index: number, stage: Pick<Stage, 'auditors' | 'fallback'>, typeOf: AuditorTypeOf): StageRole {
  if (stage.fallback) return 'fallback'
  if (index === 0) return 'first'
  return stage.auditors.some((id) => typeOf(id) === 'typesafe') ? 'behavior' : 'review'
}

// ---- auditors as the canvas shows them

/** An auditor with its channel resolved (from the list fields or the channel list). */
export interface CanvasAuditor {
  id: number
  name: string
  type: AuditorType
  channelId: number
  channelName: string
  /** false = disabled or missing: its calls fail (an errored auditor leaves the stage's count; a stage with no verdict is passed over) */
  channelOk: boolean
  /** effective single-call timeout (type default when the channel stores 0) */
  timeoutMs: number
}

export function canvasAuditors(auditors: readonly Auditor[], channels: readonly Channel[]): CanvasAuditor[] {
  const byId = new Map(channels.map((c) => [c.id, c]))
  return auditors.map((a) => {
    const ch = byId.get(a.channel_id)
    const enabled = a.channel_enabled ?? ch?.enabled ?? (channels.length ? false : a.usable)
    const timeout = a.channel_timeout_ms ?? ch?.timeout_ms ?? 0
    return {
      id: a.id,
      name: a.name,
      type: a.type,
      channelId: a.channel_id,
      channelName: a.channel_name ?? ch?.name ?? '',
      channelOk: !!enabled,
      timeoutMs: timeout > 0 ? timeout : DEFAULT_CHANNEL_TIMEOUT_MS[a.channel_type || a.type] || 0
    }
  })
}

/** The canvas auditors by id, with an auditor's type (undefined when it is missing) and name (#id when missing). */
export function auditorIndex(auditors: readonly CanvasAuditor[]) {
  const byId: ReadonlyMap<number, CanvasAuditor> = new Map(auditors.map((a) => [a.id, a]))
  return {
    byId,
    typeOf: ((id) => byId.get(id)?.type) as AuditorTypeOf,
    nameOf: (id: number) => byId.get(id)?.name ?? `#${id}`
  }
}

/** Automatic total timeout: Σ over stages of the slowest channel of the stage. */
export function autoTimeoutMs(spec: Spec, byId: ReadonlyMap<number, CanvasAuditor>): number {
  return spec.stages.reduce((sum, st) => sum + Math.max(0, ...st.auditors.map((id) => byId.get(id)?.timeoutMs ?? 0)), 0)
}
