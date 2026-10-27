<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Sparkles,
  Loader2,
  CheckCircle2,
  AlertCircle,
  KeyRound,
  Search,
  RotateCcw,
  Trash2,
  Inbox,
  Plus,
  ExternalLink,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import Input from '../components/ui/Input.vue'
import {
  api,
  statusMeta,
  mediaMeta,
  type TaskProgress,
  type Settings,
  type Link,
  type TmdbCandidate,
} from '../lib/api'

// 尚未刮削成功的全部状态
const PENDING_SET = ['pending', 'review', 'scraping', 'unmatched', 'failed']

const progress = ref<TaskProgress | null>(null)
const settings = ref<Settings | null>(null)
const starting = ref(false)
const error = ref('')

const items = ref<Link[]>([])
const total = ref(0)
const loading = ref(false)
const keyword = ref('')
const status = ref('pending_set')
const page = ref(0)
const pageSize = 20
const selected = ref<number[]>([])
const acting = ref<number | null>(null)
const editingId = ref<number | null>(null)
const tmdbInput = ref('')
const mediaInput = ref<'movie' | 'tv'>('movie')
const queryInput = ref('')

// 补 ID 的「先查询、确认后再刮削」状态
const looking = ref(false)
const lookError = ref('')
const candidates = ref<TmdbCandidate[]>([])
const picked = ref<number | null>(null)

let timer: number | undefined

const emit = defineEmits<{ changed: []; goto: [tab: string] }>()

/** 从概览卡片跳进来时带上的状态筛选 */
const props = defineProps<{ initialStatus?: string }>()

const filters = [
  { value: 'pending_set', label: '全部未刮削' },
  { value: 'pending', label: '待处理' },
  { value: 'unmatched', label: '未匹配' },
  { value: 'failed', label: '失败' },
  { value: 'review', label: '待核查' },
]

const percent = computed(() => {
  const p = progress.value
  if (!p || !p.total) return 0
  return Math.round(((p.done + p.failed) / p.total) * 100)
})
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const allChecked = computed(() => items.value.length > 0 && selected.value.length === items.value.length)

/** 当前筛选对应的后端状态列表 */
function currentStatuses(): string[] {
  return status.value === 'pending_set' ? PENDING_SET : [status.value]
}

async function loadList() {
  loading.value = true
  try {
    const res = await api.list({
      status: status.value,
      q: keyword.value,
      limit: pageSize,
      offset: page.value * pageSize,
    })
    items.value = res.items
    total.value = res.total
    selected.value = []
  } finally {
    loading.value = false
  }
}

async function loadProgress() {
  try {
    progress.value = await api.tasks()
  } catch {
    progress.value = null
  }
}

function poll() {
  window.clearInterval(timer)
  timer = window.setInterval(async () => {
    await loadProgress()
    if (progress.value && !progress.value.running) {
      window.clearInterval(timer)
      starting.value = false
      await loadList()
      emit('changed')
    }
  }, 800)
}

async function run(fn: () => Promise<unknown>) {
  starting.value = true
  error.value = ''
  try {
    await fn()
    await loadProgress()
    poll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '启动失败'
    starting.value = false
  }
}

const scrapeAll = () => run(() => api.scrape([], true))
const retryFailed = () => run(() => api.scrape([], false, ['failed', 'unmatched']))
const scrapeFiltered = () => run(() => api.scrape([], false, currentStatuses()))
const scrapeSelected = () => run(() => api.scrape(selected.value.slice()))

async function scrapeOne(id: number) {
  acting.value = id
  try {
    await api.scrapeOne(id)
    await loadProgress()
    poll()
  } finally {
    acting.value = null
  }
}

async function removeOne(id: number) {
  await api.remove(id)
  await loadList()
  emit('changed')
}

/** 第一步：只查询 TMDB，拿到候选条目，不写库 */
async function lookup(l: Link) {
  const id = Number(tmdbInput.value)
  const q = queryInput.value.trim()
  if (!id && !q) {
    lookError.value = '填 TMDB ID，或填片名搜索'
    return
  }
  looking.value = true
  lookError.value = ''
  candidates.value = []
  picked.value = null
  try {
    const res = await api.lookup({
      tmdb_id: id || undefined,
      media_type: mediaInput.value,
      query: q || undefined,
      year: l.year || undefined,
    })
    candidates.value = res.items ?? []
    if (!candidates.value.length) {
      lookError.value = '没有查到对应条目'
    } else if (candidates.value.length === 1) {
      picked.value = candidates.value[0].tmdb_id
    }
  } catch (e) {
    lookError.value = e instanceof Error ? e.message : '查询失败'
  } finally {
    looking.value = false
  }
}

