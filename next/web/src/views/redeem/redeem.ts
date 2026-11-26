import type { Tone } from '@sub2api/ui'

// Core redeem codes (Finance > Redeem codes, CONTRACTS §46 moved here):
// GET/POST /redeem-codes, DELETE /redeem-codes/:id voids an unused one,
// POST /me/redeem redeems one into the caller's balance.

export type RedeemStatus = 'unused' | 'used' | 'voided' | 'expired'

export const REDEEM_STATUSES: RedeemStatus[] = ['unused', 'used', 'voided', 'expired']

export const REDEEM_TONES: Record<RedeemStatus, Tone> = { unused: 'success', used: 'info', voided: 'gray', expired: 'gray' }

export interface RedeemCode {
  id: number
  /** Masked (the last 4 characters); the plain code is only in the generate response. */
  code: string
  value: string
  status: RedeemStatus
  used_by?: number
  used_by_email?: string
  used_at?: string
  expires_at?: string
  notes?: string
  created_at: string
}

export interface GenerateRequest {
  count: number
  value: number
  expires_at?: string
  notes?: string
}
