<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SField, SFormModal, SHint, SInput, STabbedForm, STextarea, toast, useTabbedForm, type FormTab } from '@sub2api/ui'
import type { Segment } from '@/api/pricing'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import SegmentGroupsEditor from '@/components/pricing/SegmentGroupsEditor.vue'
import { SEGMENT_FIELDS, formChanged, formOf, segmentBody } from '@/components/pricing/segmentForm'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'

// Creates or edits a user group ("segment"). A named one has two tabs: what it
// is, and its account group rates (with model rates under each account
// group). A personal one (the segment of a single user) has only the rates,
// under its user's email, and `remove` asks the opener to move the user back
// to a named one. Every tab can be filled in at once, a new group's too: Save
// sends the form and the rates in one request (useTabbedForm), and a field
// that is wrong marks its tab and opens it. Users are put in a user group on
// the users page. Closing with something not saved asks first. `readonly`
// only shows the segment, to a caller who may not change it.
const TABS = ['basic', 'groups'] as const
export type SegmentEditorTab = (typeof TABS)[number]
const props = defineProps<{ open: boolean; segment: Segment | null; tab?: SegmentEditorTab; readonly?: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'changed'): void; (e: 'remove', segment: Segment): void }>()
const { t } = useI18n()

const form = reactive(formOf(null))
const errors = ref<Record<string, string>>({})
const shown = ref<string>('basic')
const personal = computed(() => props.segment?.kind === 'personal')
const auth = useAuthStore()
// A saved segment answers with its own account groups (segment:read); one not created yet has to
// list them all (GET /groups), which takes group:read. Without it they are set once it is saved.
const showsGroups = computed(() => props.segment !== null || auth.has('group:read'))
const tabKeys = computed<SegmentEditorTab[]>(() => (personal.value ? ['groups'] : TABS.filter((key) => key !== 'groups' || showsGroups.value)))
const tabs = computed<FormTab[]>(() => tabKeys.value.map((key) => ({ key, label: t(`segments.tabs.${key}`) })))
const tabbed = useTabbedForm(shown, tabKeys)
const locked = computed(() => props.readonly || tabbed.saving.value)

tabbed.register({
  tab: 'basic',
  fields: SEGMENT_FIELDS,
  dirty: () => !personal.value && formChanged(form, props.segment),
  check() {
    errors.value = !personal.value && !form.name.trim() ? { name: t('common.required') } : {}
    return !errors.value.name
  },
  body: () => (personal.value ? {} : segmentBody(form, props.segment)),
  applyErrors: (e) => (errors.value = { ...errors.value, ...e })
})

watch(
  () => props.open,
  (v) => {
    if (!v) return
    Object.assign(form, formOf(props.segment))
    errors.value = {}
    tabbed.clearErrors()
    shown.value = props.tab && tabKeys.value.includes(props.tab) ? props.tab : tabKeys.value[0]
  },
  { immediate: true }
)

const title = computed(() => {
  if (!props.segment) return t('segments.create')
  return t(props.readonly ? 'segments.viewTitle' : 'segments.editTitle', { name: props.segment.name })
})

/** One request (SegmentWrite): the user group with its account group rates and model rates. */
const send = (body: Record<string, unknown>) => (props.segment ? api.patch<Segment>(`/segments/${props.segment.id}`, body) : api.post<Segment>('/segments', body))

async function submit() {
  try {
    if ((await tabbed.submit(send)) === false) return
  } catch (e) {
    return notifyError(e)
  }
  emit('changed')
  toast(t(props.segment ? 'common.updated' : 'segments.created'), 'success')
  emit('update:open', false)
}

const leave = async () => (await confirmDiscard(tabbed.anyDirty.value)) && emit('update:open', false)
</script>

<template>
  <SFormModal
    :open="open"
    :title="title"
    width="2xl"
    :loading="tabbed.saving.value"
    :submit-text="segment ? t('common.save') : t('common.create')"
    :submit-attrs="{ 'data-testid': 'segment-editor-submit' }"
    :enter-submit="false"
    data-testid="segment-editor"
    @update:open="leave"
    @submit="submit"
  >
    <template v-if="personal && segment" #header>
      <h3 class="flex min-w-0 items-center gap-2 text-base font-semibold text-fg" data-testid="segment-editor-personal">
        <span class="truncate">{{ segment.user?.email ?? segment.name }}</span>
        <SegmentBadge :segment="segment" class="shrink-0" />
      </h3>
    </template>
    <SAlert v-if="personal && segment" tone="accent" icon="user" data-testid="segment-editor-notice">
      {{ segment.origin ? t('segments.personal.noticeOrigin', { origin: segment.origin.name }) : t('segments.personal.notice') }}
      <template v-if="!readonly" #actions>
        <SButton size="sm" data-testid="segment-editor-remove" @click="emit('remove', segment)">{{ t('segments.personal.remove') }}</SButton>
      </template>
    </SAlert>
    <!-- Every tab stays mounted while the dialog is open, so the rates keep their drafts. The least height keeps the dialog from collapsing on a short tab. -->
    <STabbedForm v-if="open" v-model="shown" :tabs="tabs" :form="tabbed" :aria-label="title" class="min-h-[20rem]" data-testid="segment-editor-tabs">
      <template #default="{ tab: key }">
        <div v-if="key === 'basic'" class="space-y-4">
          <SField :label="t('common.name')" required :hint="segment?.is_default ? t('segments.defaultName') : undefined" :error="errors.name">
            <SInput v-model="form.name" :disabled="locked || segment?.is_default" data-testid="segment-name" />
          </SField>
          <SField :label="t('segments.note')" :error="errors.description">
            <STextarea v-model="form.description" :rows="2" :disabled="locked" data-testid="segment-description" />
          </SField>
        </div>
        <SegmentGroupsEditor v-else :segment-id="segment?.id ?? null" :editable="!readonly" :disabled="tabbed.saving.value" tab="groups" />
      </template>
    </STabbedForm>
    <template v-if="!segment" #footer-start>
      <SHint size="xs" data-testid="segment-editor-unsaved">{{ t('segments.unsavedNew') }}</SHint>
    </template>
    <template v-if="readonly" #footer>
      <SButton variant="primary" data-testid="segment-editor-close" @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
    </template>
  </SFormModal>
</template>