/** 第二步：确认无误后才写入 TMDB ID 并刮削 */
async function confirmTmdb(l: Link) {
  const hit = candidates.value.find((c) => c.tmdb_id === picked.value)
  if (!hit) return
  acting.value = l.id
  try {
    await api.setTmdb(l.id, hit.tmdb_id, hit.media_type, true)
    closeEdit()
    await loadProgress()
    poll()
  } finally {
    acting.value = null
  }
}

function closeEdit() {
  editingId.value = null
  tmdbInput.value = ''
  queryInput.value = ''
  candidates.value = []
  picked.value = null
  lookError.value = ''
}

function openEdit(l: Link) {
  if (editingId.value === l.id) {
    closeEdit()
    return
  }
  editingId.value = l.id
  tmdbInput.value = l.tmdb_id ? String(l.tmdb_id) : ''
  mediaInput.value = l.media_type === 'tv' ? 'tv' : 'movie'
  queryInput.value = l.title || l.raw_title || ''
  candidates.value = []
  picked.value = null
  lookError.value = ''
}

function toggleAll() {
  selected.value = allChecked.value ? [] : items.value.map((i) => i.id)
}

function go(delta: number) {
  const next = page.value + delta
  if (next < 0 || next >= pageCount.value) return
  page.value = next
  loadList()
}

let kwTimer: number | undefined
watch(keyword, () => {
  window.clearTimeout(kwTimer)
  kwTimer = window.setTimeout(() => {
    page.value = 0
    loadList()
  }, 300)
})
watch(status, () => {
  page.value = 0
  loadList()
})

watch(
  () => props.initialStatus,
  (v) => {
    if (!v) return
    status.value = v
    page.value = 0
    loadList()
  },
)

onMounted(async () => {
  if (props.initialStatus) status.value = props.initialStatus
  await loadProgress()
  await loadList()
  if (progress.value?.running) {
    starting.value = true
    poll()
  }
  try {
    settings.value = await api.settings()
  } catch {
    settings.value = null
  }
})

onUnmounted(() => {
  window.clearInterval(timer)
  window.clearTimeout(kwTimer)
})
</script>

