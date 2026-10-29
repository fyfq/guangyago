<script setup lang="ts">
import { cn } from '../../lib/utils'

const model = defineModel<string>({ default: '' })
const props = defineProps<{ class?: string; placeholder?: string; rows?: number }>()
</script>

<template>
  <textarea
    v-model="model"
    :rows="props.rows ?? 10"
    :placeholder="props.placeholder"
    :class="
      cn(
        'w-full resize-y rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-[var(--color-surface)] px-3 py-2.5 font-mono text-[13px] leading-relaxed text-[var(--color-ink)] placeholder:text-[var(--color-faint)] focus:border-[var(--color-brand)] focus:outline-none focus:ring-2 focus:ring-[var(--color-brand-line)]',
        props.class,
      )
    "
  />
</template>
