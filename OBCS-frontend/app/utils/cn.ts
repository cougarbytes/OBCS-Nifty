import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** cn merges conditional class names and resolves Tailwind conflicts
 *  (the shadcn-vue convention). Auto-imported by Nuxt from utils/. */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
