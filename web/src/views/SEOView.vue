<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Search,
  Globe,
  FileText,
  CheckCircle2,
  AlertCircle,
  ExternalLink,
  Copy,
  Loader2,
  KeyRound,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import { api } from '../lib/api'
import { toast } from '../lib/toast'

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
    token.value = savedToken.value
    fixedBase.value = savedBase.value

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
  document.title = 'SEO 设置 · 光鸭 GO'
  loadSEOInfo()
})
</script>

<template>
  <div class="space-y-5">
    <Card>
      <div class="mb-4 flex items-center gap-2">
        <Globe class="h-5 w-5 text-[var(--color-brand)]" />
        <h2 class="text-base font-medium">搜索引擎优化</h2>
      </div>
      <p class="text-sm text-[var(--color-muted)]">
        配置和管理站点的 SEO 设置，帮助搜索引擎更好地索引您的内容
      </p>
    </Card>

    <Card>
      <div class="mb-4 flex items-center gap-2">
        <KeyRound class="h-5 w-5 text-[var(--color-brand)]" />
        <h3 class="text-base font-medium">录入 Google（站点所有权验证）</h3>
      </div>
      <p class="mb-4 text-sm text-[var(--color-muted)]">
        在 Google Search Console 添加资源后，把「HTML 标签」方式给出的验证码填在这里。
        服务端会把它注入首页原始 HTML，Google 抓取后即可确认站点归您所有，随后就能提交 sitemap、查看收录报告。
      </p>

      <ol class="mb-5 space-y-2 rounded-lg bg-[var(--color-surface-sunken)] p-4 text-xs leading-relaxed">
        <li>1. 打开 <a :href="consoleUrl" target="_blank" rel="noopener noreferrer" class="text-[var(--color-brand)] hover:underline">Search Console</a>，点击「添加资源」。</li>
        <li>2. 选择「网址前缀」并填入下面的站点根地址（选「网域」的话只能靠 DNS TXT 记录，验证码无效）。</li>
        <li>3. 验证方式选「HTML 标签」，复制 <code class="font-mono">content</code> 里的验证码。</li>
        <li>4. 粘贴到下方保存，回到 Search Console 点「验证」。</li>
        <li>5. 验证通过后，在左侧「站点地图」里提交下面这个 sitemap 地址。</li>
      </ol>

      <div class="space-y-4">
        <div>
          <label class="mb-1 block text-xs font-medium text-[var(--color-muted)]">Google 站点验证码</label>
          <Input v-model="token" placeholder="例如：aB3dE5fG7hI9jK1lM2nO4pQ5rS6tU7vW8xY9zA0bC1d2" />
          <div v-if="tokenFilled" class="mt-2 flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="flex-1 truncate">{{ metaTag }}</span>
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

        <div class="flex flex-wrap items-center gap-3">
          <Button :disabled="saving" @click="saveSEO">
            <Loader2 v-if="saving" class="mr-1 h-4 w-4 animate-spin" />
            保存
          </Button>
          <Button variant="outline" :disabled="checking" @click="checkMeta">
            <Loader2 v-if="checking" class="mr-1 h-4 w-4 animate-spin" />
            重新检查
          </Button>
          <span
            class="flex items-center gap-1 text-xs"
            :class="metaMatched ? 'text-[var(--color-good)]' : 'text-[var(--color-muted)]'"
          >
            <CheckCircle2 v-if="metaMatched" class="h-3.5 w-3.5" />
            <AlertCircle v-else class="h-3.5 w-3.5" />
            <span v-if="!tokenFilled">尚未配置验证码</span>
            <span v-else-if="metaMatched">验证标签已在首页生效，可以去 Search Console 点验证了</span>
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
            <a :href="sitemapUrl" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            站点地图帮助搜索引擎发现和理解您网站的内容结构
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="flex-1 truncate">{{ sitemapUrl }}</span>
            <Button variant="ghost" size="sm" @click="copyToClipboard(sitemapUrl, 'Sitemap URL')">
              <Copy class="h-3.5 w-3.5" />
            </Button>
          </div>
          <div class="mt-2 flex items-center gap-4 text-xs text-[var(--color-muted)]">
            <span>已索引页面：<strong class="text-[var(--color-ink)]">{{ indexedCount }}</strong></span>
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
            <a :href="robotsUrl" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            robots.txt 文件告诉搜索引擎爬虫哪些页面可以访问
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="flex-1 truncate">{{ robotsUrl }}</span>
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
            <a :href="structuredDataUrl" target="_blank" rel="noopener noreferrer" class="text-xs text-[var(--color-brand)] hover:underline flex items-center gap-1">
              查看 <ExternalLink class="h-3 w-3" />
            </a>
          </div>
          <p class="mb-2 text-xs text-[var(--color-muted)]">
            Schema.org 结构化数据帮助搜索引擎理解页面内容，提升搜索结果展示效果
          </p>
          <div class="flex items-center gap-2 rounded bg-[var(--color-surface-sunken)] p-2 font-mono text-xs">
            <span class="flex-1 truncate">{{ structuredDataUrl }}</span>
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
        将您的站点提交到各大搜索引擎的站长平台，可以加速收录并监控搜索表现
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

      <div class="space-y-3 text-sm">
        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">1. 提交 Sitemap</p>
          <p class="text-xs text-[var(--color-muted)]">
            在 Google Search Console 中提交您的 sitemap URL，帮助 Google 更快地发现和索引您的页面
          </p>
        </div>

        <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
          <p class="font-medium mb-1">2. 验证站点所有权</p>
          <p class="text-xs text-[var(--color-muted)]">
            在上方「录入 Google」填写验证码即可让爬虫读到验证标签，验证通过后 Search Console 才会开放索引报告与 sitemap 提交
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
</template>
