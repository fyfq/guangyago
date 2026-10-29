<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Search,
  FileText,
  CheckCircle2,
  AlertCircle,
  ExternalLink,
  Copy,
  Loader2,
  KeyRound,
  Image as ImageIcon,
  Upload,
  Trash2,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import { api, type BrandingKind } from '../lib/api'
import { applyBranding, branding, DEFAULT_SITE_NAME } from '../lib/branding'
import { toast } from '../lib/toast'
import { safeExternalUrl } from '../lib/utils'

const loading = ref(true)
const baseUrl = ref('')
const sitemapUrl = ref('')
const robotsUrl = ref('')
const structuredDataUrl = ref('')
const indexedCount = ref(0)
const totalResources = ref(0)

// Google 站点验证
const token = ref('')
const fixedBase = ref('')
const saving = ref(false)
const checking = ref(false)
// 后端返回的原始值，用来判断是否真的改动过
const savedToken = ref('')
const savedBase = ref('')
// 抓到的原始 HTML 里是否已带上验证标签
const metaLive = ref(false)
const metaMatched = ref(false)

// 站点标识：标题走 settings，logo 与图标是上传到数据目录的素材
const siteName = ref('')
const savedSiteName = ref('')
const siteSubtitle = ref('')
const savedSiteSubtitle = ref('')
const savingName = ref(false)
const busyKind = ref<BrandingKind | null>(null)

const tokenFilled = computed(() => token.value.trim() !== '')
const metaTag = computed(
  () => `<meta name="google-site-verification" content="${token.value.trim()}" />`,
)
const dirty = computed(
  () => token.value.trim() !== savedToken.value || fixedBase.value.trim() !== savedBase.value,
)
const consoleUrl = computed(
  () => `https://search.google.com/search-console?resource_id=${encodeURIComponent(baseUrl.value + '/')}`,
)

async function loadSEOInfo() {
  loading.value = true
  try {
    // 获取当前页面 URL 作为基础 URL
    baseUrl.value = window.location.origin

    const [stats, settings] = await Promise.all([api.portalStats(), api.settings()])
    totalResources.value = stats.library_total || stats.total || 0

    const seo = settings.seo
    savedToken.value = seo?.google_verification || ''
    savedBase.value = seo?.base_url || ''
    savedSiteName.value = seo?.site_name || ''
    token.value = savedToken.value
    fixedBase.value = savedBase.value
    siteName.value = savedSiteName.value
    savedSiteSubtitle.value = seo?.site_subtitle ?? '资源库'
    siteSubtitle.value = savedSiteSubtitle.value

    // sitemap 里的域名以服务端配置为准，否则展示的地址可能和实际提交的不一致
    const root = seo?.base_url || window.location.origin
    sitemapUrl.value = `${root}/sitemap.xml`
    robotsUrl.value = `${root}/robots.txt`
    structuredDataUrl.value = `${root}/api/structured-data`

    // 获取 sitemap 内容来统计已索引数量
    try {
      const res = await fetch(sitemapUrl.value)
      const text = await res.text()
      // 简单统计 <url> 标签数量
      const matches = text.match(/<url>/g)
      indexedCount.value = matches ? matches.length : 0
    } catch {
      indexedCount.value = 0
    }
    await checkMeta()
  } catch (e) {
    console.error('加载 SEO 信息失败', e)
  } finally {
    loading.value = false
  }
}