<template>
  <div class="space-y-5">
    <Card title="刮削队列" desc="未刮削的记录都在这里，刮削成功后自动进入资源库">
      <div
        v-if="settings && !settings.tmdb_configured"
        class="mb-4 flex items-start gap-2 rounded-lg bg-[var(--color-warn-soft)] px-3 py-2.5 text-[13px] text-[var(--color-warn)]"
      >
        <KeyRound class="mt-0.5 h-4 w-4 shrink-0" />
        <span>还没有配置 TMDB API Key，先去「设置」页填入，否则记录会被标记为未匹配。</span>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <Button :disabled="starting || !total" @click="scrapeFiltered">
          <Loader2 v-if="starting" class="h-4 w-4 animate-spin" />
          <Sparkles v-else class="h-4 w-4" />
          刮削当前筛选（{{ total }}）
        </Button>
        <Button variant="outline" :disabled="starting || !selected.length" @click="scrapeSelected">
          <Sparkles class="h-4 w-4" />
          刮削所选 {{ selected.length || '' }}
        </Button>
        <Button variant="outline" :disabled="starting" @click="retryFailed">
          <RotateCcw class="h-4 w-4" />
          重试失败与未匹配
        </Button>
        <Button variant="ghost" :disabled="starting" @click="scrapeAll">
          刮削全部待处理
        </Button>
      </div>

      <p v-if="error" class="mt-3 text-[13px] text-[var(--color-bad)]">{{ error }}</p>

      <div v-if="progress && progress.total" class="mt-5">
        <div class="flex items-center justify-between text-[13px]">
          <span class="truncate text-[var(--color-muted)]">{{ progress.current || '准备中' }}</span>
          <span class="font-medium">{{ progress.done + progress.failed }} / {{ progress.total }}</span>
        </div>
        <div class="mt-2 h-2 overflow-hidden rounded-full bg-stone-100">
          <div
            class="h-full rounded-full bg-[var(--color-brand)] transition-all duration-300"
            :style="{ width: percent + '%' }"
          />
        </div>
        <div class="mt-3 flex items-center gap-4 text-[13px]">
          <span class="flex items-center gap-1.5 text-[var(--color-ok)]">
            <CheckCircle2 class="h-4 w-4" />
            成功 {{ progress.done }}
          </span>
          <span class="flex items-center gap-1.5 text-[var(--color-bad)]">
            <AlertCircle class="h-4 w-4" />
            失败 {{ progress.failed }}
          </span>
          <Badge v-if="progress.running" class="bg-[var(--color-brand-soft)] text-[var(--color-brand)]">
            进行中
          </Badge>
        </div>
        <p v-if="progress.error" class="mt-2 text-[13px] text-[var(--color-bad)]">
          最近错误：{{ progress.error }}
        </p>
      </div>
    </Card>

    <Card>
      <template #title>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <span>待刮削列表</span>
            <span class="ml-2 text-[13px] font-normal text-[var(--color-muted)]">共 {{ total }} 条</span>
          </div>
          <button
            class="flex items-center gap-1 text-[13px] text-[var(--color-brand)] hover:underline"
            @click="emit('goto', 'library')"
          >
            已刮削的去资源库 →
          </button>
        </div>
      </template>

      <div class="flex flex-wrap items-center gap-2">
        <div class="relative w-full max-w-xs">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-stone-400" />
          <Input v-model="keyword" class="pl-9" placeholder="搜索片名 / 链接" />
        </div>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="f in filters"
            :key="f.value"
            class="rounded-full px-3 py-1 text-[13px] transition-colors"
            :class="
              status === f.value
                ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)] font-medium'
                : 'text-[var(--color-muted)] hover:bg-stone-100'
            "
            @click="status = f.value"
          >
            {{ f.label }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="py-10 text-center text-[var(--color-muted)]">
        <Loader2 class="mx-auto h-5 w-5 animate-spin" />
      </div>

      <div v-else-if="!items.length" class="py-12 text-center">
        <Inbox class="mx-auto h-8 w-8 text-stone-300" />
        <p class="mt-3 text-[13px] text-[var(--color-muted)]">没有待刮削的记录</p>
        <Button class="mx-auto mt-4" size="sm" @click="emit('goto', 'import')">
          <Plus class="h-4 w-4" />
          去录入
        </Button>
      </div>

      <div v-else class="mt-4 space-y-2">
        <div
          v-for="l in items"
          :key="l.id"
          class="rounded-lg border border-[var(--color-line)] p-3"
        >
          <div class="flex items-start gap-3">
            <input
              type="checkbox"
              class="mt-1"
              :value="l.id"
              :checked="selected.includes(l.id)"
              @change="
                selected.includes(l.id)
                  ? (selected = selected.filter((x) => x !== l.id))
                  : selected.push(l.id)
              "
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate text-[13px] font-medium">{{ l.title || l.raw_title || '—' }}</span>
                <Badge :class="statusMeta[l.status]?.cls">{{ statusMeta[l.status]?.label ?? l.status }}</Badge>
                <Badge v-if="mediaMeta[l.media_type]" :class="mediaMeta[l.media_type].cls">
                  {{ mediaMeta[l.media_type].label }}
                </Badge>
              </div>
              <p class="mt-0.5 text-xs text-[var(--color-muted)]">
                {{ [l.year || '', l.tmdb_id ? `TMDB ${l.tmdb_id}` : '', l.code ? `提取码 ${l.code}` : ''].filter(Boolean).join(' · ') }}
              </p>
              <p v-if="l.raw_title && l.raw_title !== l.title" class="mt-0.5 truncate text-xs text-stone-400">
                {{ l.raw_title }}
              </p>
              <p v-if="l.error" class="mt-1 text-xs text-[var(--color-bad)]">{{ l.error }}</p>
            </div>

            <div class="flex shrink-0 items-center gap-1">
              <button
                class="flex items-center gap-1 rounded-md px-2 py-1 text-[13px] text-[var(--color-brand)] hover:bg-stone-100 disabled:opacity-40"
                :disabled="acting === l.id"
                @click="scrapeOne(l.id)"
              >
                <Loader2 v-if="acting === l.id" class="h-3.5 w-3.5 animate-spin" />
                <Sparkles v-else class="h-3.5 w-3.5" />
                刮削
              </button>
              <button
                class="rounded-md px-2 py-1 text-[13px] text-[var(--color-muted)] hover:bg-stone-100"
                title="手动指定 TMDB ID"
                @click="openEdit(l)"
              >
                补 ID
              </button>
              <button
                class="rounded p-1 text-[var(--color-muted)] hover:bg-stone-100 hover:text-[var(--color-bad)]"
                title="删除"
                @click="removeOne(l.id)"
              >
                <Trash2 class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <!-- 手动补 TMDB ID：先查询确认，再落库刮削 -->
          <div
            v-if="editingId === l.id"
            class="mt-3 space-y-3 border-t border-[var(--color-line)] pt-3"
          >
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-[13px] text-[var(--color-muted)]">TMDB ID</span>
              <Input v-model="tmdbInput" class="w-28" placeholder="20851" />
              <select
                v-model="mediaInput"
                class="h-9 rounded-md border border-[var(--color-line)] bg-transparent px-2 text-[13px]"
              >
                <option value="movie">电影</option>
                <option value="tv">剧集</option>
              </select>
              <span class="text-xs text-stone-400">或</span>
              <Input v-model="queryInput" class="w-44" placeholder="按片名搜索" />
              <Button size="sm" variant="outline" :disabled="looking" @click="lookup(l)">
                <Loader2 v-if="looking" class="h-3.5 w-3.5 animate-spin" />
                <Search v-else class="h-3.5 w-3.5" />
                查询
              </Button>
            </div>

            <p v-if="lookError" class="text-xs text-[var(--color-bad)]">{{ lookError }}</p>

            <!-- 候选列表：确认无误后才允许刮削 -->
            <div v-if="candidates.length" class="space-y-2">
              <p class="text-xs text-[var(--color-muted)]">
                共 {{ candidates.length }} 个候选，选中后再确认刮削
              </p>
              <label
                v-for="c in candidates"
                :key="c.tmdb_id + c.media_type"
                class="flex cursor-pointer gap-3 rounded-lg border p-2 transition-colors"
                :class="
                  picked === c.tmdb_id
                    ? 'border-[var(--color-brand)] bg-[var(--color-brand-soft)]'
                    : 'border-[var(--color-line)] hover:bg-stone-50'
                "
              >
                <input
                  v-model="picked"
                  type="radio"
                  class="mt-2"
                  :value="c.tmdb_id"
                />
                <img
                  v-if="c.poster_url"
                  :src="c.poster_url"
                  class="h-20 w-14 shrink-0 rounded object-cover"
                  alt=""
                />
                <div class="h-20 w-14 shrink-0 rounded bg-stone-100" v-else />
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="truncate text-[13px] font-medium">{{ c.title }}</span>
                    <Badge :class="mediaMeta[c.media_type]?.cls">{{ c.media_label }}</Badge>
                    <span class="text-xs text-[var(--color-muted)]">{{ c.year || '—' }}</span>
                  </div>
                  <p v-if="c.original_title && c.original_title !== c.title" class="truncate text-xs text-stone-400">
                    {{ c.original_title }}
                  </p>
                  <p class="mt-1 line-clamp-2 text-xs text-[var(--color-muted)]">{{ c.overview }}</p>
                  <a
                    :href="c.site_url"
                    target="_blank"
                    class="mt-1 inline-flex items-center gap-1 text-xs text-[var(--color-brand)] hover:underline"
                  >
                    TMDB {{ c.tmdb_id }}
                    <ExternalLink class="h-3 w-3" />
                  </a>
                </div>
              </label>

              <div class="flex items-center gap-2">
                <Button size="sm" :disabled="picked === null || acting === l.id" @click="confirmTmdb(l)">
                  <Loader2 v-if="acting === l.id" class="h-3.5 w-3.5 animate-spin" />
                  <CheckCircle2 v-else class="h-3.5 w-3.5" />
                  确认并刮削
                </Button>
                <Button size="sm" variant="ghost" @click="closeEdit">取消</Button>
              </div>
            </div>

            <p v-else class="text-xs text-stone-400">
              在 themoviedb.org 搜到条目后，从网址里取数字 ID；查询确认无误后才会写入并刮削
            </p>
          </div>
        </div>
      </div>

      <div v-if="items.length" class="mt-4 flex items-center justify-between text-[13px] text-[var(--color-muted)]">
        <span>第 {{ page + 1 }} / {{ pageCount }} 页</span>
        <div class="flex gap-2">
          <Button variant="outline" size="sm" :disabled="page === 0" @click="go(-1)">上一页</Button>
          <Button variant="outline" size="sm" :disabled="page + 1 >= pageCount" @click="go(1)">下一页</Button>
        </div>
      </div>
    </Card>
  </div>
</template>
