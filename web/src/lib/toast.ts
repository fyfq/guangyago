import { reactive } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'

interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

const MAX_VISIBLE = 4
let seq = 0

export const toasts = reactive<ToastItem[]>([])

export function dismissToast(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

function push(kind: ToastKind, message: string) {
  const item = { id: ++seq, kind, message }
  toasts.push(item)
  if (toasts.length > MAX_VISIBLE) toasts.splice(0, toasts.length - MAX_VISIBLE)
  window.setTimeout(() => dismissToast(item.id), kind === 'error' ? 5000 : 3000)
}

/** 轻量提示：用于操作成功、复制、失败原因等短反馈，不阻塞操作 */
export const toast = {
  success: (message: string) => push('success', message),
  error: (message: string) => push('error', message),
  info: (message: string) => push('info', message),
}
