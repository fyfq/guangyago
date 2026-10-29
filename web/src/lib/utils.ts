import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// safeExternalUrl 只放行 http/https 协议，其余（javascript:/data:/vbscript: 等）
// 一律返回空串。用户从 CSV 或手动导入的链接字段直接绑到 :href 时会被 XSS 利用，
// 这里作为最后一道协议白名单。
export function safeExternalUrl(raw: unknown): string {
  if (typeof raw !== 'string') return ''
  const s = raw.trim()
  if (!s) return ''
  try {
    const u = new URL(s, window.location.origin)
    if (u.protocol === 'http:' || u.protocol === 'https:') return u.toString()
  } catch {
    // 相对路径 / 非法 URL 都视为不安全
  }
  return ''
}
