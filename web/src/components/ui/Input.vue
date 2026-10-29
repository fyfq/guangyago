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
        'h-9 w-full rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)] placeholder:text-[var(--color-faint)] focus:border-[var(--color-brand)] focus:outline-none focus:ring-2 focus:ring-[var(--color-brand-line)]',
        props.class,
      )
    "
  />
</template>
