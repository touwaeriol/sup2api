<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SField, SFormModal, SGrid, SHint, SInput, STabs, STextarea, toast, type TabItem } from '@sub2api/ui'
import type { User } from '@/api/types'
import type { Segment } from '@/api/pricing'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import SegmentDiscountField from '@/components/pricing/SegmentDiscountField.vue'
import SegmentGroupsEditor from '@/components/pricing/SegmentGroupsEditor.vue'
import { hasDiscount } from '@/components/pricing/pricingFormat'
import { formChanged, formOf, segmentBody, validDiscount } from '@/components/pricing/segmentForm'
import { canListMembers, moveToSegment } from '@/components/pricing/segmentMembers'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { markUnsaved, useTabbedSave, type SaveStep } from '@/composables/useTabbedSave'
import { applyFormError } from '@/utils/formErrors'
import SegmentMembers from './SegmentMembers.vue'

// Creates or edits a segment. A named one has three tabs: what it is, its rate
// in every group, and who is in it (for a caller who may list its members:
// they are users). A personal one (the segment of a single user) has no name
// and no members to edit: its discount sits above its groups, and `remove`
// asks the opener to move the user back to a named segment. Saving sends the
// form, then the groups' drafts, then (of a new segment) the members picked
// for it; a step that fails keeps the dialog open on its tab, and what was
// created is not created again. The members of a saved segment change at
// once. `changed` says the opener's list is out of date. Closing with
// something not saved asks first. `readonly` only shows the segment, to a
// caller who may not change it.
const TABS = ['basic', 'groups', 'members'] as const
export type SegmentEditorTab = (typeof TABS)[number]
const props = defineProps<{ open: boolean; segment: Segment | null; tab?: SegmentEditorTab; readonly?: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'changed'): void; (e: 'remove', segment: Segment): void }>()
const { t } = useI18n()

/** The segment being edited: the opener's, or the one created by an attempt that failed after it. */
const current = ref<Segment | null>(null)
const form = reactive(formOf(null))
const errors = ref<Record<string, string>>({})
/** The members picked for a new segment, added once it is created. */
const pending = ref<User[]>([])
/** The tab in front; `tab` is the one the opener asks for. */
const shown = ref<SegmentEditorTab>('basic')
const groupsEditor = ref<InstanceType<typeof SegmentGroupsEditor>>()

const personal = computed(() => props.segment?.kind === 'personal')
/** The tab the form's fields are on: a personal segment has only its discount, above its groups. */
const formTab = computed<SegmentEditorTab>(() => (personal.value ? 'groups' : 'basic'))
const showsMembers = canListMembers()
/** The tabs of the segment shown. */
const tabKeys = computed<SegmentEditorTab[]>(() => {
  if (personal.value) return [formTab.value]
  return TABS.filter((key) => key !== 'members' || showsMembers)
})

watch(
  () => props.open,
  (v) => {
    if (!v) return
    current.value = props.segment
    Object.assign(form, formOf(props.segment))
    errors.value = {}
    pending.value = []
    shown.value = props.tab && tabKeys.value.includes(props.tab) ? props.tab : tabKeys.value[0]
  },
  { immediate: true }
)

const title = computed(() => {
  if (!props.segment) return t('segments.create')
  return t(props.readonly ? 'segments.viewTitle' : 'segments.editTitle', { name: props.segment.name })
})
const discount = computed(() => form.discount.trim())
const formDirty = computed(() => formChanged(form, current.value))
const unsaved = (key: string) => (key === formTab.value && formDirty.value) || (key === 'groups' && groupsEditor.value?.dirty) || (key === 'members' && pending.value.length > 0)
const tabLabel = (key: SegmentEditorTab) => (key === 'members' && pending.value.length ? t('segments.tabs.membersPicked', { n: pending.value.length }) : t(`segments.tabs.${key}`))
const tabs = computed<TabItem[]>(() => markUnsaved(tabKeys.value.map((key) => ({ key, label: tabLabel(key) })), unsaved))

/** Leaves without saving: what was typed for a new segment counts as much as a change to a saved one. */
const leave = async () => (await confirmDiscard(formDirty.value || groupsEditor.value?.dirty || pending.value.length > 0)) && emit('update:open', false)

