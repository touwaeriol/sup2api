<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton } from '@sub2api/ui'
import type { SegmentRef } from '@/api/pricing'
import { useAuthStore } from '@/stores/auth'
import PersonalConvertDialog from './PersonalConvertDialog.vue'

// A button that sets one user's rates apart: the convert dialog asks whom and
// answers with his personal segment, the one he has or the one it made for
// him (`created` true: there is one more of them now). Renders nothing for a
// caller who may not do that.
withDefaults(defineProps<{ label?: string; size?: 'sm' | 'md'; disabled?: boolean }>(), { label: undefined, size: 'md' })
const emit = defineEmits<{ (e: 'select', segment: SegmentRef, created: boolean): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const allowed = computed(() => auth.has('user:read') && auth.has('segment:manage'))
const open = ref(false)
</script>

<template>
  <template v-if="allowed">
    <SButton :size="size" icon="plus" :disabled="disabled" data-testid="personal-segment-picker" @click="open = true">{{ label ?? t('segments.convert.pick') }}</SButton>
    <PersonalConvertDialog v-model:open="open" :user="null" @done="(segment, created) => emit('select', segment, created)" />
  </template>
</template>
