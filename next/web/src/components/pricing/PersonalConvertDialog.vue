<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SChoiceCards, SField, SFormModal, toast, type ChoiceOption } from '@sub2api/ui'
import type { PersonalDialogUser, Segment, SegmentRef } from '@/api/pricing'
import { notifyError } from '@/utils/errors'
import UserPick from './UserPick.vue'

// Prices a user on his own (POST /users/:id/personal-segment): he leaves his
// user group for one of his own, which starts as a copy of that group's
// grants, rates and model rates, or empty. Without `user` the dialog first asks
// whom: a user who is priced on his own already is answered at once. `done`
// carries his personal segment and whether it was made just now.
const props = defineProps<{ open: boolean; user: PersonalDialogUser | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'done', segment: SegmentRef, created: boolean): void }>()
const { t } = useI18n()

/** The user picked here, when the opener names none. */
const picked = ref<PersonalDialogUser | null>(null)
const copy = ref(true)
const busy = ref(false)
watch(
  () => props.open,
  (v) => {
    if (!v) return
    copy.value = true
    picked.value = null
  }
)

const target = computed(() => props.user ?? picked.value)
const from = computed(() => target.value?.segment?.name || t('segments.convert.currentSegment'))
const choices = computed<ChoiceOption[]>(() => [
  { value: true, icon: 'copy', label: t('segments.convert.copy', { from: from.value }), description: t('segments.convert.copyHint') },
  { value: false, icon: 'plus', label: t('segments.convert.blank'), description: t('segments.convert.blankHint') }
])

function done(segment: SegmentRef, created: boolean) {
  emit('done', segment, created)
  emit('update:open', false)
}

function pick(user: PersonalDialogUser) {
  if (user.segment?.kind === 'personal') return done(user.segment, false)
  picked.value = user
}

async function submit() {
  const user = target.value
  if (!user) return
  busy.value = true
  try {
    const segment = await api.post<Segment>(`/users/${user.id}/personal-segment`, { copy: copy.value })
    toast(t('segments.convert.done', { name: user.email }), 'success')
    done(segment, true)
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
    :title="target ? t('segments.convert.title', { name: target.email }) : t('segments.convert.pick')"
    :loading="busy"
    :submit-text="t('segments.convert.submit')"
    :submit-disabled="!target"
    :submit-attrs="{ 'data-testid': 'personal-convert-submit' }"
    :enter-submit="false"
    data-testid="personal-convert-dialog"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
    <SField v-if="!user" :label="t('common.user')" :hint="t('segments.convert.pickHint')">
      <UserPick :placeholder="t('segments.convert.pickPlaceholder')" data-testid="personal-convert-user" @select="pick" />
    </SField>
    <template v-if="target">
      <SAlert tone="accent" icon="user" data-testid="personal-convert-intro">{{ t('segments.convert.intro', { from }) }}</SAlert>
      <SChoiceCards :model-value="copy" :cols="1" :options="choices" :aria-label="t('segments.convert.startFrom')" data-testid="personal-convert-start" @update:model-value="copy = $event === true" />
    </template>
  </SFormModal>
</template>
