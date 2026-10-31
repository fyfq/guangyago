<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  AlertCircle,
  ChevronDown,
  Copy,
  ExternalLink,
  Eye,
  Image as ImageIcon,
  Loader2,
  MousePointerClick,
  TrendingUp,
  Users,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import { api, mediaLabel, type PortalLink, type PortalShare, type PortalTraffic } from '../lib/api'
import { branding } from '../lib/branding'
import { safeExternalUrl } from '../lib/utils'

const ranges = [7, 30, 90] as const
const days = ref<(typeof ranges)[number]>(30)
const data = ref<PortalTraffic | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  hoverIndex.value = null
  try {
    data.value = await api.portalTraffic(days.value)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '统计加载失败'
  } finally {
    loading.value = false
  }
}

function pick(n: (typeof ranges)[number]) {
  if (n === days.value) return
  days.value = n
  closeDetail()
  load()
}

const openID = ref<number | null>(null)
const detail = ref<PortalLink | null>(null)
const detailLoading = ref(false)
const detailError = ref('')

let detailSeq = 0

async function loadDetail(id: number) {
  const seq = ++detailSeq
  detail.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const d = await api.portalTrafficLink(id)
    if (seq === detailSeq) detail.value = d
  } catch (e) {
    if (seq === detailSeq) detailError.value = e instanceof Error ? e.message : '详情加载失败'
  } finally {
    if (seq === detailSeq) detailLoading.value = false
  }
}

function toggle(id: number) {
  if (openID.value === id) {
    closeDetail()
    return
  }
  openID.value = id
  loadDetail(id)
}

function closeDetail() {
  detailSeq++
  openID.value = null
  detail.value = null
  detailError.value = ''
  detailLoading.value = false
}

function shares(d: PortalLink): PortalShare[] {
  if (d.links?.length) return d.links
  return d.url ? [{ id: d.id, url: d.url, code: d.code, shared_at: d.shared_at }] : []
}

// 打开某条分享时按分享计数，本行先加一再静默刷新榜单
async function openShare(s: PortalShare) {
  s.opens = (s.opens ?? 0) + 1
  try {
    await api.portalOpen(s.id)
    load()
  } catch {
    s.opens -= 1
  }
}

function interactions(t: { browses: number; copies: number; opens: number }) {
  return t.browses + t.copies + t.opens
}

const sortKeys = [
  { key: 'total', label: '合计' },
  { key: 'browses', label: '浏览' },
  { key: 'copies', label: '复制' },
  { key: 'opens', label: '打开' },
] as const
type SortKey = (typeof sortKeys)[number]['key']

const sortKey = ref<SortKey>('total')

function metric(
  t: { browses: number; copies: number; opens: number },
  key: SortKey = sortKey.value,
) {
  if (key === 'browses') return t.browses
  if (key === 'copies') return t.copies
  if (key === 'opens') return t.opens
  return interactions(t)
}

// 榜单只在本地重排，切换排序不重新请求
const top = computed(() =>
  [...(data.value?.top ?? [])].sort((a, b) => metric(b) - metric(a)),
)

// 明细表的列顺序：三项互动在前，合计收口
const tableCols = [
  { key: 'browses', label: '浏览' },
  { key: 'copies', label: '复制' },
  { key: 'opens', label: '打开' },
  { key: 'total', label: '合计' },
] as const

const cards = computed(() => [
  { key: 'today_pv', label: '今日访问', value: data.value?.today_pv ?? 0, icon: Eye },
  { key: 'today_uv', label: '今日访客', value: data.value?.today_uv ?? 0, icon: Users },
  { key: 'range_browses', label: `${days.value} 天浏览`, value: data.value?.range_browses ?? 0, icon: ImageIcon },
  { key: 'range_copies', label: `${days.value} 天复制`, value: data.value?.range_copies ?? 0, icon: Copy },
  { key: 'range_opens', label: `${days.value} 天打开`, value: data.value?.range_opens ?? 0, icon: MousePointerClick },
  {
    key: 'total_interactions',
    label: '累计互动',
    value: data.value ? interactions({
      browses: data.value.total_browses,
      copies: data.value.total_copies,
      opens: data.value.total_opens,
    }) : 0,
    icon: TrendingUp,
  },
])

const empty = computed(() => {
  const d = data.value
  return (
    !d ||
    (d.range_pv === 0 && d.range_browses === 0 && d.range_copies === 0 && d.range_opens === 0)
  )
})

