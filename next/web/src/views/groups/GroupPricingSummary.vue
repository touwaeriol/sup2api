<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint, SLink } from '@sub2api/ui'
import type { Group } from '@/api/types'

// How a group prices its models, in a table cell or a detail row: at their
// original prices, or so many at a group price. With `link` the summary is a
// button that asks for the prices (`open`). It stays whole in a table cell;
// on phones the list's cards are narrower than it, so it wraps there.
const props = defineProps<{ group: Pick<Group, 'price_rule_count'>; link?: boolean }>()
const emit = defineEmits<{ (e: 'open'): void }>()
const { t } = useI18n()

const ruled = computed(() => props.group.price_rule_count ?? 0)
</script>

<template>
  <SHint v-if="!ruled" inline data-testid="group-pricing-summary">{{ t('groups.pricingSummary.base') }}</SHint>
  <component :is="link ? SLink : 'span'" v-else :as="link ? 'button' : undefined" class="text-left sm:whitespace-nowrap" data-testid="group-pricing-summary" @click="link && emit('open')">
    {{ t('groups.pricingSummary.models', { n: ruled }, ruled) }}
  </component>
</template>
