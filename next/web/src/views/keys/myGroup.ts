// What the caller gets in one of their own groups (a row of GET /me/groups).
import type { Group } from '@/api/types'
import { rateText } from '@/components/pricing/pricingFormat'

type RatedGroup = Pick<Group, 'rate_multiplier' | 'effective_rate'>

/** The rate the caller pays in the group: their own, or the group's default when the server reports no other. */
export const myRate = (g: RatedGroup) => g.effective_rate ?? g.rate_multiplier

/** Whether the caller pays another rate than the group's default: the rate of their user group there. */
export const hasOwnRate = (g: RatedGroup) => Number(myRate(g)) !== Number(g.rate_multiplier)

/** "vip · ×0.8 · exclusive": a group in a list of choices, by its name, the caller's rate and `exclusive` when it is a private group granted to them. */
export function myGroupLabel(g: RatedGroup & Pick<Group, 'name' | 'exclusive'>, exclusive: string): string {
  return [g.name, rateText(myRate(g)), g.exclusive ? exclusive : ''].filter(Boolean).join(' · ')
}
