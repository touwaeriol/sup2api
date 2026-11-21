import { api } from '@sub2api/host'
import type { PersonalDialogUser, Segment } from '@/api/pricing'
import { useAuthStore } from '@/stores/auth'

/**
 * Whether the caller may list the members of a segment
 * (GET /segments/:id/members): its rows are users, so it takes user:read
 * besides the segment:read the pages of segments are opened with.
 */
export function canListMembers(auth = useAuthStore()): boolean {
  return auth.has('user:read')
}

/**
 * Moves users into a named segment (POST /segments/:id/members). A user is in
 * exactly one segment, so this is every change of membership; the personal
 * segment a user leaves is deleted with its settings.
 */
export function moveToSegment(segmentId: number, userIds: number[]) {
  return api.post<{ moved: number }>(`/segments/${segmentId}/members`, { user_ids: userIds })
}

/**
 * The member of a personal segment, as the personal dialogs take a user. A
 * caller who does not read users is told no `user`: the member's id and the
 * segment's name ("#id" to him) stand in, which is all moving him back takes.
 */
export function personalUserOf(s: Segment | null): PersonalDialogUser | null {
  if (!s || s.user_id === undefined) return null
  return { id: s.user_id, email: s.user?.email ?? s.name, display_name: s.user?.display_name ?? '', segment: s }
}