const chart = computed(() => {
  const trend = data.value?.trend ?? []
  const W = 720
  const H = 220
  const padL = 36
  const padR = 12
  const padT = 12
  const padB = 24
  const plotW = W - padL - padR
  const plotH = H - padT - padB
  const max = Math.max(1, ...trend.flatMap((d) => [d.pv, d.browses, d.copies, d.opens]))
  const x = (i: number) => (trend.length <= 1 ? padL : padL + (i * plotW) / (trend.length - 1))
  const y = (v: number) => H - padB - (v / max) * plotH
  const line = (key: 'pv' | 'browses' | 'copies' | 'opens') =>
    trend.map((d, i) => `${x(i).toFixed(1)},${y(d[key]).toFixed(1)}`).join(' ')
  const band = trend.length > 1 ? plotW / (trend.length - 1) : plotW
  // 刻度按整数步长生成，避免窗口很小时出现「2 2 1 1」这种重复标签
  const tickStep = Math.max(1, Math.ceil(max / 4))
  const ticks: { y: number; value: number }[] = []
  for (let v = 0; v <= max; v += tickStep) ticks.push({ y: y(v), value: v })
  // 点太密时只标注少量日期，避免横轴糊成一片
  const labelStep = Math.max(1, Math.ceil(trend.length / 7))
  return {
    W,
    H,
    padL,
    padR,
    padT,
    plotH,
    band,
    max,
    x,
    y,
    pv: line('pv'),
    browses: line('browses'),
    copies: line('copies'),
    opens: line('opens'),
    ticks,
    labels: trend
      .map((d, i) => ({ i, text: d.date.slice(5) }))
      .filter(({ i }) => i % labelStep === 0 || i === trend.length - 1),
  }
})

// hoverIndex 指向当前悬停/触摸的那一天，null 表示没有交互。
const hoverIndex = ref<number | null>(null)

const hover = computed(() => {
  const i = hoverIndex.value
  const d = i === null ? undefined : data.value?.trend[i]
  if (!d) return null
  const c = chart.value
  return {
    d,
    // 提示框跟随横轴位置，两端留出余量避免贴边被裁掉
    left: Math.min(92, Math.max(8, (c.x(i!) / c.W) * 100)),
    points: [
      { key: 'pv', color: 'var(--color-brand)', cx: c.x(i!), cy: c.y(d.pv) },
      { key: 'browses', color: 'var(--color-info)', cx: c.x(i!), cy: c.y(d.browses) },
      { key: 'copies', color: 'var(--color-warn)', cx: c.x(i!), cy: c.y(d.copies) },
      { key: 'opens', color: 'var(--color-bad)', cx: c.x(i!), cy: c.y(d.opens) },
    ],
  }
})

// followChart 把指针位置换算成离得最近的一天，缩放后的 SVG 也适用。
function followChart(e: PointerEvent) {
  const trend = data.value?.trend ?? []
  if (!trend.length) return
  const c = chart.value
  const rect = (e.currentTarget as SVGSVGElement).getBoundingClientRect()
  if (!rect.width) return
  const px = ((e.clientX - rect.left) / rect.width) * c.W
  const ratio = trend.length <= 1 ? 0 : (px - c.padL) / (c.W - c.padL - c.padR)
  const i = Math.round(ratio * (trend.length - 1))
  hoverIndex.value = Math.min(trend.length - 1, Math.max(0, i))
}

onMounted(() => {
  document.title = `统计 · ${branding.site_name}`
  load()
})
</script>

