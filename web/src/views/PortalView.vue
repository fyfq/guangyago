<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Cloud,
  Search,
  Share2,
  X,
  Loader2,
  Inbox,
  Copy,
  ExternalLink,
  LogIn,
  RotateCcw,
  Star,
  CalendarDays,
  AlertCircle,
  ChevronDown,
} from 'lucide-vue-next'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import { api, mediaMeta, type PortalLink, type PortalShare, type PortalStats } from '../lib/api'
import { branding } from '../lib/branding'
import { copyShare } from '../lib/clipboard'
import { toast } from '../lib/toast'
import { safeExternalUrl } from '../lib/utils'

const PAGE = 24
const items = ref<PortalLink[]>([])
const total = ref(0)
const stats = ref<PortalStats | null>(null)
const keyword = ref('')
const media = ref('')
const year = ref(0)
const sort = ref<PortalSort>('new')
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const detail = ref<PortalLink | null>(null)
// 详情弹层的焦点管理：打开时移入弹层，关闭时还原到触发元素
const detailPanel = ref<HTMLElement | null>(null)
let lastFocused: HTMLElement | null = null
// 同一个 TMDB 的多条分享在服务端已合并，详情直接列出全部链接
const detailShares = computed<PortalShare[]>(() => detail.value?.links ?? [])
const overviewExpanded = ref(false)
const overviewElement = ref<HTMLElement | null>(null)
const overviewOverflow = ref(false)
const overviewObserver = new ResizeObserver(() => {
  const el = overviewElement.value
  if (el && !overviewExpanded.value) {
    overviewOverflow.value = el.scrollHeight > el.clientHeight
  }
})
watch(overviewElement, (el) => {
  overviewObserver.disconnect()
  if (el) overviewObserver.observe(el)
}, { flush: 'post' })
watch(() => detail.value?.id, async () => {
  overviewExpanded.value = false
  overviewOverflow.value = false
  await nextTick()
  const el = overviewElement.value
  if (el) overviewOverflow.value = el.scrollHeight > el.clientHeight
})


let loadSeq = 0
let statsSeq = 0
let timer: number | undefined
let urlTimer: number | undefined

const SORTS = ['new', 'release', 'rating'] as const
type PortalSort = (typeof SORTS)[number]

// 页面状态全部映射到查询串，sitemap 里的 /?id=<n> 才有对应内容
function applyURL() {
  const p = new URLSearchParams(location.search)
  keyword.value = p.get('q') ?? ''
  const mt = p.get('media_type') ?? ''
  media.value = mt === 'movie' || mt === 'tv' ? mt : ''
  const y = Number(p.get('year'))
  year.value = Number.isInteger(y) && y > 0 ? y : 0
  const s = p.get('sort')
  sort.value = (SORTS as readonly string[]).includes(s ?? '') ? (s as PortalSort) : 'new'
  detail.value = null
  const id = Number(p.get('id'))
  if (Number.isInteger(id) && id > 0) openDeepLink(id)
}

// id 留在串上表示这是可分享的详情页；push=true 时新增一条历史，让返回键能关掉详情
function syncURL(push = false) {
  const p = new URLSearchParams()
  if (keyword.value.trim()) p.set('q', keyword.value.trim())
  if (media.value) p.set('media_type', media.value)
  if (year.value) p.set('year', String(year.value))
  if (sort.value !== 'new') p.set('sort', sort.value)
  const id = detail.value?.id
  if (id) p.set('id', String(id))
  const qs = p.toString()
  const href = qs ? `/?${qs}` : '/'
  if (push) history.pushState(null, '', href)
  else history.replaceState(null, '', href)
  syncTitle()
}

// 详情页标题与服务端注入保持一致（《片名》（年份） · 站名），SPA 内切换时前端补上
function syncTitle() {
  const site = branding.site_name
  const d = detail.value
  if (!d) {
    document.title = `${site} · 资源库`
    return
  }
  const name = d.title || '未命名资源'
  document.title = d.year ? `${name}（${d.year}） · ${site}` : `${name} · ${site}`
}

function scheduleURL() {
  window.clearTimeout(urlTimer)
  urlTimer = window.setTimeout(syncURL, 300)
}

