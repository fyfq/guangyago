<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Sparkles,
  Loader2,
  CheckCircle2,
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
import TaskProgressBar from '../components/TaskProgress.vue'
import {
  api,
  statusMeta,
  mediaMeta,
  type TaskProgress,
  type Settings,
  type Link,
  type TmdbCandidate,
} from '../lib/api'
import { toast } from '../lib/toast'
import { confirmAction } from '../lib/confirm'

// 尚未刮削成功的全部状态
const PENDING_SET = ['pending', 'review', 'scraping', 'unmatched', 'failed']

const progress = ref<TaskProgress | null>(null)
const settings = ref<Settings | null>(null)
const starting = ref(false)

const items = ref<Link[]>([])
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
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
const showIdForm = ref(false)

// 补 ID 的「先查询、确认后再刮削」状态
const looking = ref(false)
const lookError = ref('')
const candidates = ref<TmdbCandidate[]>([])
const picked = ref<string | null>(null)

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

const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const allChecked = computed(() => items.value.length > 0 && selected.value.length === items.value.length)
const running = computed(() => !!progress.value?.running)

const emptyText = computed(() => {
  if (keyword.value.trim()) return '没有匹配的记录'
  if (status.value === 'pending_set') return '没有待刮削的记录'
  return `没有「${filters.find((f) => f.value === status.value)?.label ?? ''}」的记录`
})

/** 当前筛选对应的后端状态列表 */
function currentStatuses(): string[] {
  return status.value === 'pending_set' ? PENDING_SET : [status.value]
}

// 输入、切页、切筛选可能同时触发请求，只让最后一次结果落地
let loadSeq = 0

async function loadList() {
  const seq = ++loadSeq
  loading.value = true
  loadError.value = ''
  try {
    const res = await api.list({
      status: status.value,
      q: keyword.value,
      limit: pageSize,
      offset: page.value * pageSize,
    })
    if (seq !== loadSeq) return
    items.value = res.items
    total.value = res.total
    selected.value = []
  } catch (e) {
    if (seq !== loadSeq) return
    loadError.value = e instanceof Error ? e.message : '加载失败'
    items.value = []
    total.value = 0
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

let loadTimer: number | undefined
function scheduleLoad(delay = 150) {
  window.clearTimeout(loadTimer)
  loadTimer = window.setTimeout(loadList, delay)
}

async function loadProgress() {
  try {
    progress.value = await api.tasks('scrape')
  } catch {
    progress.value = null
  }
}

function poll() {
  window.clearInterval(timer)
  timer = window.setInterval(async () => {
    await loadProgress()
    if (!progress.value?.running) {
      window.clearInterval(timer)
      starting.value = false
      await loadList()
      emit('changed')
    }
  }, 800)
}

async function run(action: () => Promise<{ queued: number }>) {
  starting.value = true
  try {
    const res = await action()
    toast.success(`已加入刮削队列 ${res.queued} 条`)
    await loadProgress()
    poll()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '启动失败')
    starting.value = false
  }
}

const retryFailed = () => run(() => api.scrape([], false, ['failed', 'unmatched']))
const scrapeFiltered = () => run(() => api.scrape([], false, currentStatuses()))
const scrapeSelected = () => run(() => api.scrape(selected.value.slice()))

async function stopAll() {
  try {
    await api.cancelTasks('scrape')
    toast.info('已停止排队中的刮削任务')
    await loadProgress()
    await loadList()
    emit('changed')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '停止失败')
  }
}

async function scrapeOne(id: number) {
  acting.value = id
  try {
    await api.scrapeOne(id)
    toast.info('已加入刮削队列')
    await loadProgress()
    poll()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '刮削启动失败')
  } finally {
    acting.value = null
  }
}

