<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SStatusBadge } from '@sub2api/ui'
import { statusLabelKey, statusTones, type StatusKind } from './status'

// Status badge of an order, a promo code or a promo code use.
const props = defineProps<{ kind: StatusKind; status: string }>()
const { t } = useI18n()
const label = computed(() => t(statusLabelKey(props.kind, props.status)))
</script>

<template>
  <SStatusBadge :status="status" :label="label" :tones="statusTones(kind)" size="xs" />
</template>