// 浏览（进详情）、复制、打开各自计数，互不混用；失败不影响交互
function report(action: 'browse' | 'copy' | 'open', id: number) {
  const call = action === 'browse' ? api.portalBrowse : action === 'copy' ? api.portalCopy : api.portalOpen
  call(id).catch(() => {})
}

// 复制成功才计数，避免用户没拿到链接也被算一次
async function copyAndReport(s: PortalShare) {
  if (await copyShare(s)) report('copy', s.id)
}

async function openDeepLink(id: number) {
  if (detail.value?.id === id) return
  const found = items.value.find((l) => l.id === id)
  if (found) {
    detail.value = found
    return
  }
  try {
    detail.value = await api.portalLink(id)
  } catch {
    // 深链指向未收录或已下架的资源，提示后退回列表页
    toast.error('该资源不存在或已下架')
    syncURL()
  }
}

function openDetail(l: PortalLink) {
  // 点海报（整张卡片）进详情，按资源的代表分享记一次浏览
  report('browse', l.id)
  detail.value = l
  syncURL(true)
}

function closeDetail() {
  detail.value = null
  syncURL()
}

function onPopState() {
  applyURL()
}

const filters = computed(() => [
  { value: '', label: '全部', count: stats.value?.total ?? total.value },
  { value: 'movie', label: '电影', count: stats.value?.movies ?? 0 },
  { value: 'tv', label: '剧集', count: stats.value?.tv ?? 0 },
])

// 年份选项跟随搜索与分类变化；选中的年份若被筛掉也保留，避免下拉框显示为空
const yearOptions = computed(() => {
  const ys = stats.value?.years ?? []
  if (year.value && !ys.some((y) => y.year === year.value)) {
    return [{ year: year.value, count: 0 }, ...ys]
  }
  return ys
})

const hasFilter = computed(
  () => !!keyword.value.trim() || !!media.value || year.value > 0 || sort.value !== 'new',
)

function mediaLabel(l: PortalLink) {
  return mediaMeta[l.media_type]?.label ?? ''
}

function genresText(l: PortalLink) {
  return l.genres ? l.genres.replace(/[,，]/g, ' / ') : ''
}

async function load(reset = true) {
  const seq = ++loadSeq
  if (reset) {
    loading.value = true
    error.value = ''
  } else {
    loadingMore.value = true
  }
  try {
    const res = await api.portalLinks({
      q: keyword.value.trim(),
      media_type: media.value,
      year: year.value || undefined,
      sort: sort.value,
      limit: PAGE,
      offset: reset ? 0 : items.value.length,
    })
    if (seq !== loadSeq) return
    items.value = reset ? res.items : [...items.value, ...res.items]
    total.value = res.total
    if (reset) loadStats()
  } catch (e) {
    if (seq !== loadSeq) return
    error.value = e instanceof Error ? e.message : '加载失败'
    if (reset) {
      items.value = []
      total.value = 0
    }
  } finally {
    if (seq === loadSeq) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

// 筛选项上的数字要和当前搜索结果一致，所以每次列表刷新都同步重新统计
async function loadStats() {
  const seq = ++statsSeq
  try {
    const res = await api.portalStats({
      q: keyword.value.trim(),
      media_type: media.value,
      year: year.value || undefined,
    })
    if (seq === statsSeq) stats.value = res
  } catch {
    if (seq === statsSeq) stats.value = null
  }
}

function schedule(delay = 250) {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => load(), delay)
}

function resetFilters() {
  keyword.value = ''
  media.value = ''
  year.value = 0
  sort.value = 'new'
  schedule(0)
  syncURL()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    closeDetail()
    return
  }
  if (e.key !== 'Tab' || !detail.value) return
  const panel = detailPanel.value
  if (!panel) return
  // aria-modal 只描述语义，实际焦点仍会跑到背后的页面，这里手动圈住
  const focusables = Array.from(
    panel.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((el) => el.offsetParent !== null)
  const first = focusables[0]
  const last = focusables[focusables.length - 1]
  if (!first || !last) {
    e.preventDefault()
    panel.focus()
    return
  }
  const active = document.activeElement
  if (!panel.contains(active)) {
    e.preventDefault()
    first.focus()
  } else if (e.shiftKey && active === first) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && active === last) {
    e.preventDefault()
    first.focus()
  }
}

