<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Cloud,
  Search,
  X,
  Loader2,
  Inbox,
  Copy,
  ExternalLink,
  RotateCcw,
  Star,
  AlertCircle,
  ChevronDown,
} from 'lucide-vue-next'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import { api, mediaMeta, type PortalLink, type PortalStats } from '../lib/api'
import { toast } from '../lib/toast'

const PAGE = 24
const items = ref<PortalLink[]>([])
const total = ref(0)
const stats = ref<PortalStats | null>(null)
const keyword = ref('')
const media = ref('')
const year = ref(0)
const sort = ref<'new' | 'rating'>('new')
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const detail = ref<PortalLink | null>(null)

let loadSeq = 0
let statsSeq = 0
let timer: number | undefined

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
}

async function copyText(text: string) {
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

async function copyLink(l: PortalLink) {
  const text = l.code ? `${l.url}\n提取码：${l.code}` : l.url
  if (await copyText(text)) toast.success('链接已复制')
  else toast.error('复制失败，请重试或点击打开链接')
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') detail.value = null
}

watch(keyword, () => schedule(300))
watch(media, () => schedule(0))
watch(year, () => schedule(0))
watch(sort, () => schedule(0))

watch(detail, (d) => {
  document.body.style.overflow = d ? 'hidden' : ''
})

onMounted(async () => {
  document.title = '光鸭 GO · 资源库'
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
  window.addEventListener('keydown', onKey)
  load()
})

onUnmounted(() => {
  window.clearTimeout(timer)
  window.removeEventListener('keydown', onKey)
  document.body.style.overflow = ''
})
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-6 py-3.5">
        <div class="flex items-center gap-2.5">
          <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--color-brand)]">
            <Cloud class="h-4 w-4 text-white" />
          </div>
          <div>
            <p class="text-[15px] font-medium leading-tight">光鸭 GO</p>
            <p class="text-xs leading-tight text-[var(--color-muted)]">资源库</p>
          </div>
        </div>
        <p class="text-[13px] text-[var(--color-muted)]">
          已收录 <span class="font-medium text-[var(--color-ink)]">{{ stats?.library_total ?? total }}</span> 部
        </p>
      </div>
    </header>

    <section class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto max-w-2xl px-6 pb-6 pt-9 sm:pb-7">
        <div class="text-center">
          <h1 class="text-3xl tracking-tight sm:text-4xl">光鸭片库</h1>
          <p class="mt-2 text-[13px] text-[var(--color-muted)]">
            已发布到频道的资源，搜索片名就能找到分享链接
          </p>
        </div>

        <div class="relative mt-6">
          <Search class="pointer-events-none absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-faint)]" />
          <input
            v-model="keyword"
            class="h-11 w-full rounded-full border border-[var(--color-line-strong)] bg-[var(--color-surface)] pl-10 pr-10 text-sm text-[var(--color-ink)] placeholder:text-[var(--color-faint)] focus:border-[var(--color-brand)] focus:outline-none focus:ring-2 focus:ring-[var(--color-brand-line)]"
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

        <div class="mt-3.5 flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex flex-wrap items-center justify-center gap-1">
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

          <div class="flex items-center justify-center gap-1.5">
            <div class="flex items-center gap-0.5 rounded-full bg-[var(--color-surface-sunken)] p-0.5">
              <button
                class="rounded-full px-2.5 py-1 text-[13px] transition-colors"
                :class="
                  sort === 'new'
                    ? 'bg-[var(--color-surface)] font-medium text-[var(--color-ink)] shadow-sm'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
                "
                @click="sort = 'new'"
              >
                最新分享
              </button>
              <button
                class="flex items-center gap-1 rounded-full px-2.5 py-1 text-[13px] transition-colors"
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
                class="h-8 cursor-pointer appearance-none rounded-full border pl-3 pr-8 text-[13px] transition-colors focus:border-[var(--color-brand)] focus:outline-none"
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
            @click="detail = l"
            @keydown.enter="detail = l"
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
      @click.self="detail = null"
    >
      <div
        class="relative max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-[var(--radius-card)] bg-[var(--color-surface)] p-5 shadow-xl"
        role="dialog"
        aria-modal="true"
        :aria-label="detail.title"
      >
        <button
          class="absolute right-3 top-3 rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]"
          aria-label="关闭"
          @click="detail = null"
        >
          <X class="h-4 w-4" />
        </button>

        <div class="grid gap-5 sm:grid-cols-[190px_1fr]">
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

          <div class="min-w-0">
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

            <p class="mt-3 whitespace-pre-line text-[13px] leading-relaxed text-[var(--color-muted)]">
              {{ detail.overview || '暂无简介' }}
            </p>

            <div v-if="detail.code" class="mt-4 rounded-lg bg-[var(--color-surface-sunken)] p-3 text-[13px]">
              提取码 <span class="font-mono font-medium">{{ detail.code }}</span>
            </div>

            <div class="mt-4 flex flex-wrap items-center gap-2">
              <Button v-if="detail.url" size="sm" @click="copyLink(detail)">
                <Copy class="h-4 w-4" />
                复制链接{{ detail.code ? '与提取码' : '' }}
              </Button>
              <a v-if="detail.url" :href="detail.url" target="_blank" rel="noreferrer">
                <Button size="sm" variant="outline">
                  <ExternalLink class="h-4 w-4" />
                  打开光鸭
                </Button>
              </a>
              <span v-if="detail.shared_at" class="text-xs text-[var(--color-faint)]">
                分享于 {{ detail.shared_at.slice(0, 10) }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
