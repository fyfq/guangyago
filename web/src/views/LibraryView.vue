<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Search,
  Download,
  Trash2,
  Loader2,
  ExternalLink,
  Sparkles,
  Send,
  LayoutGrid,
  List,
  X,
  Inbox,
  Plus,
  AlertCircle,
  RotateCcw,
  Check,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import Badge from '../components/ui/Badge.vue'
import { api, statusMeta, mediaMeta, mediaLabel, type Link } from '../lib/api'
import { toast } from '../lib/toast'
import { confirmAction } from '../lib/confirm'

const items = ref<Link[]>([])
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
const keyword = ref('')
const status = ref('scraped_set')
const page = ref(0)
const pageSize = 20
const selected = ref<number[]>([])
const exporting = ref(false)
const mode = ref<'table' | 'grid'>('table')
const detail = ref<Link | null>(null)
const acting = ref<number | null>(null)

const drawer = ref<HTMLElement | null>(null)

const props = defineProps<{ initialStatus?: string }>()

const emit = defineEmits<{ changed: []; goto: [tab: string] }>()

// 资源库只承载刮削成功的记录；未刮削的在「刮削队列」页处理
const filters = [
  { value: 'scraped_set', label: '全部状态' },
  { value: 'scraped', label: '已刮削' },
  { value: 'pushed', label: '已推送' },
  { value: 'push_failed', label: '推送失败' },
]

const allChecked = computed(() => items.value.length > 0 && selected.value.length === items.value.length)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

// 请求可能因输入、切页、切筛选而重叠，只让最后一次结果落地
let loadSeq = 0