watch(keyword, () => {
  schedule(300)
  scheduleURL()
})
// 分类、年份、排序都会改变结果集，地址同步之外还要重新取一次；详情只改地址不查库
watch([media, year, sort], () => {
  schedule(0)
  scheduleURL()
})
watch(detail, scheduleURL)
// 品牌信息是异步取的，站点名到位后要把已经写好的标题跟新一次
watch(() => branding.site_name, syncTitle)

watch(detail, (d, prev) => {
  document.body.style.overflow = d ? 'hidden' : ''
  if (d && !prev) {
    lastFocused = document.activeElement as HTMLElement | null
    nextTick(() => detailPanel.value?.focus())
  } else if (!d && prev) {
    lastFocused?.focus()
    lastFocused = null
  }
  const existing = document.getElementById('structured-data')
  // 深链进来的那条资源，服务端已经把 JSON-LD 写进原始 HTML，不必再取一次
  if (d && existing?.dataset.itemId === String(d.id)) return
  existing?.remove()
  if (!d) {
    // 关掉详情回到列表，把站点级标记补回来
    injectSiteSchema()
    return
  }
  fetch(`/api/structured-data/${d.id}`)
    .then(res => res.json())
    .then(data => {
      const script = document.createElement('script')
      script.id = 'structured-data'
      script.type = 'application/ld+json'
      script.textContent = JSON.stringify(data)
      document.head.appendChild(script)
    })
    .catch(() => {})
})

// 站点级 WebSite 标记用单独的 id，避免和单条标记互相顶掉
function injectSiteSchema() {
  if (document.getElementById('site-structured-data')) return
  fetch('/api/structured-data')
    .then(res => res.json())
    .then(data => {
      const script = document.createElement('script')
      script.id = 'site-structured-data'
      script.type = 'application/ld+json'
      script.textContent = JSON.stringify(data)
      document.head.appendChild(script)
    })
    .catch(() => {})
}

onMounted(async () => {
  // 未安装时跳转到安装向导
  try {
    const status = await api.setupStatus()
    if (!status.installed) {
      location.replace('/setup')
      return
    }
  } catch {
    // 接口异常继续
  }
  const deepLinked = new URLSearchParams(location.search).has('id')
  applyURL()
  if (!deepLinked) {
    // 深链页的标题与单条标记来自服务端注入，列表页才由前端补齐
    syncTitle()
    injectSiteSchema()
  }
  window.addEventListener('popstate', onPopState)
  window.addEventListener('keydown', onKey)
  load()
})

