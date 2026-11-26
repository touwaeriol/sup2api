export default {
  title: 'Balance ledger',
  description: 'Every balance change of every user.',
  balance: 'Balance',
  negativeHint: 'Your balance is negative; new requests are rejected until it is topped up.',
  operator: 'by admin #{id}',
  cols: {
    kind: 'Type',
    delta: 'Change',
    balance: 'Balance',
    note: 'Note'
  },
  kinds: {
    usage: 'Usage',
    admin_adjust: 'Admin adjustment',
    plugin_credit: 'Plugin credit',
    plugin_debit: 'Plugin debit',
    refund: 'Refund',
    redeem: 'Redeem code'
  },
  adjust: {
    currentBalance: 'Current balance',
    button: 'Adjust balance',
    title: 'Adjust balance',
    userRequired: 'Choose a user',
    direction: 'Direction',
    credit: 'Credit (add)',
    debit: 'Debit (subtract)',
    amount: 'Amount',
    amountInvalid: 'Enter a positive amount with at most 8 decimals',
    notePlaceholder: 'e.g. top-up',
    done: 'Balance adjusted',
    doneBalance: 'Balance adjusted, new balance {balance}',
    duplicate: 'This adjustment was already applied'
  }
}
