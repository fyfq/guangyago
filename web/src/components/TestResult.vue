<script setup lang="ts">
import { CheckCircle2, XCircle } from 'lucide-vue-next'
import type { TestResult } from '../lib/api'

defineProps<{ result?: TestResult }>()
</script>

<template>
  <div
    v-if="result"
    class="mt-2 flex items-start gap-2 rounded-lg px-3 py-2 text-[13px]"
    :class="
      result.ok
        ? 'bg-[var(--color-ok-soft)] text-[var(--color-ok)]'
        : 'bg-[var(--color-bad-soft)] text-[var(--color-bad)]'
    "
  >
    <CheckCircle2 v-if="result.ok" class="mt-0.5 h-4 w-4 shrink-0" />
    <XCircle v-else class="mt-0.5 h-4 w-4 shrink-0" />
    <div class="min-w-0 flex-1">
      <p class="break-words font-medium">{{ result.message }}</p>
      <p v-if="result.detail" class="mt-0.5 break-words text-xs opacity-80">{{ result.detail }}</p>
      <p v-if="result.elapsed_ms" class="mt-0.5 text-xs opacity-70">耗时 {{ result.elapsed_ms }} ms</p>
    </div>
  </div>
</template>