onUnmounted(() => {
  overviewObserver.disconnect()
  window.clearTimeout(timer)
  window.clearTimeout(urlTimer)
  window.removeEventListener('popstate', onPopState)
  window.removeEventListener('keydown', onKey)
  document.body.style.overflow = ''
  // 清理结构化数据
  const existingScript = document.getElementById('structured-data')
  if (existingScript) existingScript.remove()
})
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-6 py-3.5">
        <div class="flex items-center gap-2.5">
          <!-- branding.logo_url 已在 lib/branding 里限定成同源 /branding/ 路径 -->
          <img
            v-if="branding.logo_url"
            :src="branding.logo_url"
            :alt="branding.site_name"
            class="h-8 w-8 rounded-lg object-contain"
          />
          <div v-else class="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--color-brand)]">
            <Cloud class="h-4 w-4 text-white" />
          </div>
          <div>
            <p class="text-[15px] font-medium leading-tight">{{ branding.site_name }}</p>
            <p v-if="branding.site_subtitle" class="text-xs leading-tight text-[var(--color-muted)]">{{ branding.site_subtitle }}</p>
          </div>
        </div>
        <a href="/admin" class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-3 py-2 text-[13px] text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-brand)]">
          <LogIn class="h-4 w-4" />
          登录
        </a>
      </div>
    </header>

    <section class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto max-w-7xl px-6 py-5">
        <div class="flex flex-wrap items-center gap-x-4 gap-y-3">
          <div class="flex shrink-0 items-center gap-1 whitespace-nowrap">
            <button
              v-for="f in filters"
              :key="f.value"
              class="rounded-full px-3 py-1.5 text-[13px] transition-colors"
              :class="
                media === f.value
                  ? 'bg-[var(--color-brand-soft)] font-medium text-[var(--color-brand)]'
                  : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)]'
              "
              @click="media = f.value"
            >
              {{ f.label }}
              <span class="ml-1 text-xs tabular-nums opacity-70">{{ f.count }}</span>
            </button>
          </div>

          <div class="flex max-w-full flex-wrap items-center gap-2 whitespace-nowrap">
            <div class="flex shrink-0 items-center gap-0.5 rounded-full bg-[var(--color-surface-sunken)] p-0.5">
              <button
                class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2.5 py-1.5 text-[13px] transition-colors"
                :class="
                  sort === 'new'
                    ? 'bg-[var(--color-surface)] font-medium text-[var(--color-ink)] shadow-sm'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
                "
                @click="sort = 'new'"
              >
                <Share2 class="h-3.5 w-3.5" />
                最新分享
              </button>
              <button
                class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2.5 py-1.5 text-[13px] transition-colors"
                :class="
                  sort === 'release'
                    ? 'bg-[var(--color-surface)] font-medium text-[var(--color-ink)] shadow-sm'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
                "
                @click="sort = 'release'"
              >
                <CalendarDays class="h-3.5 w-3.5" />
                上映时间
              </button>
              <button
                class="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2.5 py-1.5 text-[13px] transition-colors"
                :class="
                  sort === 'rating'
                    ? 'bg-[var(--color-surface)] font-medium text-[var(--color-ink)] shadow-sm'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
                "
                @click="sort = 'rating'"
              >
                <Star class="h-3.5 w-3.5" />
                评分最高
              </button>
            </div>

            <div class="relative">
              <select
                v-model.number="year"
                class="h-9 w-36 cursor-pointer appearance-none rounded-full border pl-3 pr-8 text-[13px] transition-colors focus:border-[var(--color-brand)] focus:outline-none"
                :class="
                  year
                    ? 'border-[var(--color-brand-line)] bg-[var(--color-brand-soft)] font-medium text-[var(--color-brand)]'
                    : 'border-[var(--color-line-strong)] bg-[var(--color-surface)] text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]'
                "
                aria-label="按上映年份筛选"
              >
                <option :value="0">全部年份</option>
                <option v-for="y in yearOptions" :key="y.year" :value="y.year">
                  {{ y.year }} 年（{{ y.count }}）
                </option>
              </select>
              <ChevronDown
                class="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--color-faint)]"
              />
            </div>

            <button
              v-if="hasFilter"
              class="flex h-8 w-8 items-center justify-center rounded-full text-[var(--color-faint)] transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)]"
              title="重置筛选"
              aria-label="重置筛选"
              @click="resetFilters"
            >
              <RotateCcw class="h-3.5 w-3.5" />
            </button>
          </div>

          <div class="relative min-w-0 flex-1 basis-full sm:basis-56">
            <Search class="pointer-events-none absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-faint)]" />
            <input
              v-model="keyword"
              class="h-9 w-full rounded-full border border-[var(--color-line-strong)] bg-[var(--color-surface)] pl-10 pr-10 text-sm text-[var(--color-ink)] placeholder:text-[var(--color-faint)] focus:border-[var(--color-brand)] focus:outline-none focus:ring-2 focus:ring-[var(--color-brand-line)]"
              placeholder="搜索片名 / 原名 / 类型"
              aria-label="搜索资源"
            />
            <button
              v-if="keyword"
              class="absolute right-3 top-1/2 -translate-y-1/2 rounded p-1 text-[var(--color-faint)] hover:text-[var(--color-ink)]"
              aria-label="清空搜索"
              @click="keyword = ''"
            >
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </section>

    <main class="mx-auto max-w-7xl px-6 py-7">
      <div
        v-if="error"
        class="mb-5 flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]"
      >
        <AlertCircle class="h-4 w-4 shrink-0" />
        <span class="min-w-0 flex-1">加载失败：{{ error }}</span>
        <Button size="sm" variant="outline" @click="load()">
          <RotateCcw class="h-3.5 w-3.5" />
          重试
        </Button>
      </div>

      <div v-if="loading" class="py-20 text-center text-[var(--color-muted)]">
        <Loader2 class="mx-auto h-5 w-5 animate-spin" />
      </div>

      <div v-else-if="!items.length" class="py-20 text-center">
        <Inbox class="mx-auto h-8 w-8 text-[var(--color-faint)]" />
        <p class="mt-3 text-[13px] text-[var(--color-muted)]">
          {{ hasFilter ? '没有找到相关资源' : '资源库还没有内容' }}
        </p>
        <Button v-if="hasFilter" class="mx-auto mt-4" size="sm" variant="outline" @click="resetFilters">
          清空筛选
        </Button>
      </div>

      <template v-else>
        <div class="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
          <article
            v-for="l in items"
            :key="l.id"
            class="group cursor-pointer"
            tabindex="0"
            role="button"
            :aria-label="`查看 ${l.title}`"
            @click="openDetail(l)"
            @keydown.enter="openDetail(l)"
          >
            <div
              class="relative aspect-[2/3] overflow-hidden rounded-lg border border-[var(--color-line)] bg-[var(--color-surface-hover)]"
            >
              <img
                v-if="l.poster_url"
                :src="l.poster_url"
                :alt="l.title"
                loading="lazy"
                class="h-full w-full object-cover transition-transform duration-300 group-hover:scale-[1.03]"
              />
              <div v-else class="flex h-full items-center justify-center text-xs text-[var(--color-faint)]">
                暂无海报
              </div>

              <span
                v-if="l.vote_average > 0"
                class="absolute right-2 top-2 flex items-center gap-0.5 rounded-full bg-black/65 px-1.5 py-0.5 text-[11px] font-medium text-white"
              >
                <Star class="h-3 w-3 fill-current" />
                {{ l.vote_average.toFixed(1) }}
              </span>
              <Badge
                v-if="mediaMeta[l.media_type]"
                class="absolute bottom-2 left-2"
                :class="mediaMeta[l.media_type].cls"
              >
                {{ mediaMeta[l.media_type].label }}
              </Badge>
              <span
                v-if="(l.link_count ?? 1) > 1"
                class="absolute bottom-2 right-2 flex items-center gap-0.5 rounded-full bg-black/65 px-1.5 py-0.5 text-[11px] font-medium text-white"
                :title="`共 ${l.link_count} 条分享链接`"
              >
                <Share2 class="h-3 w-3" />
                {{ l.link_count }}
              </span>
            </div>
            <p class="mt-2 truncate text-[13px] font-medium">{{ l.title || '—' }}</p>
            <p class="truncate text-xs text-[var(--color-muted)]">
              {{ l.year || '—' }}
              <template v-if="genresText(l)"> · {{ genresText(l) }}</template>
            </p>
          </article>
        </div>

        <div class="mt-8 flex flex-col items-center gap-2">
          <p class="text-xs text-[var(--color-faint)]">已显示 {{ items.length }} / {{ total }} 部</p>
          <Button
            v-if="items.length < total"
            variant="outline"
            size="sm"
            :disabled="loadingMore"
            @click="load(false)"
          >
            <Loader2 v-if="loadingMore" class="h-4 w-4 animate-spin" />
            加载更多
          </Button>
        </div>
      </template>
    </main>

    <footer class="border-t border-[var(--color-line)] py-6 text-center text-xs text-[var(--color-faint)]">
      共 {{ stats?.library_total ?? total }} 部资源 · 数据来自光鸭网盘与 TMDB
    </footer>

    <!-- 资源详情 -->
    <div
      v-if="detail"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      @click.self="closeDetail"
    >
      <div
        ref="detailPanel"
        tabindex="-1"
        class="relative max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-[var(--radius-card)] bg-[var(--color-surface)] p-5 shadow-xl focus:outline-none"
        role="dialog"
        aria-modal="true"
        :aria-label="detail.title"
      >
        <button
          class="absolute right-3 top-3 rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]"
          aria-label="关闭"
          @click="closeDetail"
        >
          <X class="h-4 w-4" />
        </button>

        <div class="grid gap-5 sm:grid-cols-[220px_minmax(0,1fr)]">
          <div class="mx-auto w-40 sm:mx-0 sm:w-full">
            <div
              class="aspect-[2/3] overflow-hidden rounded-lg border border-[var(--color-line)] bg-[var(--color-surface-hover)]"
            >
              <img
                v-if="detail.poster_url"
                :src="detail.poster_url"
                :alt="detail.title"
                class="h-full w-full object-cover"
              />
            </div>
          </div>

          <div class="flex min-w-0 flex-col" :class="overviewExpanded ? 'sm:min-h-[330px]' : 'sm:h-[330px]'">
            <div class="flex flex-wrap items-center gap-1.5 pr-8">
              <Badge v-if="mediaMeta[detail.media_type]" :class="mediaMeta[detail.media_type].cls">
                {{ mediaMeta[detail.media_type].label }}
              </Badge>
              <span v-if="detail.year" class="text-[13px] text-[var(--color-muted)]">{{ detail.year }}</span>
              <span
                v-if="detail.runtime"
                class="text-[13px] text-[var(--color-muted)]"
              >· {{ detail.runtime }} 分钟</span>
              <span
                v-if="detail.vote_average > 0"
                class="flex items-center gap-0.5 text-[13px] text-[var(--color-warn)]"
              >
                <Star class="h-3.5 w-3.5 fill-current" />
                {{ detail.vote_average.toFixed(1) }}
              </span>
            </div>

            <h2 class="mt-1.5 text-xl leading-snug">{{ detail.title }}</h2>
            <p v-if="detail.original_title && detail.original_title !== detail.title" class="text-[13px] text-[var(--color-muted)]">
              {{ detail.original_title }}
            </p>
            <p v-if="genresText(detail)" class="mt-2 text-[13px] text-[var(--color-muted)]">
              {{ genresText(detail) }}
            </p>

            <div class="mt-3 flex min-h-0 flex-1 flex-col">
              <p
                id="portal-overview"
                ref="overviewElement"
                class="whitespace-pre-line text-[13px] leading-relaxed text-[var(--color-muted)]"
                :class="overviewExpanded ? '' : 'line-clamp-4 max-h-24 overflow-hidden'"
              >
                {{ detail.overview || '暂无简介' }}
              </p>
              <button
                v-if="overviewOverflow || overviewExpanded"
                class="mt-1 shrink-0 self-start text-xs text-[var(--color-brand)] hover:underline"
                :aria-expanded="overviewExpanded"
                aria-controls="portal-overview"
                @click="overviewExpanded = !overviewExpanded"
              >
                {{ overviewExpanded ? '收起简介' : '展开简介' }}
              </button>
            </div>

            <div
              v-if="detailShares.length"
              class="mt-3 max-h-32 shrink-0 space-y-1.5 overflow-y-auto rounded-lg bg-[var(--color-surface-sunken)] p-3 text-[13px]"
            >
              <div v-for="(s, i) in detailShares" :key="s.url || i" class="flex items-center gap-2">
                <div class="min-w-0 flex-1 truncate">
                  <span class="text-[var(--color-muted)]">分享 {{ i + 1 }}</span>
                  <span v-if="s.code" class="ml-2">提取码 <span class="font-mono font-medium">{{ s.code }}</span></span>
                  <span v-if="s.shared_at" class="ml-2 text-xs text-[var(--color-faint)]">
                    {{ s.shared_at.slice(0, 10) }}
                  </span>
                </div>
                <Button v-if="s.url" size="sm" variant="outline" @click="copyAndReport(s)">
                  <Copy class="h-3.5 w-3.5" />
                  复制
                </Button>
                <a
                  v-if="s.url && safeExternalUrl(s.url)"
                  :href="safeExternalUrl(s.url)"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="flex shrink-0 items-center gap-1 text-xs text-[var(--color-brand)] hover:underline"
                  @click="report('open', s.id)"
                >
                  <ExternalLink class="h-3.5 w-3.5" />
                  打开
                </a>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