// checkMeta 抓一次首页原始 HTML，确认验证标签真的能被爬虫读到
async function checkMeta() {
  checking.value = true
  try {
    const res = await fetch(`${fixedBase.value || window.location.origin}/`, { cache: 'no-store' })
    const html = await res.text()
    const m = html.match(/google-site-verification"\s+content="([^"]+)"/)
    metaLive.value = !!m
    metaMatched.value = !!m && !!token.value.trim() && m[1] === token.value.trim()
  } catch {
    metaLive.value = false
    metaMatched.value = false
  } finally {
    checking.value = false
  }
}

async function saveSEO() {
  const t = token.value.trim()
  if (t && !/^[A-Za-z0-9_-]{10,120}$/.test(t)) {
    toast.error('验证码只含字母、数字、下划线和短横线，长度 10-120')
    return
  }
  const base = fixedBase.value.trim().replace(/\/+$/, '')
  if (base && !/^https?:\/\//.test(base)) {
    toast.error('站点根地址需要以 http:// 或 https:// 开头')
    return
  }
  saving.value = true
  try {
    await api.saveSettings({
      seo: { google_verification: t, base_url: base },
      save_to_file: true,
    })
    savedToken.value = t
    savedBase.value = base
    toast.success('SEO 配置已保存')
    await Promise.all([checkMeta(), loadSEOInfo()])
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '保存失败')
  } finally {
    saving.value = false
  }
}

const siteNameDirty = computed(() => siteName.value.trim() !== savedSiteName.value || siteSubtitle.value.trim() !== savedSiteSubtitle.value)

// 两类素材结构一样，用同一张卡片渲染，尺寸上限与后端 branding.go 保持一致
const assets = computed(() => [
  { kind: 'logo' as BrandingKind, label: 'Logo', hint: '门户与后台头部；透明底，≤512 KB', url: branding.logo_url },
  { kind: 'icon' as BrandingKind, label: '站点图标', hint: '浏览器标签页；正方形，≤256 KB', url: branding.icon_url },
])

async function saveSiteName() {
  const name = siteName.value.trim()
  savingName.value = true
  try {
    await api.saveSettings({ seo: { site_name: name, site_subtitle: siteSubtitle.value.trim() }, save_to_file: true })
    savedSiteName.value = name
    savedSiteSubtitle.value = siteSubtitle.value.trim()
    applyBranding(await api.branding())
    toast.success('站点信息已保存')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '保存失败')
  } finally {
    savingName.value = false
  }
}

// 上传与清除的回包里就是最新品牌信息，直接覆盖全站状态，不必刷新页面
async function uploadAsset(kind: BrandingKind, event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  busyKind.value = kind
  try {
    applyBranding(await api.uploadBranding(kind === 'logo' ? { logo: file } : { icon: file }))
    toast.success(kind === 'logo' ? 'Logo 已更新' : '站点图标已更新')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '上传失败')
  } finally {
    // 清空 value，否则连续选同一个文件不会触发 change
    input.value = ''
    busyKind.value = null
  }
}

async function clearAsset(kind: BrandingKind) {
  busyKind.value = kind
  try {
    applyBranding(await api.clearBranding(kind))
    toast.success(kind === 'logo' ? 'Logo 已清除' : '站点图标已清除')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '清除失败')
  } finally {
    busyKind.value = null
  }
}

function copyToClipboard(text: string, label: string) {
  navigator.clipboard.writeText(text).then(() => {
    toast.success(`${label} 已复制`)
  }).catch(() => {
    toast.error('复制失败')
  })
}

const searchEngines = [
  {
    name: 'Google Search Console',
    url: 'https://search.google.com/search-console',
    desc: '提交 sitemap、查看索引状态、监控搜索表现',
  },
  {
    name: 'Bing Webmaster Tools',
    url: 'https://www.bing.com/webmasters',
    desc: 'Bing 搜索的站长工具，提交 sitemap 和监控索引',
  },
  {
    name: '百度资源平台',
    url: 'https://ziyuan.baidu.com',
    desc: '百度搜索的站长平台，提交链接和查看收录情况',
  },
]

onMounted(() => {
  document.title = `SEO 设置 · ${branding.site_name}`
  loadSEOInfo()
})
</script>

