<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SAlert, SCheckbox, SDescriptions, SField, SForm, SFormModal, SInput, STextarea, type DescItem, type Rules } from '@sub2api/ui'
import { paymentApi, usePaymentHost, type Order } from '../host'
import { formatLocal, formatUsd } from '../format'
import { useModalForm } from './actions'
import { REFUND_MODES, deductFor, payShare, refundLeft, refundsAll, shortfallOf } from './refunds'

// Refunds a credited order, in full or in part: the money goes back through
// the order's gateway (or was returned at the provider by hand and is only
// recorded), and the balance is taken back from the user unless unticked.
const props = defineProps<{ open: boolean; order: Order | null }>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  refunded: [order: Order]
}>()
const host = usePaymentHost()
const t = host.t

interface Draft extends Record<string, unknown> {
  amount?: number
  reason?: string
  deduct_balance: boolean
  force: boolean
}

const left = computed(() => (props.order ? refundLeft(props.order) : 0))
const rules: Rules<Draft> = {
  amount: [
    { required: true, type: 'number' },
    { validator: v => Number(v) > 0 || t('admin.positive') },
    {
      validator: v => Number(v) <= left.value || t('admin.refund.amountMax', { max: formatUsd(left.value) })
    }
  ],
  reason: { required: true, whitespace: true, max: 200 }
}
const { model, form, saving, reset, submit } = useModalForm<Draft>(
  () => ({
    amount: left.value,
    reason: '',
    deduct_balance: true,
    force: false
  }),
  rules
)
// The 409 of a short balance, shown under the force box until the form changes.
const shortfall = ref<ReturnType<typeof shortfallOf>>(null)

watch(
  () => props.open,
  open => open && reset()
)
watch(model, () => (shortfall.value = null), { deep: true })

const mode = computed(() => REFUND_MODES[props.order?.gateway ?? 'epay'])
const amount = computed(() => Math.max(0, Number(model.value.amount) || 0))
const pay = computed(() => (props.order ? formatLocal(payShare(props.order, amount.value), props.order.currency) : ''))
const deduct = computed(() => (props.order ? formatUsd(deductFor(props.order, amount.value)) : ''))
const bonusNote = computed(() => {
  const o = props.order
  if (!o?.bonus) return ''
  return t(refundsAll(o, amount.value) ? 'admin.refund.bonusAll' : 'admin.refund.bonusPart', { bonus: formatUsd(o.bonus) })
})

const summary = computed<DescItem[]>(() => {
  const o = props.order
  if (!o) return []
  return [
    {
      key: 'no',
      label: t('admin.orders.fields.out_trade_no'),
      value: o.out_trade_no,
      mono: true
    },
    {
      key: 'user',
      label: t('admin.orders.fields.user'),
      value: o.user_email || `#${o.user_id}`
    },
    {
      key: 'amount',
      label: t('admin.orders.fields.amount'),
      value: t('admin.refund.paidWith', {
        amount: formatUsd(o.amount),
        pay: formatLocal(o.pay_amount, o.currency)
      })
    },
    ...(o.refunded_amount
      ? [
          {
            key: 'refunded',
            label: t('admin.orders.fields.refunded_amount'),
            value: formatUsd(o.refunded_amount)
          }
        ]
      : []),
    {
      key: 'left',
      label: t('admin.refund.left'),
      value: formatUsd(left.value)
    }
  ]
})

/** Sends the refund; a short balance stays in the dialog instead of a toast. */
async function send(o: Order, d: Draft): Promise<Order | undefined> {
  const body = {
    amount: Number(d.amount),
    reason: String(d.reason).trim(),
    deduct_balance: d.deduct_balance,
    force: d.deduct_balance && d.force,
    mode: mode.value
  }
  try {
    return await paymentApi.refundOrder(o.id, body)
  } catch (e) {
    shortfall.value = shortfallOf(e)
    if (!shortfall.value) throw e
    return undefined
  }
}

async function save() {
  const o = props.order
  const res = o && (await submit(d => send(o, d)))
  if (!res) return
  host.toast(t('admin.refund.done'), 'success')
  emit('update:open', false)
  emit('refunded', res)
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="t('admin.refund.title')"
    :loading="saving"
    :submit-text="t('admin.refund.submit')"
    submit-variant="danger"
    :enter-submit="false"
    @update:open="emit('update:open', $event)"
    @submit="save"
  >
    <SDescriptions :items="summary" label-width="6rem" dense class="payment-refund-summary" />
    <SForm :form="form">
      <SField prop="amount" :label="t('admin.refund.amount')" :hint="t('admin.refund.amountHint', { max: formatUsd(left), pay })">
        <SInput type="number" min="0" :max="left" step="0.01" suffix="USD" v-bind="form.bindNumber('amount')" />
      </SField>
      <SField prop="reason" :label="t('admin.refund.reason')">
        <STextarea :rows="2" :maxlength="200" :placeholder="t('admin.refund.reasonPlaceholder')" v-bind="form.bind('reason')" />
      </SField>
      <SField :label="t('admin.refund.mode')">
        <SAlert dense :tone="mode === 'manual' ? 'warning' : 'info'" :title="t(`admin.refund.modes.${mode}.title`)">
          {{ t(`admin.refund.modes.${mode}.body`, { pay }) }}
        </SAlert>
      </SField>
      <SField prop="deduct_balance" :hint="bonusNote || t('admin.refund.deductHint')">
        <SCheckbox :label="t('admin.refund.deduct', { amount: deduct })" v-bind="form.bind('deduct_balance')" />
      </SField>
      <SField v-if="model.deduct_balance" prop="force" :hint="t('admin.refund.forceHint')">
        <SCheckbox :label="t('admin.refund.force')" :error="!!shortfall" v-bind="form.bind('force')" />
      </SField>
      <SAlert v-if="shortfall" tone="danger" dense :title="shortfall.message">{{ shortfall.detail }}</SAlert>
    </SForm>
  </SFormModal>
</template>

<style scoped>
.payment-refund-summary {
  margin-bottom: 1rem;
}
</style>
