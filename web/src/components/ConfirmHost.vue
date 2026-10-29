<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { AlertTriangle } from 'lucide-vue-next'
import Button from './ui/Button.vue'
import { confirmState, settleConfirm } from '../lib/confirm'

const panel = ref<HTMLElement | null>(null)

watch(
  () => confirmState.open,
  async (open) => {
    document.body.style.overflow = open ? 'hidden' : ''
    if (!open) return
    await nextTick()
    panel.value?.focus()
  },
)

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  e.preventDefault()
  settleConfirm(false)
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="confirmState.open"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4"
      @click.self="settleConfirm(false)"
    >
      <div
        ref="panel"
        tabindex="-1"
        class="w-full max-w-sm rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] p-5 shadow-[0_12px_40px_rgba(20,20,20,0.18)] outline-none"
        role="alertdialog"
        aria-modal="true"
        :aria-label="confirmState.title"
        @keydown="onKeydown"
      >
        <div class="flex items-start gap-3">
          <div
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full"
            :class="
              confirmState.danger
                ? 'bg-[var(--color-bad-soft)] text-[var(--color-bad)]'
                : 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
            "
          >
            <AlertTriangle class="h-4 w-4" />
          </div>
          <div class="min-w-0 flex-1">
            <h3 class="text-[15px] font-medium">{{ confirmState.title }}</h3>
            <p v-if="confirmState.message" class="mt-1 text-[13px] leading-relaxed text-[var(--color-muted)]">
              {{ confirmState.message }}
            </p>
          </div>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button variant="outline" size="sm" @click="settleConfirm(false)">
            {{ confirmState.cancelText }}
          </Button>
          <Button
            size="sm"
            :variant="confirmState.danger ? 'danger' : 'default'"
            @click="settleConfirm(true)"
          >
            {{ confirmState.confirmText }}
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
