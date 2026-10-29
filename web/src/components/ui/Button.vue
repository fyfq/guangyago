<script setup lang="ts">
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '../../lib/utils'

const button = cva(
  'inline-flex items-center justify-center gap-1.5 rounded-[var(--radius-control)] text-sm font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-brand)]',
  {
    variants: {
      variant: {
        default: 'bg-[var(--color-brand)] text-white hover:bg-[var(--color-brand-hover)]',
        outline: 'border border-[var(--color-line-strong)] bg-[var(--color-surface)] text-[var(--color-ink)] hover:bg-[var(--color-surface-hover)]',
        ghost: 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)]',
        danger: 'bg-[var(--color-bad)] text-white hover:brightness-90',
      },
      size: {
        sm: 'h-8 px-3',
        md: 'h-9 px-4',
        lg: 'h-10 px-5 text-[15px]',
      },
    },
    defaultVariants: { variant: 'default', size: 'md' },
  },
)

type ButtonVariants = VariantProps<typeof button>

const props = defineProps<{
  variant?: ButtonVariants['variant']
  size?: ButtonVariants['size']
  class?: string
}>()
</script>

<template>
  <button :class="cn(button({ variant: props.variant, size: props.size }), props.class)">
    <slot />
  </button>
</template>
