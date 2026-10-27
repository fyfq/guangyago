<script setup lang="ts">
import { cn } from '../../lib/utils'

const model = defineModel<string | number>({ default: '' })
const props = defineProps<{ class?: string; placeholder?: string; type?: string }>()
</script>

<template>
  <input
    v-model="model"
    :type="props.type ?? 'text'"
    :placeholder="props.placeholder"
    :class="
      cn(
        'h-9 w-full rounded-lg border border-[var(--color-line)] bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)] placeholder:text-stone-400 focus:border-[var(--color-brand)] focus:outline-none',
        props.class,
      )
    "
  />
</template>
