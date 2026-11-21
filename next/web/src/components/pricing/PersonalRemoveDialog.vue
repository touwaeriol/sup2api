<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SField, SFormModal, toast } from '@sub2api/ui'
import type { PersonalDialogUser, SegmentRef } from '@/api/pricing'
import { notifyError } from '@/utils/errors'
import SegmentSelect from './SegmentSelect.vue'
import { useSegmentsLookup } from './segmentLookup'

// Moves a user out of his personal segment into a named one
// (DELETE /users/:id/personal-segment?to=): his own grants and rates go with it.
// `origin` is the segment he was in before, offered first.
const props = defineProps<{ open: boolean; user: PersonalDialogUser | null; origin?: SegmentRef | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'done'): void }>()
const { t } = useI18n()
const { segments } = useSegmentsLookup()

const target = ref<number | null>(null)
const busy = ref(false)

/** Back where he came from; the default segment when that one is gone or unknown. */
function initialTarget(): number | null {
  const back = segments.value.find((s) => s.id === props.origin?.id) ?? segments.value.find((s) => s.is_default)
  return back?.id ?? null
}

watch(
  () => props.open,
  async (v) => {
    if (!v) return
    target.value = null
    await useSegmentsLookup().ready
    target.value ??= initialTarget()
  },
  { immediate: true }
)

async function submit() {
  const user = props.user
  const to = segments.value.find((s) => s.id === target.value)
  if (!user || !to) return
  busy.value = true
  try {
    await api.del(`/users/${user.id}/personal-segment`, { to: to.id })
    toast(t('segments.remove.done', { name: user.email, segment: to.name }), 'success')
    emit('done')
    emit('update:open', false)
  } catch (e) {
    notifyError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="t('segments.remove.title', { name: user?.email || '' })"
    :loading="busy"
    :submit-text="t('segments.remove.submit')"
    submit-variant="danger"
    :submit-disabled="target === null"
    :submit-attrs="{ 'data-testid': 'personal-remove-submit' }"
    :enter-submit="false"
    data-testid="personal-remove-dialog"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
    <SField :label="t('segments.remove.target')">
      <SegmentSelect v-model="target" data-testid="personal-remove-target" />
    </SField>
    <SAlert tone="danger">{{ t('segments.remove.warning') }}</SAlert>
  </SFormModal>
</template>
