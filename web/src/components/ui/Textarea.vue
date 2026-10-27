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
        'w-full resize-y rounded-lg border border-[var(--color-line)] bg-[var(--color-surface)] px-3 py-2.5 font-mono text-[13px] leading-relaxed text-[var(--color-ink)] placeholder:text-stone-400 focus:border-[var(--color-brand)] focus:outline-none',
        props.class,
      )
    "
  />
</template>