<template>
  <div class="space-y-5">
    <Card>
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <ImageIcon class="h-5 w-5 text-[var(--color-brand)]" />
          <h3 class="text-base font-medium">站点标识</h3>
          <p class="text-sm text-[var(--color-muted)]">图片上传后自动保存。</p>
        </div>
        <div class="ml-auto flex shrink-0 items-center gap-3">
          <span v-if="siteNameDirty" class="text-xs text-[var(--color-muted)]">有未保存的改动</span>
          <Button :disabled="savingName || !siteNameDirty" @click="saveSiteName">
            <Loader2 v-if="savingName" class="h-4 w-4 animate-spin" />
            保存站点信息
          </Button>
        </div>
      </div>

      <div class="grid auto-rows-fr items-stretch gap-3 sm:grid-cols-2">
        <div class="min-h-32 space-y-2 rounded-lg border border-[var(--color-line)] p-3">
          <label class="block text-sm font-medium leading-tight">站点标题</label>
          <Input v-model="siteName" :placeholder="DEFAULT_SITE_NAME" />
          <p class="text-xs leading-snug text-[var(--color-muted)]">留空保存即回到默认的「{{ DEFAULT_SITE_NAME }}」。</p>
        </div>

        <div class="min-h-32 space-y-2 rounded-lg border border-[var(--color-line)] p-3">
          <label class="block text-sm font-medium leading-tight">门户副标题</label>
          <Input v-model="siteSubtitle" placeholder="资源库" />
          <p class="text-xs leading-snug text-[var(--color-muted)]">显示在门户站点标题下方，留空隐藏。</p>
        </div>

        <div
          v-for="a in assets"
          :key="a.kind"
          class="flex min-h-32 flex-wrap items-center gap-3 rounded-lg border border-[var(--color-line)] p-3"
        >
          <div
            class="flex h-9 w-9 shrink-0 items-center justify-center overflow-hidden rounded-[var(--radius-control)] bg-[var(--color-surface-sunken)]"
          >
            <!-- branding 里的地址已在 lib/branding 限定成同源 /branding/ 路径 -->
            <img v-if="a.url" :src="a.url" :alt="a.label" class="h-full w-full object-contain" />
            <ImageIcon v-else class="h-4 w-4 text-[var(--color-faint)]" />
          </div>
          <div class="min-w-32 flex-1">
            <p class="text-sm font-medium leading-tight">{{ a.label }}</p>
            <p class="mt-0.5 text-xs leading-snug text-[var(--color-muted)]">{{ a.hint }}</p>
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-1.5">
            <label
              class="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-[var(--color-surface)] px-3 text-sm font-medium transition-colors hover:bg-[var(--color-surface-hover)]"
            >
              <input
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                class="sr-only"
                :disabled="busyKind === a.kind"
                :aria-label="`选择${a.label}文件`"
                @change="uploadAsset(a.kind, $event)"
              />
              <Loader2 v-if="busyKind === a.kind" class="h-4 w-4 animate-spin" />
              <Upload v-else class="h-4 w-4" />
              选择文件
            </label>
            <Button
              v-if="a.url"
              variant="ghost"
              size="sm"
              :disabled="busyKind === a.kind"
              :aria-label="`清除${a.label}`"
              @click="clearAsset(a.kind)"
            >
              <Trash2 class="h-4 w-4" />
            </Button>
          </div>
        </div>
      </div>


    </Card>

    <!-- 同一行的卡片按行高对齐，不再各自顶部起算 -->
    <div class="grid gap-5 lg:grid-cols-2">
    <Card class="flex h-full flex-col">
      <div class="mb-4 flex items-center gap-2">
        <KeyRound class="h-5 w-5 text-[var(--color-brand)]" />
        <h3 class="text-base font-medium">Google 站点验证</h3>
      </div>
      <p class="mb-4 text-sm text-[var(--color-muted)]">
        填写 Search Console 提供的 HTML 标签验证码，保存后检查是否生效。
      </p>

      <details class="mb-5 rounded-lg bg-[var(--color-surface-sunken)] p-3 text-xs text-[var(--color-muted)]">
        <summary class="cursor-pointer font-medium text-[var(--color-ink)]">如何获取验证码与提交站点地图</summary>
        <ol class="mt-3 space-y-2 leading-relaxed">
        <li>1. 打开 <a :href="consoleUrl" target="_blank" rel="noopener noreferrer" class="text-[var(--color-brand)] hover:underline">Search Console</a>，点击「添加资源」。</li>
        <li>2. 选择「网址前缀」并填入下面的站点根地址（选「网域」的话只能靠 DNS TXT 记录，验证码无效）。</li>
        <li>3. 验证方式选「HTML 标签」，复制 <code class="font-mono">content</code> 里的验证码。</li>
        <li>4. 粘贴到下方保存，回到 Search Console 点「验证」。</li>
        <li>5. 验证通过后，在左侧「站点地图」里提交下面这个 sitemap 地址。</li>
        </ol>
      </details>

      <div class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-[var(--color-muted)]">Google 站点验证码</label>
          <Input v-model="token" placeholder="例如：aB3dE5fG7hI9jK1lM2nO4pQ5rS6tU7vW8xY9zA0bC1d2" />
          <div v-if="tokenFilled" class="mt-2 flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="min-w-0 flex-1 truncate">{{ metaTag }}</span>
            <Button variant="ghost" size="sm" @click="copyToClipboard(metaTag, 'meta 标签')">
              <Copy class="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        <div>
          <label class="mb-1 block text-xs font-medium text-[var(--color-muted)]">站点根地址（可选）</label>
          <Input v-model="fixedBase" :placeholder="`${baseUrl}（留空则按访问请求自动推断）`" />
          <p class="mt-1 text-xs text-[var(--color-faint)]">
            反向代理或有多域名时建议固定，robots.txt 与 sitemap 里的 URL 会使用它。
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-3 border-t border-[var(--color-line)] pt-4">
          <Button :disabled="saving || !dirty" @click="saveSEO">
            <Loader2 v-if="saving" class="mr-1 h-4 w-4 animate-spin" />
            保存验证配置
          </Button>
          <Button variant="outline" :disabled="checking" @click="checkMeta">
            <Loader2 v-if="checking" class="mr-1 h-4 w-4 animate-spin" />
            重新检查
          </Button>
          <span
            class="flex w-full items-start gap-1.5 text-xs leading-relaxed"
            :class="metaMatched ? 'text-[var(--color-good)]' : 'text-[var(--color-muted)]'"
          >
            <CheckCircle2 v-if="metaMatched" class="h-3.5 w-3.5" />
            <AlertCircle v-else class="h-3.5 w-3.5" />
            <span v-if="!tokenFilled">尚未配置验证码</span>
            <span v-else-if="metaMatched">标签已生效，请到 Search Console 完成验证</span>
            <span v-else-if="dirty">有未保存的改动</span>
            <span v-else-if="metaLive">首页有验证标签，但和这里填的验证码不一致</span>
            <span v-else>首页原始 HTML 还没带上标签，若刚保存过请稍等缓存刷新</span>
          </span>
        </div>
      </div>
    </Card>



    <Card v-if="!loading">
      <div class="mb-4 flex items-center gap-2">
        <FileText class="h-5 w-5 text-[var(--color-brand)]" />
        <h3 class="text-base font-medium">站点地图与 robots.txt</h3>
      </div>

      <div class="space-y-4">
        <!-- Sitemap -->
        <div class="rounded-lg border border-[var(--color-line)] p-4">
          <div class="mb-2 flex items-center justify-between">
            <div class="flex items-center gap-2">
              <CheckCircle2 class="h-4 w-4 text-[var(--color-good)]" />
              <span class="text-sm font-medium">Sitemap 已启用</span>
            </div>
            <a :href="safeExternalUrl(sitemapUrl)" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            站点地图帮助搜索引擎发现和理解您网站的内容结构
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="min-w-0 flex-1 truncate">{{ sitemapUrl }}</span>
            <Button variant="ghost" size="sm" @click="copyToClipboard(sitemapUrl, 'Sitemap URL')">
              <Copy class="h-3.5 w-3.5" />
            </Button>
          </div>
          <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--color-muted)]">
            <span>站点地图 URL 数：<strong class="text-[var(--color-ink)]">{{ indexedCount }}</strong></span>
            <span>总资源数：<strong class="text-[var(--color-ink)]">{{ totalResources }}</strong></span>
          </div>
        </div>

        <!-- Robots.txt -->
        <div class="rounded-lg border border-[var(--color-line)] p-4">
          <div class="mb-2 flex items-center justify-between">
            <div class="flex items-center gap-2">
              <CheckCircle2 class="h-4 w-4 text-[var(--color-good)]" />
              <span class="text-sm font-medium">robots.txt 已配置</span>
            </div>
            <a :href="safeExternalUrl(robotsUrl)" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            robots.txt 文件告诉搜索引擎爬虫哪些页面可以访问
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="min-w-0 flex-1 truncate">{{ robotsUrl }}</span>
            <Button variant="ghost" size="sm" @click="copyToClipboard(robotsUrl, 'robots.txt URL')">
              <Copy class="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        <!-- 结构化数据 -->
        <div class="rounded-lg border border-[var(--color-line)] p-4">
          <div class="mb-2 flex items-center justify-between">
            <div class="flex items-center gap-2">
              <CheckCircle2 class="h-4 w-4 text-[var(--color-good)]" />
              <span class="text-sm font-medium">结构化数据已启用</span>
            </div>
            <a :href="safeExternalUrl(structuredDataUrl)" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            Schema.org 结构化数据帮助搜索引擎理解页面内容，提升搜索结果展示效果
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="min-w-0 flex-1 truncate">{{ structuredDataUrl }}</span>
            <Button variant="ghost" size="sm" @click="copyToClipboard(structuredDataUrl, '结构化数据 URL')">
              <Copy class="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </div>
    </Card>

    <Card v-else>
      <div class="flex items-center justify-center py-8">
        <Loader2 class="h-5 w-5 animate-spin text-[var(--color-muted)]" />
      </div>
    </Card>

    <Card>
      <div class="mb-4 flex items-center gap-2">
        <Search class="h-5 w-5 text-[var(--color-brand)]" />
        <h3 class="text-base font-medium">提交到搜索引擎</h3>
      </div>
      <p class="mb-4 text-sm text-[var(--color-muted)]">
        复制站点地图地址，到对应平台完成提交并查看实际收录情况。
      </p>

      <div class="space-y-3">
        <div
          v-for="engine in searchEngines"
          :key="engine.name"
          class="rounded-lg border border-[var(--color-line)] p-4 transition-colors hover:bg-[var(--color-surface-hover)]"
        >
          <div class="mb-1 flex items-center justify-between">
            <span class="text-sm font-medium">{{ engine.name }}</span>
            <a
              :href="engine.url"
              target="_blank"
              rel="noopener noreferrer"
              class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1"
            >
              访问 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="text-xs text-[var(--color-muted)]">{{ engine.desc }}</p>
        </div>
      </div>
    </Card>

    <Card>
      <div class="mb-4 flex items-center gap-2">
        <AlertCircle class="h-5 w-5 text-[var(--color-info)]" />
        <h3 class="text-base font-medium">SEO 建议</h3>
      </div>

      <div class="grid gap-3 text-sm sm:grid-cols-2">
        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">1. 提交 Sitemap</p>
          <p class="text-xs text-[var(--color-muted)]">
            在 Google Search Console 中提交您的 sitemap URL，帮助 Google 更快地发现和索引您的页面
          </p>
        </div>

        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">2. 验证站点所有权</p>
          <p class="text-xs text-[var(--color-muted)]">
            在上方「Google 站点验证」填写验证码即可让爬虫读到验证标签，验证通过后 Search Console 才会开放索引报告与 sitemap 提交
          </p>
        </div>

        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">3. 监控索引状态</p>
          <p class="text-xs text-[var(--color-muted)]">
            定期检查搜索引擎的索引报告，确保所有重要页面都被正确收录
          </p>
        </div>

        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">4. 优化内容质量</p>
          <p class="text-xs text-[var(--color-muted)]">
            持续更新高质量的资源内容，提升用户停留时间和分享率，有助于提高搜索排名
          </p>
        </div>
      </div>
    </Card>
    </div>
  </div>
</template>
