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
