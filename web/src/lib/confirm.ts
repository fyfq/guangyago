import { reactive } from 'vue'

interface ConfirmOptions {
  title: string
  message?: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

interface ConfirmState extends ConfirmOptions {
  open: boolean
  resolve?: (ok: boolean) => void
}

export const confirmState = reactive<ConfirmState>({
  open: false,
  title: '',
  confirmText: '确认',
  cancelText: '取消',
  danger: false,
})

/** 等待用户确认的 Promise：适合删除、批量推送这类不可撤销操作 */
export function confirmAction(opts: ConfirmOptions): Promise<boolean> {
  confirmState.resolve?.(false)
  Object.assign(confirmState, {
    message: '',
    confirmText: '确认',
    cancelText: '取消',
    danger: false,
    ...opts,
    open: true,
  })
  return new Promise<boolean>((resolve) => {
    confirmState.resolve = resolve
  })
}

export function settleConfirm(ok: boolean) {
  confirmState.resolve?.(ok)
  confirmState.resolve = undefined
  confirmState.open = false
}