async function load() {
  const seq = ++loadSeq
  loading.value = true
  loadError.value = ''
  try {
    const res = await api.list({
      q: keyword.value,
      status: status.value,
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
/** 筛选条件变化后合并成一次请求，避免关键词与状态各触发一遍 */
function scheduleLoad(delay = 150) {
  window.clearTimeout(loadTimer)
  loadTimer = window.setTimeout(load, delay)
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

watch(detail, async (d) => {
  document.body.style.overflow = d ? 'hidden' : ''
  if (!d) return
  await nextTick()
  drawer.value?.focus()
})

onMounted(() => {
  if (props.initialStatus) status.value = props.initialStatus
  load()
})

onUnmounted(() => {
  window.clearTimeout(loadTimer)
  document.body.style.overflow = ''
})

function toggleAll() {
  selected.value = allChecked.value ? [] : items.value.map((i) => i.id)
}

function toggleSelect(id: number) {
  selected.value = selected.value.includes(id)
    ? selected.value.filter((x) => x !== id)
    : [...selected.value, id]
}

async function removeOne(l: Link) {
  const ok = await confirmAction({
    title: '删除这条记录？',
    message: `「${l.title || l.raw_title || l.url}」会从资源库移除，已推送到频道的内容不受影响。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await api.remove(l.id)
    if (detail.value?.id === l.id) detail.value = null
    toast.success('已删除')
    emit('changed')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '删除失败')
  }
}

async function removeSelected() {
  if (!selected.value.length) return
  const ok = await confirmAction({
    title: `删除所选 ${selected.value.length} 条记录？`,
    message: '删除后无法恢复，已推送到频道的内容不受影响。',
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    const ids = selected.value.slice()
    await api.batchRemove(ids)
    toast.success(`已删除 ${ids.length} 条`)
    emit('changed')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '批量删除失败')
  }
}

async function scrapeOne(id: number) {
  acting.value = id
  try {
    await api.scrapeOne(id)
    toast.info('已加入刮削队列')
    emit('changed')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '刮削启动失败')
  } finally {
    acting.value = null
  }
}

async function pushOne(id: number) {
  acting.value = id
  try {
    await api.pushOne(id)
    toast.info('已加入推送队列')
    emit('changed')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '推送启动失败')
  } finally {
    acting.value = null
  }
}

async function exportCsv() {
  exporting.value = true
  try {
    const res = await fetch(api.exportUrl({ status: status.value, q: keyword.value }))
    if (!res.ok) throw new Error(`导出失败（${res.status}）`)
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = `光鸭资源_${new Date().toISOString().slice(0, 10)}.csv`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    toast.success(`已导出 ${total.value} 条`)
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '导出失败')
  } finally {
    exporting.value = false
  }
}

function go(delta: number) {
  const next = page.value + delta
  if (next < 0 || next >= pageCount.value) return
  page.value = next
  load()
}

function reset() {
  keyword.value = ''
  status.value = 'scraped_set'
  page.value = 0
  scheduleLoad()
}

function onDrawerKey(e: KeyboardEvent) {
  if (e.key === 'Escape') detail.value = null
}

// 去刮削队列处理未刮削的记录
function gotoQueue() {
  emit('goto', 'scrape')
}
</script>

<template>
  <Card>
    <template #title>
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 class="text-[15px] font-medium">资源库</h2>
          <span class="ml-2 text-[13px] font-normal text-[var(--color-muted)]">
            共 {{ total }} 条 · 刮削完成的资源都在这里，可按推送状态回看
          </span>
        </div>
        <div class="flex gap-1 rounded-[var(--radius-control)] bg-[var(--color-surface-sunken)] p-0.5">
          <button
            class="flex items-center gap-1 rounded-md px-2 py-1 text-[13px] transition-colors"
            :class="mode === 'table' ? 'bg-[var(--color-surface)] shadow-sm' : 'text-[var(--color-muted)]'"
            @click="mode = 'table'"
          >
            <List class="h-3.5 w-3.5" />
            列表
          </button>
          <button
            class="flex items-center gap-1 rounded-md px-2 py-1 text-[13px] transition-colors"
            :class="mode === 'grid' ? 'bg-[var(--color-surface)] shadow-sm' : 'text-[var(--color-muted)]'"
            @click="mode = 'grid'"
          >
            <LayoutGrid class="h-3.5 w-3.5" />
            海报墙
          </button>
        </div>
      </div>
    </template>

    <!-- 空库引导 -->
    <div v-if="!loading && !total && !keyword && status === 'scraped_set'" class="py-14 text-center">
      <Inbox class="mx-auto h-8 w-8 text-[var(--color-faint)]" />
      <p class="mt-3 text-[13px] text-[var(--color-muted)]">资源库里还没有刮削完成的资源</p>
      <p class="mt-1 text-[13px] text-[var(--color-faint)]">录入的链接要先在「刮削队列」完成 TMDB 刮削才会进库</p>
      <div class="mt-4 flex justify-center gap-2">
        <Button size="sm" @click="emit('goto', 'import')">
          <Plus class="h-4 w-4" />
          去录入
        </Button>
        <Button size="sm" variant="outline" @click="gotoQueue">
          <Sparkles class="h-4 w-4" />
          去刮削队列
        </Button>
      </div>
    </div>

    <template v-else>
      <div
        v-if="loadError"
        class="mb-4 flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]"
      >
        <AlertCircle class="h-4 w-4 shrink-0" />
        <span class="min-w-0 flex-1">加载失败：{{ loadError }}</span>
        <Button size="sm" variant="outline" @click="load">
          <RotateCcw class="h-3.5 w-3.5" />
          重试
        </Button>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <div class="relative w-full max-w-xs">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-faint)]" />
          <Input v-model="keyword" class="pl-9" placeholder="搜索片名 / 链接 / TMDB ID" />
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

        <div class="ml-auto flex items-center gap-2">
          <Button v-if="keyword || status !== 'scraped_set'" variant="ghost" size="sm" @click="reset">
            清空筛选
          </Button>
          <Button variant="outline" size="sm" :disabled="!selected.length" @click="removeSelected">
            <Trash2 class="h-4 w-4" />
            删除所选 {{ selected.length || '' }}
          </Button>
          <Button variant="outline" size="sm" :disabled="exporting" @click="exportCsv">
            <Download class="h-4 w-4" />
            导出 CSV
          </Button>
        </div>
      </div>

      <!-- 海报墙 -->
      <div v-if="mode === 'grid'" class="mt-4">
        <div v-if="loading" class="py-10 text-center text-[var(--color-muted)]">
          <Loader2 class="mx-auto h-5 w-5 animate-spin" />
        </div>
        <div v-else-if="!items.length" class="py-10 text-center text-[13px] text-[var(--color-muted)]">
          没有符合条件的记录
        </div>
        <div v-else class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
          <div v-for="l in items" :key="l.id">
            <div class="relative">
              <button class="block w-full text-left" @click="detail = l">
                <div
                  class="relative aspect-[2/3] overflow-hidden rounded-lg border border-[var(--color-line)] bg-[var(--color-surface-hover)] bg-cover bg-center"
                  :style="l.poster_url ? { backgroundImage: `url(${l.poster_url})` } : {}"
                >
                  <Badge class="absolute bottom-2 left-2" :class="statusMeta[l.status]?.cls">
                    {{ statusMeta[l.status]?.label ?? l.status }}
                  </Badge>
                </div>
              </button>
              <!-- 海报墙也要能多选，否则批量删除只能回到列表视图 -->
              <label
                class="absolute right-2 top-2 flex h-6 w-6 cursor-pointer items-center justify-center rounded-[var(--radius-control)] border shadow-sm transition-colors"
                :class="
                  selected.includes(l.id)
                    ? 'border-[var(--color-brand)] bg-[var(--color-brand)] text-white'
                    : 'border-[var(--color-line-strong)] bg-[var(--color-surface)] text-transparent hover:text-[var(--color-muted)]'
                "
                :title="selected.includes(l.id) ? '取消选择' : '选择'"
              >
                <input
                  type="checkbox"
                  class="sr-only"
                  :checked="selected.includes(l.id)"
                  @change="toggleSelect(l.id)"
                />
                <Check class="h-3.5 w-3.5" />
              </label>
            </div>
            <p class="mt-2 truncate text-[13px] font-medium">{{ l.title || l.raw_title || '—' }}</p>
            <p class="flex items-center gap-1.5 text-xs text-[var(--color-muted)]">
              <span>{{ l.year || '—' }}</span>
              <Badge v-if="mediaMeta[l.media_type]" :class="mediaMeta[l.media_type].cls">
                {{ mediaMeta[l.media_type].label }}
              </Badge>
            </p>
          </div>
        </div>
      </div>

      <!-- 列表 -->
      <div v-else class="mt-4 overflow-x-auto">
        <table class="w-full border-collapse text-[13px]">
          <thead>
            <tr class="border-b border-[var(--color-line)] text-left text-[var(--color-muted)]">
              <th class="w-10 py-2 pr-2">
                <input type="checkbox" :checked="allChecked" @change="toggleAll" />
              </th>
              <th class="w-12 py-2 pr-3 font-medium"></th>
              <th class="py-2 pr-4 font-medium">片名</th>
              <th class="w-16 py-2 pr-4 font-medium">类型</th>
              <th class="w-16 py-2 pr-4 font-medium">年份</th>
              <th class="w-20 py-2 pr-4 font-medium">TMDB</th>
              <th class="w-20 py-2 pr-4 font-medium">提取码</th>
              <th class="w-24 py-2 pr-4 font-medium">状态</th>
              <th class="w-36 py-2 pr-4 font-medium">分享时间</th>
              <th class="w-24 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading">
              <td colspan="10" class="py-10 text-center text-[var(--color-muted)]">
                <Loader2 class="mx-auto h-5 w-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="!items.length">
              <td colspan="10" class="py-10 text-center text-[var(--color-muted)]">没有符合条件的记录</td>
            </tr>
            <tr
              v-for="l in items"
              :key="l.id"
              class="cursor-pointer border-b border-[var(--color-line)] last:border-0 hover:bg-[var(--color-surface-sunken)]"
              @click="detail = l"
            >
              <td class="py-2.5 pr-2" @click.stop>
                <input v-model="selected" type="checkbox" :value="l.id" />
              </td>
              <td class="py-2.5 pr-3">
                <div
                  class="h-[42px] w-7 rounded bg-[var(--color-surface-hover)] bg-cover bg-center"
                  :style="l.poster_url ? { backgroundImage: `url(${l.poster_url})` } : {}"
                />
              </td>
              <td class="py-2.5 pr-4">
                <div class="flex items-center gap-2">
                  <span class="font-medium">{{ l.title || l.raw_title || '—' }}</span>
                  <a
                    :href="l.url"
                    target="_blank"
                    rel="noreferrer"
                    class="text-[var(--color-faint)] hover:text-[var(--color-brand)]"
                    @click.stop
                  >
                    <ExternalLink class="h-3.5 w-3.5" />
                  </a>
                </div>
                <p v-if="l.raw_title && l.raw_title !== l.title" class="text-xs text-[var(--color-faint)]">
                  {{ l.raw_title }}
                </p>
              </td>
              <td class="py-2.5 pr-4">
                <Badge v-if="mediaMeta[l.media_type]" :class="mediaMeta[l.media_type].cls">
                  {{ mediaMeta[l.media_type].label }}
                </Badge>
                <span v-else class="text-[var(--color-muted)]">—</span>
              </td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ l.year || '—' }}</td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ l.tmdb_id || '—' }}</td>
              <td class="py-2.5 pr-4 font-mono text-[var(--color-muted)]">{{ l.code || '—' }}</td>
              <td class="py-2.5 pr-4">
                <Badge :class="statusMeta[l.status]?.cls">{{ statusMeta[l.status]?.label ?? l.status }}</Badge>
              </td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ l.shared_at || '—' }}</td>
              <td class="py-2.5" @click.stop>
                <div class="flex items-center gap-1">
                  <button
                    class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-brand)] disabled:opacity-40"
                    title="重新刮削"
                    :disabled="acting === l.id"
                    @click="scrapeOne(l.id)"
                  >
                    <Loader2 v-if="acting === l.id" class="h-4 w-4 animate-spin" />
                    <Sparkles v-else class="h-4 w-4" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-brand)] disabled:opacity-40"
                    title="推送到频道"
                    :disabled="acting === l.id"
                    @click="pushOne(l.id)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-bad)]"
                    title="删除"
                    @click="removeOne(l)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="mt-4 flex items-center justify-between text-[13px] text-[var(--color-muted)]">
        <span>第 {{ page + 1 }} / {{ pageCount }} 页</span>
        <div class="flex gap-2">
          <Button variant="outline" size="sm" :disabled="page === 0" @click="go(-1)">上一页</Button>
          <Button variant="outline" size="sm" :disabled="page + 1 >= pageCount" @click="go(1)">下一页</Button>
        </div>
      </div>
    </template>

    <!-- 详情抽屉 -->
    <div
      v-if="detail"
      class="fixed inset-0 z-50 flex justify-end bg-black/20"
      @click.self="detail = null"
    >
      <aside
        ref="drawer"
        tabindex="-1"
        class="h-full w-full max-w-md overflow-y-auto bg-[var(--color-surface)] p-5 outline-none"
        role="dialog"
        aria-modal="true"
        :aria-label="detail.title || detail.raw_title || '资源详情'"
        @keydown="onDrawerKey"
      >
        <div class="mb-4 flex items-start justify-between">
          <div class="min-w-0">
            <h3 class="text-[15px] font-medium">{{ detail.title || detail.raw_title }}</h3>
            <div class="mt-1 flex flex-wrap items-center gap-1.5">
              <Badge v-if="mediaMeta[detail.media_type]" :class="mediaMeta[detail.media_type].cls">
                {{ mediaMeta[detail.media_type].label }}
              </Badge>
              <span v-if="detail.year" class="text-[13px] text-[var(--color-muted)]">{{ detail.year }}</span>
            </div>
            <p v-if="detail.original_title" class="text-[13px] text-[var(--color-muted)]">
              {{ detail.original_title }}
            </p>
          </div>
          <button
            class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]"
            aria-label="关闭"
            @click="detail = null"
          >
            <X class="h-4 w-4" />
          </button>
        </div>

        <div
          class="mb-4 aspect-[2/3] w-40 overflow-hidden rounded-lg border border-[var(--color-line)] bg-[var(--color-surface-hover)] bg-cover bg-center"
          :style="detail.poster_url ? { backgroundImage: `url(${detail.poster_url})` } : {}"
        />

        <dl class="space-y-2 text-[13px]">
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">原名</dt>
            <dd>{{ detail.original_title || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">年份</dt>
            <dd>{{ detail.year || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">影视类型</dt>
            <dd>{{ mediaLabel(detail.media_type) || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">TMDB</dt>
            <dd>{{ detail.tmdb_id || '—' }} {{ detail.media_type ? `(${detail.media_type})` : '' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">评分</dt>
            <dd>{{ detail.vote_average ? detail.vote_average.toFixed(1) : '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">类型</dt>
            <dd>{{ detail.genres || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">提取码</dt>
            <dd class="font-mono">{{ detail.code || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">状态</dt>
            <dd>
              <Badge :class="statusMeta[detail.status]?.cls">
                {{ statusMeta[detail.status]?.label ?? detail.status }}
              </Badge>
            </dd>
          </div>
          <div v-if="detail.error" class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">错误</dt>
            <dd class="text-[var(--color-bad)]">{{ detail.error }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">简介</dt>
            <dd class="text-[var(--color-muted)]">{{ detail.overview || '—' }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-16 shrink-0 text-[var(--color-muted)]">链接</dt>
            <dd class="break-all">
              <a :href="detail.url" target="_blank" rel="noreferrer" class="text-[var(--color-brand)]">
                {{ detail.url }}
              </a>
            </dd>
          </div>
        </dl>

        <div class="mt-5 flex gap-2">
          <Button size="sm" :disabled="acting === detail.id" @click="scrapeOne(detail.id)">
            <Sparkles class="h-4 w-4" />
            重新刮削
          </Button>
          <Button size="sm" variant="outline" :disabled="acting === detail.id" @click="pushOne(detail.id)">
            <Send class="h-4 w-4" />
            推送
          </Button>
          <Button size="sm" variant="ghost" @click="removeOne(detail)">
            <Trash2 class="h-4 w-4" />
          </Button>
        </div>
      </aside>
    </div>
  </Card>
</template>
