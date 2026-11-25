<script setup lang="ts">
// Custom logo of a payment method: an image dropped or picked (read into a
// data URL and stored inline in the settings) or an https address. A file of
// another type or over the size limit is refused before it is read; the
// form's rules check the stored value.
import { computed, ref } from 'vue'
import { SButton, SField, SIcon, SInput } from '@sub2api/ui'
import { usePaymentHost } from '../host'
import MethodIcon from '../components/MethodIcon.vue'
import { LOGO_TYPES, parseLogo } from '../components/methodIcon'
import { kb, LOGO_ACCEPT, LOGO_MAX_BYTES, logoFileError } from './settingsForm'

const props = defineProps<{ modelValue?: string; name: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const t = usePaymentHost().t

const picker = ref<HTMLInputElement>()
const dragging = ref(false)
const rejected = ref('')

const source = computed(() => parseLogo(props.modelValue))
const summary = computed(() => {
  const s = source.value
  if (!s) return ''
  return s.kind === 'url' ? t('settings.methods.logo.linked', { host: s.host }) : t('settings.methods.logo.uploaded', { type: LOGO_TYPES[s.type], size: kb(s.bytes) })
})
/** The address field shows an address, never an inline image. */
const address = computed(() => (props.modelValue?.startsWith('data:') ? '' : (props.modelValue ?? '')))

function set(v: string) {
  rejected.value = ''
  emit('update:modelValue', v)
}

function readDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

async function take(file: File | undefined) {
  if (!file) return
  const error = logoFileError(file, t)
  if (error) return void (rejected.value = error)
  try {
    set(await readDataUrl(file))
  } catch {
    rejected.value = t('settings.methods.logo.readFailed')
  }
}

function onPick(e: Event) {
  const input = e.target as HTMLInputElement
  take(input.files?.[0])
  input.value = ''
}

function onDrop(e: DragEvent) {
  dragging.value = false
  take(e.dataTransfer?.files[0])
}

// Leaving for a child of the zone is not leaving it.
function onLeave(e: DragEvent) {
  if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node | null)) dragging.value = false
}
</script>

<template>
  <div class="payment-logo">
    <!-- Messages go under the zone, which is what a refused file or a missing logo is about; the address has its own field below. -->
    <SField prop="logo" :error="rejected || undefined" :hint="t('settings.methods.logo.limits', { max: kb(LOGO_MAX_BYTES) })">
      <div
        class="payment-logo-zone"
        :class="{ 'payment-logo-zone--over': dragging, 'payment-logo-zone--filled': source}"
        data-testid="payment-logo-zone"
        @dragover.prevent="dragging = true"
        @dragleave="onLeave"
        @drop.prevent="onDrop"
      >
        <template v-if="source">
          <MethodIcon :method="{ name, logo: modelValue }" size="lg" />
          <span class="payment-logo-summary">{{ summary }}</span>
          <span class="payment-logo-actions">
            <SButton size="sm" icon="upload" @click="picker?.click()">{{ t('settings.methods.logo.replace') }}</SButton>
            <SButton size="sm" variant="ghost" icon="trash" data-testid="payment-logo-remove" @click="set('')">{{ t('settings.methods.logo.remove') }}</SButton>
          </span>
        </template>
        <button v-else type="button" class="payment-logo-drop" @click="picker?.click()">
          <SIcon name="upload" class="payment-logo-drop-icon" />
          <span>{{ dragging ? t('settings.methods.logo.dropActive') : t('settings.methods.logo.drop') }}</span>
        </button>
      </div>
      <input ref="picker" type="file" :accept="LOGO_ACCEPT" class="payment-logo-file" tabindex="-1" aria-hidden="true" data-testid="payment-logo-file" @change="onPick" />
    </SField>
    <SField :label="t('settings.methods.logo.url')" size="sm">
      <SInput :model-value="address" type="url" :placeholder="t('settings.methods.logo.urlPlaceholder')" data-testid="payment-logo-url" @update:model-value="set(String($event ?? '').trim())" />
    </SField>
  </div>
</template>
