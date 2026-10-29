<script setup lang="ts">
import { computed } from 'vue'
import { CheckCircle2, AlertCircle, Loader2, XCircle } from 'lucide-vue-next'
import Badge from './ui/Badge.vue'
import Button from './ui/Button.vue'
import type { TaskProgress } from '../lib/api'

const props = defineProps<{
  progress: TaskProgress | null
  cancelable?: boolean
}>()

const emit = defineEmits<{ cancel: [] }>()

const percent = computed(() => {
  const p = props.progress
  if (!p || !p.total) return 0
  return Math.round(((p.done + p.failed) / p.total) * 100)
})

const canceled = computed(() => props.progress?.current === '已取消')
</script>

<template>
  <div v-if="progress && progress.total" class="rounded-lg border border-[var(--color-line)] p-3">
    <div class="flex items-center gap-2 text-[13px]">
      <Loader2 v-if="progress.running" class="h-3.5 w-3.5 shrink-0 animate-spin text-[var(--color-brand)]" />
      <CheckCircle2 v-else-if="!canceled" class="h-3.5 w-3.5 shrink-0 text-[var(--color-ok)]" />
      <XCircle v-else class="h-3.5 w-3.5 shrink-0 text-[var(--color-faint)]" />
      <span class="min-w-0 flex-1 truncate text-[var(--color-muted)]">
        {{ progress.current || '准备中' }}
      </span>
      <span class="shrink-0 font-medium">{{ progress.done + progress.failed }} / {{ progress.total }}</span>
      <Button v-if="progress.running && cancelable" variant="outline" size="sm" @click="emit('cancel')">
        停止
      </Button>
    </div>
    <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-[var(--color-surface-hover)]">
      <div
        class="h-full rounded-full transition-all duration-300"
        :class="progress.running ? 'bg-[var(--color-brand)]' : 'bg-[var(--color-ok)]'"
        :style="{ width: percent + '%' }"
      />
    </div>
    <div class="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
      <span class="flex items-center gap-1.5 text-[var(--color-ok)]">
        <CheckCircle2 class="h-3.5 w-3.5" />
        成功 {{ progress.done }}
      </span>
      <span class="flex items-center gap-1.5" :class="progress.failed ? 'text-[var(--color-bad)]' : 'text-[var(--color-faint)]'">
        <AlertCircle class="h-3.5 w-3.5" />
        失败 {{ progress.failed }}
      </span>
      <Badge v-if="progress.running" class="bg-[var(--color-info-soft)] text-[var(--color-info)]">进行中</Badge>
      <Badge v-else-if="canceled" class="bg-[var(--color-surface-hover)] text-[var(--color-muted)]">已停止</Badge>
    </div>
    <p v-if="progress.error" class="mt-2 line-clamp-2 text-[13px] text-[var(--color-bad)]">
      最近错误：{{ progress.error }}
    </p>
  </div>
</template>
