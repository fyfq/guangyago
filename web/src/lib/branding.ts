import { reactive } from 'vue'
import { api, type Branding } from './api'

/** 站点没配标题时的兜底值，与后端 defaultSiteName 保持一致 */
export const DEFAULT_SITE_NAME = 'gygo'

export const branding = reactive<Branding>({
  site_name: DEFAULT_SITE_NAME,
  site_subtitle: '资源库',
  logo_url: '',
  icon_url: '',
})

/**
 * 素材地址只放行服务端下发的 /branding/ 同源路径：
 * 配置文件被改成外部地址或伪协议时直接忽略，绑到 :src 前不必再过协议白名单。
 */
function safeBrandingUrl(url: string): string {
  return url.startsWith('/branding/') ? url : ''
}

/** 应用一份品牌信息；设置页上传/清除后直接用它刷新全站状态 */
export function applyBranding(next: Branding): void {
  branding.site_name = next.site_name.trim() || DEFAULT_SITE_NAME
  branding.site_subtitle = next.site_subtitle
  branding.logo_url = safeBrandingUrl(next.logo_url)
  branding.icon_url = safeBrandingUrl(next.icon_url)
  applyIcon(branding.icon_url)
}

/** 后台换图标后当前标签页立刻跟着变；原始 HTML 里服务端已经注入过一份，这里只兜住热更新 */
function applyIcon(href: string): void {
  const existing = document.head.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!href) {
    existing?.remove()
    return
  }
  const link = existing ?? document.createElement('link')
  link.rel = 'icon'
  link.href = href
  if (!existing) document.head.appendChild(link)
}

let pending: Promise<void> | null = null

/** 全站只取一次品牌信息，后续视图直接用 branding 里的响应式值 */
export function loadBranding(): Promise<void> {
  pending ??= api
    .branding()
    .then(applyBranding)
    .catch(() => {
      // 品牌信息取不到就用内置默认，不该拖垮页面主体内容
    })
  return pending
}
