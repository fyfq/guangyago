<script setup lang="ts">
import type { Component } from 'vue'
import { CheckCircle2, AlertCircle, Info, X } from 'lucide-vue-next'
import { toasts, dismissToast, type ToastKind } from '../lib/toast'

const icon: Record<ToastKind, Component> = {
  success: CheckCircle2,
  error: AlertCircle,
  info: Info,
}

const tone: Record<ToastKind, string> = {
  success: 'text-[var(--color-ok)]',
  error: 'text-[var(--color-bad)]',
  info: 'text-[var(--color-info)]',
}
</script>

<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed bottom-5 right-5 z-50 flex w-[min(360px,calc(100vw-2.5rem))] flex-col gap-2"
      aria-live="polite"
    >
      <TransitionGroup
        enter-from-class="opacity-0 translate-y-1"
        enter-active-class="transition duration-150"
        leave-to-class="opacity-0"
        leave-active-class="transition duration-150"
      >
        <div
          v-for="t in toasts"
          :key="t.id"
          class="pointer-events-auto flex items-start gap-2 rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] px-3 py-2.5 shadow-[0_6px_20px_rgba(20,20,20,0.12)]"
        >
          <component :is="icon[t.kind]" class="mt-0.5 h-4 w-4 shrink-0" :class="tone[t.kind]" />
          <p class="min-w-0 flex-1 text-[13px] leading-relaxed break-words">{{ t.message }}</p>
          <button
            class="shrink-0 rounded p-0.5 text-[var(--color-faint)] hover:bg-[var(--color-surface-hover)]"
            aria-label="关闭提示"
            @click="dismissToast(t.id)"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
