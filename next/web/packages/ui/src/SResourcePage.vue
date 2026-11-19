<script setup lang="ts" generic="T extends Record<string, any>">
// A plugin's list page over one of its routes: STable (as a card) with a
// search box (`search` names the query parameter; none without it), the
// #toolbar slot, a refresh button, a create button, and the pager; rows come from useList
// (`client` host.pluginApi, `path`). Cells take `cell-<key>` slots as in
// STable. With `canCreate` and a #create slot the button opens an
// SFormModal holding the slot ({ close, done }); its Save runs `submit`
// (validate and post there; resolving false keeps the dialog open), and a
// successful create goes back to page 1, reloads and emits `created`. A
// slot that saves by itself calls `done()`.
import { computed, ref, useSlots } from 'vue'
import { errorMessage, isSilentError, useHost, useList, type ApiClient } from '@sub2api/host'
import SButton from './SButton.vue'
import SFormModal from './SFormModal.vue'
import SInput from './SInput.vue'
import SPagination from './SPagination.vue'
import STable from './STable.vue'
import { toast } from './feedback'
import { useUiT } from './i18n'
import type { TableColumn } from './types'

const props = defineProps<{
  client: ApiClient
  path: string
  columns: TableColumn[]
  /** Query parameter of the search box; no box without it. */
  search?: string
  rowKey?: string
  createLabel?: string
  canCreate?: boolean
  /** Save of the create dialog; false keeps it open. */
  submit?: () => Promise<boolean | void>
}>()
const emit = defineEmits<{ (e: 'created'): void }>()
const slots = useSlots()
const t = useUiT()
const host = useHost()
const notify = (e: unknown) => isSilentError(e) || toast(errorMessage(e, host.i18n.t), 'error')
const list = useList<T>(props.client, () => props.path, props.search ? { [props.search]: '' } : {}, { onError: notify })
const cellSlots = computed(() => Object.keys(slots).filter((n) => n.startsWith('cell-')))
const creating = ref(false)
const saving = ref(false)

function done() {
  creating.value = false
  emit('created')
  if (list.page.value !== 1) list.page.value = 1
  else void list.reload()
}

async function save() {
  if (!props.submit) return
  saving.value = true
  try {
    if ((await props.submit()) !== false) done()
  } catch (e) {
    notify(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div>
    <STable :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :row-key="rowKey" card @retry="list.reload()">
      <template #toolbar>
        <div class="s-resource-toolbar">
          <SInput v-if="search" v-model="list.filters[search]" type="search" icon="search" :placeholder="t('ui.search')" :aria-label="t('ui.search')" class="s-resource-search" />
          <slot name="toolbar" />
          <SButton icon="refresh" :loading="list.loading.value" data-testid="resource-refresh" @click="list.reload()">{{ t('ui.refreshNow') }}</SButton>
          <SButton v-if="canCreate && $slots.create" variant="primary" class="s-resource-create" data-testid="resource-create" @click="creating = true">
            {{ createLabel || t('ui.form.create') }}
          </SButton>
        </div>
      </template>
      <template v-for="name in cellSlots" #[name]="scope"><slot :name="name" v-bind="scope" /></template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>
    <SFormModal v-if="$slots.create" v-model:open="creating" :title="createLabel || t('ui.form.create')" :loading="saving" @submit="save">
      <slot name="create" :close="() => (creating = false)" :done="done" />
    </SFormModal>
  </div>
</template>

<style scoped>
.s-resource-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.s-resource-search {
  width: 16rem;
  max-width: 100%;
}
.s-resource-create {
  margin-left: auto;
}
</style>
