import type { Tone } from '@sub2api/ui'

// Badge tones of the moderation enums (results, modes, levels, severities,
// AI verdicts, trends, suggestions).

export function resultTone(v: string | undefined): Tone {
  switch (v) {
    case 'pass':
      return 'success'
    case 'hit':
      return 'warning'
    case 'block':
      return 'danger'
    case 'overturned':
      return 'info'
    default:
      return 'gray'
  }
}

export function modeTone(m: string | undefined): Tone {
  return m === 'enforce' ? 'danger' : m === 'observe' ? 'info' : 'gray'
}

export function levelTone(l: string | undefined): Tone {
  return l === 'enforce' ? 'danger' : 'warning'
}

export function severityTone(s: string | undefined): Tone {
  switch (s) {
    case 'critical':
    case 'high':
      return 'danger'
    case 'medium':
      return 'warning'
    case 'low':
      return 'info'
    default:
      return 'gray'
  }
}

export function aiVerdictTone(v: string | null | undefined): Tone {
  return v === 'violation' ? 'danger' : v === 'false_positive' ? 'success' : v === 'unsure' ? 'warning' : 'gray'
}

export function trendTone(v: string | null | undefined): Tone {
  switch (v) {
    case 'escalating':
      return 'danger'
    case 'persistent':
      return 'warning'
    case 'occasional':
      return 'info'
    case 'likely_false_positive':
      return 'success'
    default:
      return 'gray'
  }
}

export function suggestionTone(v: string | null | undefined): Tone {
  return v === 'block' ? 'danger' : v === 'observe' ? 'warning' : v === 'release' ? 'success' : 'gray'
}

/** A proposal's state (prototype, design §9): waiting stands out, done is calm. */
export function proposalTone(v: string | null | undefined): Tone {
  return v === 'pending' ? 'warning' : v === 'applied' || v === 'auto_applied' ? 'success' : 'gray'
}

/** A user's risk as the AI judged it (prototype). */
export function riskTone(v: string | null | undefined): Tone {
  return v === 'high' ? 'danger' : v === 'medium' ? 'warning' : v === 'low' ? 'success' : 'gray'
}
