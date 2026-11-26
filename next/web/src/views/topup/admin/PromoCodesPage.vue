<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SButton, SDateTime, SPageHeader, SPagination, SProgress, SRowAction, STable, type TableColumn } from '@sub2api/ui'
import { deleteWithConfirm, toggleStatus } from '@/composables/crud'
import { useList } from '@/composables/useList'
import { formatMoney, formatNumber } from '@/utils/format'
import { TOPUP_PATHS } from '../api'
import type { PromoCode } from '../topup'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import PromoEditModal from './PromoEditModal.vue'
import PromoUsageDrawer from './PromoUsageDrawer.vue'
import { actionsColumn, columnOf } from './columns'

// Promo codes (admin): a bonus credited on top of a paid top-up when the
// user enters the code at checkout. The state shown (expired, used up) is
// the server's.
const { t } = useI18n()

const list = useList<PromoCode>(TOPUP_PATHS.promoCodes)

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.promo.fields')
  return [
    col('code', { mobile: 'title' }),
    col('bonus_amount', { align: 'right', mobile: 'meta' }),
    col('min_amount', { align: 'right', mobile: 'meta' }),
    col('usage', { mobile: 'meta' }),
    col('expires_at', { mobile: 'meta' }),
    col('status', { mobile: 'status' }),
    col('notes', { grow: true, mobile: 'subtitle' }),
    col('created_at', { mobile: 'detail' }),
    actionsColumn()
  ]
})

const usageText = (p: PromoCode) => `${formatNumber(p.used_count)} / ${p.max_uses ? formatNumber(p.max_uses) : t('common.unlimited')}`

const editOpen = ref(false)
const editing = ref<PromoCode | null>(null)
function openEdit(p: PromoCode | null) {
  editing.value = p
  editOpen.value = true
}

const usageOpen = ref(false)
const usagePromo = ref<PromoCode | null>(null)
function openUsage(p: PromoCode) {
  usagePromo.value = p
  usageOpen.value = true
}

const toggle = (p: PromoCode) => toggleStatus(TOPUP_PATHS.promoCode(p.id), p.status, list.reload)
const remove = (p: PromoCode) =>
  deleteWithConfirm(TOPUP_PATHS.promoCode(p.id), t('topup.admin.promo.deleteConfirm', { code: p.code }), list.reload)
</script>

<template>
  <div>
    <SPageHeader :title="t('topup.admin.promo.title')">
      <template #actions>
        <SButton icon="refresh" :loading="list.loading.value" @click="list.reload">{{ t('common.refresh') }}</SButton>
        <SButton variant="primary" icon="plus" @click="openEdit(null)">{{ t('topup.admin.promo.create') }}</SButton>
      </template>
    </SPageHeader>

    <SAlert tone="info" class="mb-3">{{ t('topup.admin.promo.intro') }}</SAlert>

    <STable
      card
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :error="list.error.value"
      :empty-text="t('topup.admin.promo.empty')"
      @retry="list.reload"
    >
      <template #cell-code="{ row }"
        ><span class="font-mono text-[0.8125rem]">{{ row.code }}</span></template
      >
      <template #cell-bonus_amount="{ row }"
        ><span class="tabular-nums">{{ formatMoney(row.bonus_amount, 2) }}</span></template
      >
      <template #cell-min_amount="{ row }">
        <span class="tabular-nums" :class="{ 'text-fg-muted': !Number(row.min_amount) }">{{
          Number(row.min_amount) ? formatMoney(row.min_amount, 2) : t('topup.admin.promo.anyAmount')
        }}</span>
      </template>
      <template #cell-usage="{ row }">
        <div class="w-full md:min-w-32">
          <SProgress
            v-if="row.max_uses"
            :value="row.used_count"
            :max="row.max_uses"
            :tone="row.used_count >= row.max_uses ? 'warning' : 'primary'"
            >{{ usageText(row) }}</SProgress
          >
          <span v-else class="text-xs tabular-nums text-fg-muted">{{ usageText(row) }}</span>
        </div>
      </template>
      <template #cell-expires_at="{ row }"
        ><SDateTime :value="row.expires_at" mode="absolute" muted :empty="t('topup.admin.never')"
      /></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="promo" :status="row.state" /></template>
      <template #cell-notes="{ row }"
        ><span :class="{ 'text-fg-muted': !row.notes }">{{ row.notes || '—' }}</span></template
      >
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <span class="inline-flex flex-wrap justify-start gap-1.5 md:flex-nowrap md:justify-end">
          <SRowAction :label="t('topup.admin.promo.usages')" @click="openUsage(row)" />
          <SRowAction :label="t('common.edit')" @click="openEdit(row)" />
          <SRowAction :label="row.status === 'active' ? t('common.disable') : t('common.enable')" @click="toggle(row)" />
          <SRowAction :label="t('common.delete')" tone="danger" @click="remove(row)" />
        </span>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <PromoEditModal v-model:open="editOpen" :promo="editing" @saved="list.reload" />
    <PromoUsageDrawer v-if="usagePromo" :key="usagePromo.id" v-model:open="usageOpen" :promo="usagePromo" />
  </div>
</template>
