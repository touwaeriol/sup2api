import type { Tone } from '@sub2api/ui'

// Core redeem codes (Top-up & payment > Redeem codes, CONTRACTS §140):
// GET/POST /redeem-codes, DELETE /redeem-codes/:id voids an unused one,
// POST /me/redeem redeems one into the caller's balance.

export type RedeemStatus = 'unused' | 'used' | 'voided' | 'expired'

export const REDEEM_STATUSES: RedeemStatus[] = ['unused', 'used', 'voided', 'expired']

export const REDEEM_TONES: Record<RedeemStatus, Tone> = { unused: 'success', used: 'info', voided: 'gray', expired: 'gray' }

export interface RedeemCode {
  id: number
  /** The code's last 4 characters: the server keeps no more, the plain code is only in the generate response. */
  code_tail: string
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

/** POST /redeem-codes: the plain codes (XXXX-XXXX-…), shown this once. */
export interface Generated {
  codes: string[]
}

/** POST /me/redeem: what was credited and the balance after it. */
export interface Redeemed {
  amount: string
  balance: string
}

/** Decimals a code's value is shown with: the ledger's 8, trailing zeros dropped (formatMoney keeps two). */
export const REDEEM_DIGITS = 8

/** A listed code, masked: only its last 4 characters are known. */
export const maskCode = (tail: string) => `••••-••••-••••-${tail}`