async function removeOne(l: Link) {
  const ok = await confirmAction({
    title: '删除这条记录？',
    message: `「${l.title || l.raw_title || l.url}」会一并删除，之后需要重新录入。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await api.remove(l.id)
    toast.success('已删除')
    await loadList()
    emit('changed')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '删除失败')
  }
}

/** 候选条目的唯一键：电影和剧集的 TMDB ID 各自独立，不能只用数字 */
function candidateKey(c: TmdbCandidate) {
  return `${c.media_type}-${c.tmdb_id}`
}

/** 第一步：只查询 TMDB，拿到候选条目，不写库 */
async function lookup(l: Link, mode: 'query' | 'id') {
  const id = mode === 'id' ? Number(tmdbInput.value) : 0
  const q = mode === 'query' ? queryInput.value.trim() : ''
  if (!id && !q) {
    lookError.value = mode === 'id' ? '填 TMDB 数字 ID 再查询' : '填片名后再查询'
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
      picked.value = candidateKey(candidates.value[0])
    }
  } catch (e) {
    lookError.value = e instanceof Error ? e.message : '查询失败'
  } finally {
    looking.value = false
  }
}

/** 第二步：确认无误后才写入 TMDB ID 并刮削 */
async function confirmTmdb(l: Link) {
  const hit = candidates.value.find((c) => candidateKey(c) === picked.value)
  if (!hit) return
  acting.value = l.id
  try {
    await api.setTmdb(l.id, hit.tmdb_id, hit.media_type, true)
    toast.success(`已指定 TMDB ${hit.tmdb_id}，开始刮削`)
    closeEdit()
    await loadProgress()
    poll()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '写入 TMDB ID 失败')
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
  showIdForm.value = false
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
  showIdForm.value = false
}

function toggleAll() {
  selected.value = allChecked.value ? [] : items.value.map((i) => i.id)
}

function toggleSelect(id: number) {
  selected.value = selected.value.includes(id)
    ? selected.value.filter((x) => x !== id)
    : [...selected.value, id]
}

function go(delta: number) {
  const next = page.value + delta
  if (next < 0 || next >= pageCount.value) return
  page.value = next
  loadList()
}

watch(keyword, () => {
  page.value = 0
  scheduleLoad(300)
})
watch(status, () => {
  page.value = 0
  scheduleLoad()
})

watch(
  () => props.initialStatus,
  (v) => {
    if (!v) return
    status.value = v
    page.value = 0
    scheduleLoad()
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
  window.clearTimeout(loadTimer)
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
        <Button :disabled="starting || running || !total" @click="scrapeFiltered">
          <Loader2 v-if="starting || running" class="h-4 w-4 animate-spin" />
          <Sparkles v-else class="h-4 w-4" />
          刮削当前筛选（{{ total }}）
        </Button>
        <Button variant="outline" :disabled="starting || running || !selected.length" @click="scrapeSelected">
          <Sparkles class="h-4 w-4" />
          刮削所选 {{ selected.length || '' }}
        </Button>
        <Button variant="outline" :disabled="starting || running" @click="retryFailed">
          <RotateCcw class="h-4 w-4" />
          重试失败与未匹配
        </Button>
      </div>

      <div v-if="progress && progress.total" class="mt-5">
        <TaskProgressBar :progress="progress" cancelable @cancel="stopAll" />
      </div>
    </Card>

    <Card>
      <template #title>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="text-[15px] font-medium">待刮削列表</h2>
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
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-faint)]" />
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
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]'
            "
            @click="status = f.value"
          >
            {{ f.label }}
          </button>
        </div>
      </div>

      <div
        v-if="loadError"
        class="mt-4 flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]"
      >
        <span class="min-w-0 flex-1">列表加载失败：{{ loadError }}</span>
        <Button size="sm" variant="outline" @click="loadList">
          <RotateCcw class="h-3.5 w-3.5" />
          重试
        </Button>
      </div>

      <div v-if="loading" class="py-10 text-center text-[var(--color-muted)]">
        <Loader2 class="mx-auto h-5 w-5 animate-spin" />
      </div>

      <div v-else-if="!items.length" class="py-12 text-center">
        <Inbox class="mx-auto h-8 w-8 text-[var(--color-faint)]" />
        <p class="mt-3 text-[13px] text-[var(--color-muted)]">{{ emptyText }}</p>
        <Button class="mx-auto mt-4" size="sm" @click="emit('goto', 'import')">
          <Plus class="h-4 w-4" />
          去录入
        </Button>
      </div>

      <template v-else>
        <div
          class="mt-4 flex items-center gap-2 border-b border-[var(--color-line)] pb-2 text-[13px] text-[var(--color-muted)]"
        >
          <input
            type="checkbox"
            :checked="allChecked"
            :indeterminate.prop="selected.length > 0 && !allChecked"
            @change="toggleAll"
          />
          <label class="cursor-pointer select-none" @click="toggleAll">全选本页</label>
          <span v-if="selected.length" class="text-[var(--color-brand)]">已选 {{ selected.length }} 条</span>
        </div>

        <div class="space-y-2 pt-2">
          <div
            v-for="l in items"
            :key="l.id"
            class="rounded-lg border border-[var(--color-line)] p-3"
          >
            <div class="flex items-start gap-3">
              <input
                type="checkbox"
                class="mt-1"
                :checked="selected.includes(l.id)"
                @change="toggleSelect(l.id)"
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
                <p v-if="l.raw_title && l.raw_title !== l.title" class="mt-0.5 truncate text-xs text-[var(--color-faint)]">
                  {{ l.raw_title }}
                </p>
                <p v-if="l.error" class="mt-1 text-xs text-[var(--color-bad)]">{{ l.error }}</p>
              </div>

              <div class="flex shrink-0 items-center gap-1">
                <button
                  class="flex items-center gap-1 rounded-md px-2 py-1 text-[13px] text-[var(--color-brand)] hover:bg-[var(--color-surface-hover)] disabled:opacity-40"
                  :disabled="acting === l.id"
                  @click="scrapeOne(l.id)"
                >
                  <Loader2 v-if="acting === l.id" class="h-3.5 w-3.5 animate-spin" />
                  <Sparkles v-else class="h-3.5 w-3.5" />
                  刮削
                </button>
                <button
                  class="rounded-md px-2 py-1 text-[13px] text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]"
                  title="手动指定 TMDB ID"
                  @click="openEdit(l)"
                >
                  补 ID
                </button>
                <button
                  class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-bad)]"
                  title="删除"
                  @click="removeOne(l)"
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
                <Input
                  v-model="queryInput"
                  class="w-56"
                  placeholder="按片名搜索"
                  @keyup.enter="lookup(l, 'query')"
                />
                <Button size="sm" variant="outline" :disabled="looking" @click="lookup(l, 'query')">
                  <Loader2 v-if="looking" class="h-3.5 w-3.5 animate-spin" />
                  <Search v-else class="h-3.5 w-3.5" />
                  查询
                </Button>
                <button
                  class="text-[13px] text-[var(--color-brand)] hover:underline"
                  @click="showIdForm = !showIdForm"
                >
                  {{ showIdForm ? '收起 ID 输入' : '已知 TMDB ID？' }}
                </button>
              </div>

              <div v-if="showIdForm" class="flex flex-wrap items-center gap-2">
                <span class="text-[13px] text-[var(--color-muted)]">ID</span>
                <Input
                  v-model="tmdbInput"
                  class="w-28"
                  placeholder="20851"
                  @keyup.enter="lookup(l, 'id')"
                />
                <select
                  v-model="mediaInput"
                  class="h-9 rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-transparent px-2 text-[13px]"
                >
                  <option value="movie">电影</option>
                  <option value="tv">剧集</option>
                </select>
                <Button size="sm" variant="outline" :disabled="looking" @click="lookup(l, 'id')">
                  精确查询
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
                  :key="candidateKey(c)"
                  class="flex cursor-pointer gap-3 rounded-lg border p-2 transition-colors"
                  :class="
                    picked === candidateKey(c)
                      ? 'border-[var(--color-brand)] bg-[var(--color-brand-soft)]'
                      : 'border-[var(--color-line)] hover:bg-[var(--color-surface-sunken)]'
                  "
                >
                  <input
                    v-model="picked"
                    type="radio"
                    class="mt-2"
                    :value="candidateKey(c)"
                  />
                  <img
                    v-if="c.poster_url"
                    :src="c.poster_url"
                    class="h-20 w-14 shrink-0 rounded object-cover"
                    alt=""
                  />
                  <div class="h-20 w-14 shrink-0 rounded bg-[var(--color-surface-hover)]" v-else />
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="truncate text-[13px] font-medium">{{ c.title }}</span>
                      <Badge :class="mediaMeta[c.media_type]?.cls">{{ c.media_label }}</Badge>
                      <span class="text-xs text-[var(--color-muted)]">{{ c.year || '—' }}</span>
                    </div>
                    <p v-if="c.original_title && c.original_title !== c.title" class="truncate text-xs text-[var(--color-faint)]">
                      {{ c.original_title }}
                    </p>
                    <p class="mt-1 line-clamp-2 text-xs text-[var(--color-muted)]">{{ c.overview }}</p>
                    <a
                      :href="c.site_url"
                      target="_blank"
                      rel="noopener noreferrer"
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

              <p v-else class="text-xs text-[var(--color-faint)]">
                先按片名搜出候选，或展开填入 themoviedb.org 网址里的数字 ID；确认无误后才会写入并刮削
              </p>
            </div>
          </div>
        </div>
      </template>

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