// ------------------------------------------------------------------ saving

const { saving, run } = useTabbedSave(shown)
/** The fields hold still while they are saved: the answer replaces what they show. */
const locked = computed(() => props.readonly || saving.value)
/** The form was saved by the current attempt, whatever happens after it. */
let formSaved = false

function validate(): boolean {
  errors.value = {}
  if (!personal.value && !form.name.trim()) errors.value.name = t('common.required')
  if (!validDiscount(discount.value)) errors.value.discount = t('segments.discount.invalid')
  return !Object.keys(errors.value).length
}

/** Checks and sends the form. False when it was refused: the errors are next to the fields. */
async function saveForm(): Promise<boolean> {
  if (!validate()) return false
  const body = segmentBody(form, personal.value)
  try {
    const saved = current.value ? await api.patch<Segment>(`/segments/${current.value.id}`, body) : await api.post<Segment>('/segments', body)
    current.value = saved
    Object.assign(form, formOf(saved))
  } catch (e) {
    applyFormError(e, errors)
    return false
  }
  formSaved = true
  return true
}

/** Adds the members picked for a new segment; they leave the list once they are in. */
async function saveMembers(): Promise<boolean> {
  if (!current.value || !pending.value.length) return true
  await moveToSegment(current.value.id, pending.value.map((u) => u.id))
  pending.value = []
  return true
}

/** What Save does, in order: the groups' rates are answered under the discount the form has just saved. */
const steps: SaveStep<SegmentEditorTab>[] = [
  { tab: () => formTab.value, save: saveForm },
  { tab: 'groups', save: async () => (await groupsEditor.value?.save(current.value?.id ?? null)) ?? true },
  { tab: 'members', save: saveMembers }
]

async function submit() {
  formSaved = false
  const done = await run(steps)
  if (formSaved) emit('changed')
  if (!done) return
  toast(t(props.segment ? 'common.updated' : 'segments.created'), 'success')
  emit('update:open', false)
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="title"
    width="2xl"
    :loading="saving"
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
    <STabs v-model="shown" :tabs="tabs" :aria-label="title">
      <template #default="{ active }">
        <!-- The least height keeps the dialog from collapsing on a short tab. -->
        <div class="min-h-[20rem]">
          <div v-show="active === 'basic'" class="space-y-4">
            <SGrid>
              <SField :label="t('common.name')" required :hint="current?.is_default ? t('segments.defaultName') : undefined" :error="errors.name">
                <SInput v-model="form.name" :disabled="locked || current?.is_default" data-testid="segment-name" />
              </SField>
              <SegmentDiscountField v-if="!personal" v-model="form.discount" :error="errors.discount" :disabled="locked" />
            </SGrid>
            <SField :label="t('segments.note')" :error="errors.description">
              <STextarea v-model="form.description" :rows="2" :disabled="locked" data-testid="segment-description" />
            </SField>
          </div>
          <div v-show="active === 'groups'" class="space-y-3">
            <SegmentDiscountField v-if="personal" v-model="form.discount" :error="errors.discount" :disabled="locked" />
            <p v-else class="text-sm text-fg-muted" data-testid="segment-editor-discount">
              <template v-if="validDiscount(discount) && hasDiscount({ discount })">
                {{ t('segments.groups.discount') }} <b class="font-mono font-medium text-fg">×{{ Number(discount) }}</b>
                {{ t('segments.groups.discountHint', { discount: Number(discount) }) }}
              </template>
              <template v-else>{{ t('segments.groups.noDiscount') }}</template>
            </p>
            <SegmentGroupsEditor ref="groupsEditor" :segment-id="segment?.id ?? null" :discount="discount" :editable="!readonly" :disabled="saving" />
          </div>
          <SegmentMembers v-if="active === 'members'" v-model:pending="pending" :segment="segment" :name="form.name" @changed="emit('changed')" />
        </div>
      </template>
    </STabs>
    <template v-if="!segment" #footer-start>
      <SHint size="xs" data-testid="segment-editor-unsaved">{{ t('segments.unsavedNew') }}</SHint>
    </template>
    <template v-if="readonly" #footer>
      <SButton variant="primary" data-testid="segment-editor-close" @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
    </template>
  </SFormModal>
</template>
