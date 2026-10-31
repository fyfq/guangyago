import type { PortalShare } from './api'
import { toast } from './toast'

export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 局域网 http 不是安全上下文，或用户拒绝了剪贴板权限，退回旧接口
    }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none'
  document.body.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}

export async function copyShare(s: PortalShare): Promise<boolean> {
  const text = s.code ? `${s.url}\n提取码：${s.code}` : s.url
  if (await copyText(text)) {
    toast.success('链接已复制')
    return true
  }
  toast.error('复制失败，请重试或点击打开链接')
  return false
}
