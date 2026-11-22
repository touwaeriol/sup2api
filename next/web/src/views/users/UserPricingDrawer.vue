<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SDrawer, SHint, SLink, SSectionTitle, SSpinner, toast, useTabbedForm } from '@sub2api/ui'
import type { Segment } from '@/api/pricing'
import type { User } from '@/api/types'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import { canSimulatePrices } from '@/components/pricing/quoteSubjects'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { useTabbedSubmit } from '@/composables/useTabbedSubmit'
import { useAuthStore } from '@/stores/auth'
import SegmentGroupsEditor from '@/components/pricing/SegmentGroupsEditor.vue'
import UserSegmentPanel from './UserSegmentPanel.vue'

// The account group rates of one user: the user group ("segment") he belongs
// to and what it gives him in every account group. The settings of a named
// one are every member's, so they are shown read-only; those of a personal
// one are edited here as drafts and saved in one request (PATCH
// /segments/:id) with the button in the footer; closing with something not
// saved asks first. `changed` tells the list that the user is in another
// user group now.
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

// The drawer has no tabs: the rates are the one part of the request (useTabbedForm).
const tabbed = useTabbedForm(ref('groups'), ['groups'])
/** Saves made: the rates load again from what the last one answered. */
const saves = ref(0)
const editable = computed(() => segment.value?.kind === 'personal' && canManage.value)
const dirty = computed(() => editable.value && tabbed.anyDirty.value)

const { submit } = useTabbedSubmit(tabbed, {
  send: (body) => api.patch<Segment>(`/segments/${segment.value!.id}`, body),
  done() {
    saves.value++
    toast(t('common.saved'), 'success')
  }
})
const save = () => segment.value && dirty.value && submit()

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
          <UserSegmentPanel :user="user" :segment="segment" :editable="canManage" @changed="onMoved" />
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
          <SegmentGroupsEditor :key="saves" :segment-id="segment.id" :editable="editable" :disabled="tabbed.saving.value" tab="groups" />
        </section>
      </template>
    </div>

    <template #footer>
      <SButton v-if="canSimulate" icon="price" class="me-auto" data-testid="user-pricing-simulate" @click="simulatorOpen = true">{{ t('users.pricing.simulate') }}</SButton>
      <SButton data-testid="user-pricing-close" @click="close">{{ t('common.close') }}</SButton>
      <SButton v-if="editable" variant="primary" :loading="tabbed.saving.value" :disabled="!dirty" data-testid="user-pricing-save" @click="save">{{ t('common.save') }}</SButton>
    </template>
  </SDrawer>

  <PriceSimulatorModal v-if="canSimulate" v-model:open="simulatorOpen" :preset="{ userId: user?.id }" />
</template>
