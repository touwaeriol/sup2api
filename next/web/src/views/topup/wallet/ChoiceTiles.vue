<script setup lang="ts" generic="T extends Tile<string | number>">
import { useId } from 'vue'
import type { Tile } from './types'

// A radio group drawn as tiles (the amount grid, the payment methods): the
// selected tile is outlined in the text colour, as new-api's top-up does.
// Each tile's content comes from the default slot; `title` explains a
// disabled tile on hover.

defineProps<{ modelValue: T['value'] | null | undefined; tiles: T[]; label: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: T['value']): void }>()
const name = useId()
</script>

<template>
  <div class="pay-tiles" role="radiogroup" :aria-label="label">
    <label
      v-for="tile in tiles"
      :key="tile.value"
      class="pay-tile"
      :class="{ 'pay-tile--on': tile.value === modelValue, 'pay-tile--off': tile.disabled }"
      :title="tile.title"
    >
      <input
        type="radio"
        class="pay-tile-input"
        :name="name"
        :value="tile.value"
        :checked="tile.value === modelValue"
        :disabled="tile.disabled"
        @change="emit('update:modelValue', tile.value)"
      />
      <slot :tile="tile" :checked="tile.value === modelValue" />
    </label>
  </div>
</template>

<style scoped>
.pay-tiles {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
}
@media (min-width: 768px) {
  .pay-tiles {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.75rem;
  }
}
.pay-tile {
  position: relative;
  display: flex;
  min-width: 0;
  min-height: 3.5rem;
  align-items: center;
  gap: 0.625rem;
  padding: 0.625rem 0.875rem;
  border: 1px solid rgb(var(--line));
  border-radius: 0.625rem;
  background: rgb(var(--surface));
  color: rgb(var(--fg));
  cursor: pointer;
  transition:
    border-color 0.15s,
    background-color 0.15s;
}
.pay-tile:hover {
  border-color: rgb(var(--line-strong));
}
.pay-tile--on,
.pay-tile--on:hover {
  border-color: rgb(var(--fg));
  background: rgb(var(--fg) / 0.04);
  box-shadow: inset 0 0 0 1px rgb(var(--fg));
}
.pay-tile--off {
  cursor: not-allowed;
  opacity: 0.55;
}
.pay-tile--off:hover {
  border-color: rgb(var(--line));
}
.pay-tile:has(:focus-visible) {
  outline: 2px solid rgb(var(--fg) / 0.4);
  outline-offset: 2px;
}
.pay-tile-input {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
</style>
