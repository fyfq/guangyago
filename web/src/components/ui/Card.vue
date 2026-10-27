<script setup lang="ts">
import { cn } from '../../lib/utils'

const props = defineProps<{ class?: string; title?: string; desc?: string }>()
</script>

<template>
  <section
    :class="
      cn(
        'rounded-xl border border-[var(--color-line)] bg-[var(--color-surface)] p-5 shadow-[0_1px_2px_rgba(28,25,23,0.04)]',
        props.class,
      )
    "
  >
    <header v-if="props.title || $slots.title" class="mb-4">
      <slot name="title">
        <h2 class="text-[15px] font-medium">{{ props.title }}</h2>
      </slot>
      <p v-if="props.desc" class="mt-0.5 text-[13px] text-[var(--color-muted)]">{{ props.desc }}</p>
    </header>
    <slot />
  </section>
</template>
