<script setup lang="ts">
import { computed } from 'vue'
import { SStatusBadge } from '@sub2api/ui'
import { usePaymentHost } from '../host'
import { statusLabelKey, statusTones, type StatusKind } from './status'

// Status badge of an order, a promo code or a promo code use.
const props = defineProps<{ kind: StatusKind; status: string }>()
const host = usePaymentHost()
const label = computed(() => host.t(statusLabelKey(props.kind, props.status)))
</script>

<template>
  <SStatusBadge :status="status" :label="label" :tones="statusTones(kind)" size="xs" />
</template>
