// The balance ledger of the mock: usage debits and a few credits, and
// GET /ledger, /me/ledger.
import { now, on, paginate } from '../router'
import { money } from './shared'
import { USERS } from './usageData'
import { usageRows } from './usageSeed'

/** The balance events: a top-up of every user, the debit of each billed record, a refund, an online top-up with its promo bonus and a redeemed code; oldest first. */
function ledgerEvents(): any[] {
  const topUp = new Date(Date.now() - 8 * 86400000).toISOString()
  const events: any[] = USERS.map((user) => ({ user, delta: 20, kind: 'admin_adjust', at: topUp, note: 'top-up', ref_type: '', ref_id: '', operator_id: 1 }))
  for (const u of usageRows) {
    if (u.billing_status !== 'billed') continue
    const user = USERS.find((x) => x.id === u.user_id)!
    events.push({ user, delta: -Number(u.total_cost), kind: 'usage', at: u.created_at, note: '', ref_type: 'usage', ref_id: u.request_id, usage: u })
  }
  events.push({ user: USERS[1], delta: 0.5, kind: 'refund', at: now(-3600), note: 'refund for failed request', ref_type: 'usage', ref_id: 'req_refund0001', operator_id: 1 })
  events.push({ user: USERS[2], delta: 50, kind: 'topup', at: now(-7200), note: 'online top-up', ref_type: 'payment_order', ref_id: 's2a_9f3c2a7d1e4b4c08a6f1d2e3b4c5d6e7' })
  events.push({ user: USERS[2], delta: 5, kind: 'promo_bonus', at: now(-7200), note: 'promo code WELCOME5', ref_type: 'payment_order', ref_id: 's2a_9f3c2a7d1e4b4c08a6f1d2e3b4c5d6e7' })
  events.push({ user: USERS[1], delta: 10, kind: 'redeem', at: now(-5400), note: '', ref_type: 'redeem_code', ref_id: '296' })
  return events.sort((a, b) => a.at.localeCompare(b.at))
}

/** The ledger rows of the events (oldest first), newest first: each with the balance after it; a usage record learns its row. */
function ledgerOf(events: any[]): any[] {
  const running = new Map<number, number>()
  const rows = events.map((e, i) => {
    const bal = (running.get(e.user.id) || 0) + e.delta
    running.set(e.user.id, bal)
    const id = 88000 + i
    if (e.usage) e.usage.ledger_id = id
    return {
      id,
      user_id: e.user.id,
      user_email: e.user.email,
      user_name: e.user.name,
      delta: money(e.delta),
      balance_after: money(bal),
      kind: e.kind,
      ref_type: e.ref_type,
      ref_id: e.ref_id,
      idempotency_key: e.kind === 'usage' ? `usage:${e.ref_id}` : `mock:${id}`,
      operator_id: e.operator_id ?? null,
      plugin_key: e.plugin_key ?? null,
      note: e.note,
      created_at: e.at
    }
  })
  return rows.reverse()
}

const ledgerRows = ledgerOf(ledgerEvents())

function ledgerFilter(rows: any[], q: Record<string, string>) {
  return rows.filter((l) => {
    if (q.user_id && String(l.user_id) !== q.user_id) return false
    if (q.kind && l.kind !== q.kind) return false
    if (q.from && l.created_at < q.from) return false
    if (q.to && l.created_at > q.to) return false
    return true
  })
}

on('GET', '/ledger', (req) => paginate(ledgerFilter(ledgerRows, req.query), req.query))
on('GET', '/me/ledger', (req) => paginate(ledgerFilter(ledgerRows.filter((l) => l.user_id === 1), req.query), req.query))
