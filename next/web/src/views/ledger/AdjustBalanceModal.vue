<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SField, SHint, SInput, SFormModal, SRadioGroup, STextarea, createItemId, toast, type SelectOption } from '@sub2api/ui'
import EntityPicker from '@/components/EntityPicker.vue'
import { useAuthStore } from '@/stores/auth'
import { applyFormError } from '@/utils/formErrors'
import { formatMoney } from '@/utils/format'

// Adjust a user's balance (POST /users/:id/balance/adjust, balance:adjust).
// currentBalance: the known balance of `userId` (e.g. from the users list), shown while that user is the target.
const props = defineProps<{ open: boolean; userId?: number | null; currentBalance?: string | number | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'done'): void }>()
const { t } = useI18n()
const auth = useAuthStore()

/** The picked user's id ('' = none); the picker only hands out ids. */
const user = ref('')
const credit = ref(true)
const directionOptions = computed<SelectOption[]>(() => [
  { value: true, label: t('ledger.adjust.credit') },
  { value: false, label: t('ledger.adjust.debit') }
])
const amount = ref('')
const note = ref('')
const errors = ref<Record<string, string>>({})
const busy = ref(false)

watch(
  () => props.open,
  (v) => {
    if (!v) return
    user.value = props.userId ? String(props.userId) : ''
    credit.value = true
    amount.value = ''
    note.value = ''
    errors.value = {}
  }
)

const targetUserId = computed<number | null>(() => Number(user.value) || null)

async function submit() {
  errors.value = {}
  if (!targetUserId.value) errors.value.user = t('ledger.adjust.userRequired')
  const a = amount.value.trim()
  if (!/^\d+(\.\d{1,8})?$/.test(a) || Number(a) <= 0) errors.value.amount = t('ledger.adjust.amountInvalid')
  if (Object.keys(errors.value).length) return
  busy.value = true
  try {
    const r = await api.post<{ ledger_id?: number; balance_after?: string; duplicate?: boolean }>(
      `/users/${targetUserId.value}/balance/adjust`,
      { amount: a, credit: credit.value, note: note.value },
      { headers: { 'Idempotency-Key': createItemId() } }
    )
    toast(
      r?.duplicate
        ? t('ledger.adjust.duplicate')
        : r?.balance_after !== undefined
          ? t('ledger.adjust.doneBalance', { balance: formatMoney(r.balance_after) })
          : t('ledger.adjust.done'),
      'success'
    )
    if (targetUserId.value === auth.me?.id) auth.refreshBalance()
    emit('done')
    emit('update:open', false)
  } catch (e) {
    applyFormError(e, errors)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <SFormModal :open="open" :title="t('ledger.adjust.title')" width="md" :loading="busy" :submit-text="t('common.confirm')" :enter-submit="false" @update:open="emit('update:open', $event)" @submit="submit">
    <div class="space-y-4">
      <div v-if="currentBalance !== undefined && currentBalance !== null && targetUserId === userId" class="text-sm" data-testid="adjust-current-balance">
        <SHint inline>{{ t('ledger.adjust.currentBalance') }}:</SHint>
        <span class="ml-2 font-mono font-semibold">{{ formatMoney(currentBalance) }}</span>
      </div>
      <SField :label="t('common.user')" :error="errors.user" required>
        <EntityPicker v-model:id="user" kind="user" data-testid="adjust-user" />
      </SField>
      <SField :label="t('ledger.adjust.direction')">
        <SRadioGroup v-model="credit" inline class="pt-1" :options="directionOptions" />
      </SField>
      <SField :label="t('ledger.adjust.amount')" :error="errors.amount" required>
        <div class="flex items-center gap-2">
          <span class="font-mono text-lg" :class="credit ? 'text-success-600 dark:text-success-400' : 'text-danger-600 dark:text-danger-400'">{{ credit ? '+' : '-' }}</span>
          <!-- Opened on a user, the amount is what is left to type; without one focus goes to the picker, which lists the users. -->
          <SInput v-model="amount" mono inputmode="decimal" placeholder="20.00" :data-autofocus="userId ? '' : undefined" />
          <SHint inline>{{ t('common.usd') }}</SHint>
        </div>
      </SField>
      <SField :label="t('common.note')" :error="errors.note">
        <STextarea v-model="note" :rows="2" :placeholder="t('ledger.adjust.notePlaceholder')" />
      </SField>
    </div>
  </SFormModal>
</template>
