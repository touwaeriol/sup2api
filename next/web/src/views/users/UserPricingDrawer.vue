<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SDrawer, SHint, SLink, SSectionTitle, SSpinner, toast } from '@sub2api/ui'
import type { Segment } from '@/api/pricing'
import type { User } from '@/api/types'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import { canSimulatePrices } from '@/components/pricing/quoteSubjects'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import SegmentGroupsEditor from '@/components/pricing/SegmentGroupsEditor.vue'
import { validDiscount } from '@/components/pricing/segmentForm'
import UserSegmentPanel from './UserSegmentPanel.vue'

// The group rates of one user: the segment he belongs to and what it gives
// him in every group. The settings of a named segment are every member's, so
// they are shown read-only; those of a personal segment are edited here and
// saved with the button in the footer; closing with something not saved asks
// first. `changed` tells the list that the user is in another segment now.
const props = defineProps<{ open: boolean; user: User | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'changed'): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('segment:manage'))
const canSimulate = computed(() => canSimulatePrices(auth))

// ------------------------------------------------------------------ the user's segment

// Asked for by user: the list row is out of date as soon as he is moved from here.
// A core older than user groups (in a rolling update) names none: that is an error to show, not a wait.
async function segmentOf(userId: number): Promise<Segment> {
  const { segment: own } = await api.get<User>(`/users/${userId}`)
  if (!own) throw new Error(t('users.pricing.noSegment'))
  return api.get<Segment>(`/segments/${own.id}`)
}
const revision = ref(0)
const { result: segment, error } = useLatestRequest(
  () => (props.open && props.user ? { id: props.user.id, revision: revision.value } : null),
  ({ id }) => segmentOf(id),
  0
)

function onMoved() {
  revision.value++
  emit('changed')
}

// ------------------------------------------------------------------ personal settings

const editor = ref<InstanceType<typeof SegmentGroupsEditor>>()
const discount = ref('1')
const saving = ref(false)
watch(segment, (s) => {
  discount.value = s?.discount ?? '1'
})

const editable = computed(() => segment.value?.kind === 'personal' && canManage.value)
const discountChanged = computed(() => discount.value.trim() !== segment.value?.discount)
const dirty = computed(() => editable.value && (discountChanged.value || !!editor.value?.dirty))
const savable = computed(() => dirty.value && validDiscount(discount.value.trim()))

async function save() {
  const s = segment.value
  if (!s || !savable.value) return
  saving.value = true
  try {
    // The discount first: what the editor's save answers is then rated by it.
    if (discountChanged.value) segment.value = await api.patch<Segment>(`/segments/${s.id}`, { discount: discount.value.trim() })
    if (await editor.value?.save()) toast(t('common.saved'), 'success')
  } catch (e) {
    notifyError(e)
  } finally {
    saving.value = false
  }
}

const close = async () => (await confirmDiscard(dirty.value)) && emit('update:open', false)

const simulatorOpen = ref(false)
</script>

<template>
  <SDrawer :open="open" width="xl" @update:open="close">
    <template #header>
      <div class="flex min-w-0 items-center gap-2">
        <h3 class="truncate text-base font-semibold text-fg">{{ user?.email }}</h3>
        <SegmentBadge :segment="segment ?? user?.segment" class="shrink-0" />
      </div>
      <p class="mt-0.5 truncate text-xs leading-5 text-fg-muted">{{ [user?.display_name, t('users.pricing.action')].filter(Boolean).join(' · ') }}</p>
    </template>

    <div class="space-y-6" data-testid="user-pricing-drawer">
      <SHint v-if="error" tone="danger" role="alert">{{ error }}</SHint>
      <SSpinner v-else-if="!user || !segment" block />
      <template v-else>
        <section>
          <SSectionTitle :title="t('users.pricing.membership')" />
          <UserSegmentPanel v-model:discount="discount" :user="user" :segment="segment" :editable="canManage" @changed="onMoved" />
        </section>
        <section>
          <SSectionTitle :title="t('users.pricing.groups')" />
          <SAlert v-if="segment.kind === 'named'" tone="neutral" dense class="mb-3" data-testid="user-pricing-inherited">
            {{ t('users.pricing.inherited', { segment: segment.name }) }}
            <template v-if="canManage">{{ t('users.pricing.inheritedEdit') }}</template>
            <template #actions>
              <SLink :to="{ path: '/segments', query: { edit: segment.id } }" data-testid="user-pricing-segment-link">{{ t(canManage ? 'users.pricing.editSegment' : 'users.pricing.viewSegment') }}</SLink>
            </template>
          </SAlert>
          <SegmentGroupsEditor ref="editor" :segment-id="segment.id" :editable="editable" :disabled="saving" :discount="discount" />
        </section>
      </template>
    </div>

    <template #footer>
      <SButton v-if="canSimulate" icon="price" class="me-auto" data-testid="user-pricing-simulate" @click="simulatorOpen = true">{{ t('users.pricing.simulate') }}</SButton>
      <SButton data-testid="user-pricing-close" @click="close">{{ t('common.close') }}</SButton>
      <SButton v-if="editable" variant="primary" :loading="saving" :disabled="!savable" data-testid="user-pricing-save" @click="save">{{ t('common.save') }}</SButton>
    </template>
  </SDrawer>

  <PriceSimulatorModal v-if="canSimulate" v-model:open="simulatorOpen" :preset="{ userId: user?.id }" />
</template>
