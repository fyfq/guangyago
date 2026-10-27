<script setup lang="ts">
import { cn } from '../../lib/utils'

const props = defineProps<{ class?: string }>()
</script>

<template>
  <span
    :class="
      cn(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
        props.class,
      )
    "
  >
    <slot />
  </span>
</template>
