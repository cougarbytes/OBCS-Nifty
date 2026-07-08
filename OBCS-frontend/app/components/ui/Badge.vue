<script setup lang="ts">
import { cva, type VariantProps } from 'class-variance-authority'

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary text-primary-foreground',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'text-foreground',
        gain: 'border-transparent bg-gain/15 text-gain',
        loss: 'border-transparent bg-loss/15 text-loss',
        warning: 'border-transparent bg-holiday/15 text-holiday',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)
const props = defineProps<{ variant?: VariantProps<typeof badgeVariants>['variant'] }>()
</script>

<template>
  <span :class="cn(badgeVariants({ variant: props.variant }))"><slot /></span>
</template>