<template>
  <div class="space-y-5">
    <Card class="p-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-[13px] text-[var(--color-muted)]">门户访问与资源互动，按服务器本地日期统计</p>
        <div class="flex gap-1 rounded-[var(--radius-control)] bg-[var(--color-surface-sunken)] p-0.5">
          <button
            v-for="d in ranges"
            :key="d"
            class="rounded-md px-3 py-1 text-[13px] transition-colors"
            :class="
              days === d
                ? 'bg-[var(--color-surface)] font-medium shadow-sm'
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]'
            "
            :aria-pressed="days === d"
            @click="pick(d)"
          >
            {{ d }} 天
          </button>
        </div>
      </div>
    </Card>

    <div v-if="error" class="flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2 text-[13px] text-[var(--color-bad)]">
      <AlertCircle class="h-4 w-4 shrink-0" />
      {{ error }}
      <Button variant="ghost" size="sm" class="ml-auto" @click="load">重试</Button>
    </div>

    <div v-else-if="loading && !data" class="flex justify-center py-16 text-[var(--color-muted)]">
      <Loader2 class="h-5 w-5 animate-spin" />
    </div>

    <template v-else>
      <div class="grid grid-cols-3 gap-3 md:grid-cols-6">
        <div
          v-for="c in cards"
          :key="c.key"
          class="flex min-h-14 items-center justify-between gap-2 rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] px-3 py-2 shadow-[0_1px_3px_rgba(20,20,20,0.05)]"
        >
          <div>
            <p class="text-[12px] text-[var(--color-muted)]">{{ c.label }}</p>
            <p class="mt-0.5 text-base font-medium tabular-nums">{{ c.value }}</p>
          </div>
          <component :is="c.icon" class="h-3.5 w-3.5 shrink-0 text-[var(--color-faint)]" />
        </div>
      </div>

      <Card class="flex h-full flex-col">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h3 class="text-[15px] font-medium">访问趋势</h3>
          <div class="flex items-center gap-3 text-xs text-[var(--color-muted)]">
            <span class="flex items-center gap-1"><i class="inline-block h-0.5 w-3 rounded bg-[var(--color-brand)]"></i>访问</span>
            <span class="flex items-center gap-1"><i class="inline-block h-0.5 w-3 rounded bg-[var(--color-info)]"></i>浏览</span>
            <span class="flex items-center gap-1"><i class="inline-block h-0.5 w-3 rounded bg-[var(--color-warn)]"></i>复制</span>
            <span class="flex items-center gap-1"><i class="inline-block h-0.5 w-3 rounded bg-[var(--color-bad)]"></i>打开</span>
          </div>
        </div>

        <div class="relative flex-1">
          <svg
            :viewBox="`0 0 ${chart.W} ${chart.H}`"
            class="w-full cursor-crosshair touch-pan-y"
            role="img"
            aria-label="门户访问趋势，包含访问、浏览、复制与打开次数"
            @pointermove="followChart"
            @pointerdown="followChart"
            @pointerleave="hoverIndex = null"
            @pointercancel="hoverIndex = null"
          >
            <g>
              <line
                v-for="g in chart.ticks"
                :key="g.value"
                :x1="36"
                :x2="chart.W - 12"
                :y1="g.y"
                :y2="g.y"
                stroke="var(--color-line)"
                stroke-width="1"
              />
              <text
                v-for="g in chart.ticks"
                :key="`t-${g.value}`"
                :x="30"
                :y="g.y + 3"
                text-anchor="end"
                font-size="10"
                fill="var(--color-faint)"
              >
                {{ g.value }}
              </text>
            </g>
            <g
              v-for="(d, i) in data?.trend ?? []"
              :key="`band-${d.date}`"
            >
              <rect
                :x="chart.x(i) - chart.band / 2"
                :y="chart.padT"
                :width="chart.band"
                :height="chart.plotH"
                fill="transparent"
              >
                <title>{{ d.date }}：访问 {{ d.pv }} · 浏览 {{ d.browses }} · 复制 {{ d.copies }} · 打开 {{ d.opens }}</title>
              </rect>
            </g>
            <polyline :points="chart.pv" fill="none" stroke="var(--color-brand)" stroke-width="2" />
            <polyline :points="chart.browses" fill="none" stroke="var(--color-info)" stroke-width="2" />
            <polyline :points="chart.copies" fill="none" stroke="var(--color-warn)" stroke-width="2" />
            <polyline :points="chart.opens" fill="none" stroke="var(--color-bad)" stroke-width="2" />
            <g v-if="hover && hoverIndex !== null">
              <line
                :x1="chart.x(hoverIndex)"
                :x2="chart.x(hoverIndex)"
                :y1="chart.padT"
                :y2="chart.H - 24"
                stroke="var(--color-line)"
                stroke-width="1"
                stroke-dasharray="3 3"
              />
              <circle
                v-for="p in hover.points"
                :key="`hp-${p.key}`"
                :cx="p.cx"
                :cy="p.cy"
                r="3"
                :fill="p.color"
                stroke="var(--color-surface)"
                stroke-width="1.5"
              />
            </g>
            <text
              v-for="l in chart.labels"
              :key="`x-${l.i}`"
              :x="chart.x(l.i)"
              :y="chart.H - 6"
              text-anchor="middle"
              font-size="10"
              fill="var(--color-faint)"
            >
              {{ l.text }}
            </text>
          </svg>

          <div
            v-if="hover"
            class="pointer-events-none absolute top-0 z-10 w-max -translate-x-1/2 rounded-lg border border-[var(--color-line)] bg-[var(--color-surface)] px-2.5 py-2 text-xs shadow-[0_4px_16px_rgba(20,20,20,0.12)]"
            :style="{ left: `${hover.left}%` }"
          >
            <p class="mb-1 text-[var(--color-muted)]">{{ hover.d.date }}</p>
            <div class="space-y-0.5 tabular-nums">
              <p class="flex items-center gap-1.5">
                <i class="inline-block h-0.5 w-3 rounded bg-[var(--color-brand)]"></i>访问 {{ hover.d.pv }}
              </p>
              <p class="flex items-center gap-1.5">
                <i class="inline-block h-0.5 w-3 rounded bg-[var(--color-info)]"></i>浏览 {{ hover.d.browses }}
              </p>
              <p class="flex items-center gap-1.5">
                <i class="inline-block h-0.5 w-3 rounded bg-[var(--color-warn)]"></i>复制 {{ hover.d.copies }}
              </p>
              <p class="flex items-center gap-1.5">
                <i class="inline-block h-0.5 w-3 rounded bg-[var(--color-bad)]"></i>打开 {{ hover.d.opens }}
              </p>
            </div>
          </div>

          <div
            v-if="empty"
            class="pointer-events-none absolute inset-0 flex items-center justify-center"
          >
            <p class="rounded-lg bg-[var(--color-surface)] px-3 py-2 text-[13px] text-[var(--color-muted)] shadow-sm">
              还没有访问数据，门户有访客后这里开始累计。
            </p>
          </div>
        </div>

        <p class="mt-2 text-xs text-[var(--color-muted)]">
          区间合计 访问 {{ data?.range_pv ?? 0 }} · 访客 {{ data?.range_uv ?? 0 }} · 浏览
          {{ data?.range_browses ?? 0 }} · 复制 {{ data?.range_copies ?? 0 }} · 打开
          {{ data?.range_opens ?? 0 }}
        </p>
      </Card>

      <Card class="p-0">
        <div class="flex items-center justify-between gap-2 border-b border-[var(--color-line)] px-4 py-2.5">
          <h3 class="text-[15px] font-medium">资源明细</h3>
          <span class="text-xs text-[var(--color-faint)]">点行展开分享链接</span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[560px] text-[13px]">
            <thead>
              <tr class="bg-[var(--color-surface-sunken)] text-xs text-[var(--color-muted)]">
                <th class="w-7 text-center"></th>
                <th class="w-8 py-2 text-center font-normal">#</th>
                <th class="py-2 pl-1 pr-2 text-left font-normal">资源</th>
                <th v-for="c in tableCols" :key="c.key" class="w-14 py-2 text-center font-normal">
                  <button
                    type="button"
                    class="inline-flex items-center justify-center gap-0.5 rounded px-1 py-0.5 transition-colors hover:text-[var(--color-ink)]"
                    :class="sortKey === c.key ? 'font-medium text-[var(--color-ink)]' : ''"
                    :aria-pressed="sortKey === c.key"
                    @click="sortKey = c.key"
                  >
                    {{ c.label }}
                    <ChevronDown v-if="sortKey === c.key" class="h-3 w-3" />
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              <template v-for="(t, i) in top" :key="t.id">
                <tr
                  class="group cursor-pointer border-b border-[var(--color-line)] transition-colors hover:bg-[var(--color-surface-hover)]"
                  :class="openID === t.id ? 'bg-[var(--color-surface-sunken)]' : ''"
                  @click="toggle(t.id)"
                >
                  <td class="text-center">
                    <ChevronDown
                      class="mx-auto h-3.5 w-3.5 text-[var(--color-faint)] opacity-40 transition-all group-hover:opacity-100"
                      :class="openID === t.id ? 'rotate-180 opacity-100' : ''"
                    />
                  </td>
                  <td class="py-2 text-center text-xs tabular-nums text-[var(--color-faint)]">{{ i + 1 }}</td>
                  <td class="py-1.5 pl-1 pr-2">
                    <div class="flex items-center gap-2.5">
                      <img
                        v-if="t.poster_url"
                        :src="t.poster_url"
                        :alt="t.title"
                        loading="lazy"
                        class="h-11 w-8 shrink-0 rounded object-cover shadow-sm"
                      />
                      <div v-else class="flex h-11 w-8 shrink-0 items-center justify-center rounded bg-[var(--color-surface-sunken)]">
                        <Eye class="h-4 w-4 text-[var(--color-faint)]" />
                      </div>
                      <div class="min-w-0 flex-1">
                        <p class="truncate font-medium">{{ t.title || '未命名资源' }}</p>
                        <p class="mt-0.5 text-xs text-[var(--color-muted)]">
                          {{ [t.year || '', mediaLabel(t.media_type)].filter(Boolean).join(' · ') }}
                        </p>
                      </div>
                    </div>
                  </td>
                  <td
                    v-for="c in tableCols"
                    :key="c.key"
                    class="py-2 text-center tabular-nums"
                    :class="sortKey === c.key ? 'font-medium' : ''"
                  >
                    {{ metric(t, c.key) }}
                  </td>
                </tr>
                <tr v-if="openID === t.id" class="border-b border-[var(--color-line)] bg-[var(--color-surface-sunken)]">
                  <td colspan="2"></td>
                  <td class="py-2 pl-1 pr-2">
                    <div v-if="detailLoading" class="flex items-center gap-2 text-xs text-[var(--color-muted)]">
                      <Loader2 class="h-3.5 w-3.5 animate-spin" />
                      加载中
                    </div>
                    <div v-else-if="detailError" class="flex items-center gap-2 text-xs text-[var(--color-bad)]">
                      <AlertCircle class="h-3.5 w-3.5 shrink-0" />
                      {{ detailError }}
                      <Button variant="ghost" size="sm" class="ml-auto" @click.stop="loadDetail(t.id)">重试</Button>
                    </div>
                    <div v-else-if="detail && shares(detail).length" class="space-y-0">
                      <div
                        v-for="(s, si) in shares(detail)"
                        :key="s.id || si"
                        class="flex items-center gap-2 border-b border-[var(--color-line)] py-2 text-xs last:border-0 first:pt-0"
                      >
                        <span class="shrink-0 rounded bg-[var(--color-surface)] px-1.5 py-0.5 font-medium text-[var(--color-muted)]">#{{ si + 1 }}</span>
                        <a
                          v-if="s.url && safeExternalUrl(s.url)"
                          :href="safeExternalUrl(s.url)"
                          target="_blank"
                          rel="noopener noreferrer"
                          class="flex shrink-0 items-center gap-1 text-[var(--color-brand)] hover:underline"
                          @click.stop="openShare(s)"
                        >
                          <ExternalLink class="h-3 w-3" />
                          打开
                        </a>
                        <span v-if="s.code" class="shrink-0 text-[var(--color-faint)]">
                          码 <span class="font-mono">{{ s.code }}</span>
                        </span>
                        <span v-if="s.shared_at" class="shrink-0 text-[var(--color-faint)]">
                          {{ s.shared_at.slice(5, 10) }}
                        </span>
                      </div>
                    </div>
                    <p v-else class="py-2 text-xs text-[var(--color-faint)]">暂无可用分享链接</p>
                  </td>
                  <td
                    v-for="c in tableCols"
                    :key="c.key"
                    class="py-2 text-center text-xs tabular-nums text-[var(--color-muted)]"
                  >
                    <div v-if="detailLoading || detailError || !detail || !shares(detail).length" class="text-[var(--color-faint)]">—</div>
                    <div v-else>
                      <div
                        v-for="(s, si) in shares(detail)"
                        :key="s.id || si"
                        class="border-b border-[var(--color-line)] py-2 last:border-0 first:pt-0"
                      >
                        {{ c.key === 'total' ? (s.browses ?? 0) + (s.copies ?? 0) + (s.opens ?? 0) : (s[c.key] ?? 0) }}
                      </div>
                    </div>
                  </td>
                </tr>
              </template>
              <tr v-if="!top.length">
                <td colspan="6" class="py-8 text-center text-[var(--color-muted)]">
                  暂无互动记录。
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </Card>
    </template>
  </div>
</template>
