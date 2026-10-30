<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  Save,
  CheckCircle2,
  KeyRound,
  Send,
  Database,
  Shield,
  Loader2,
  Gauge,
  RotateCcw,
  Zap,
  Download,
  Globe,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import TestResultLine from '../components/TestResult.vue'
import { api, type Settings, type TestResult } from '../lib/api'

const form = ref({
  tmdb_api_key: '',
  tg_token: '',
  chat_id: '',
  auto_push: true,
  save_to_file: true,
  public_url: '',
  proxy_enable: false,
  proxy_addr: '',
  proxy_username: '',
  proxy_password: '',
})

// 限流：默认值即官方建议值
const DEFAULTS = {
  tmdb_per_second: 4,
  tmdb_burst: 4,
  telegram_interval_ms: 3100,
  guangya_concurrency: 2,
}
const rate = ref({
  tmdb_enable: true,
  tmdb_per_second: DEFAULTS.tmdb_per_second,
  tmdb_burst: DEFAULTS.tmdb_burst,
  tmdb_shared: false,
  tg_enable: true,
  tg_interval_ms: DEFAULTS.telegram_interval_ms,
  tg_channel_only: false,
  tg_shared: false,
  tg_paid: false,
  gy_enable: true,
  gy_concurrency: DEFAULTS.guangya_concurrency,
})

/** Telegram 生效间隔：未开付费广播时按官方下限夹一次，共享凭据再放慢 */
const tgEffective = computed(() => {
  let ms = rate.value.tg_interval_ms || DEFAULTS.telegram_interval_ms
  if (!rate.value.tg_paid) {
    const floor = rate.value.tg_channel_only ? 1000 : 3000
    if (ms < floor) ms = floor
  }
  if (rate.value.tg_shared) ms = Math.round(ms * 1.5)
  return ms
})
const tgPerMinute = computed(() => (tgEffective.value ? (60000 / tgEffective.value).toFixed(1) : '0'))

/** TMDB 生效速率：共享凭据减半 */
const tmdbEffective = computed(() => {
  let v = rate.value.tmdb_per_second || DEFAULTS.tmdb_per_second
  if (rate.value.tmdb_shared) v = v / 2
  return Math.min(Math.max(v, 0.1), 40).toFixed(1)
})

function resetRate() {
  rate.value.tmdb_enable = true
  rate.value.tmdb_per_second = DEFAULTS.tmdb_per_second
  rate.value.tmdb_burst = DEFAULTS.tmdb_burst
  rate.value.tmdb_shared = false
  rate.value.tg_enable = true
  rate.value.tg_interval_ms = DEFAULTS.telegram_interval_ms
  rate.value.tg_channel_only = false
  rate.value.tg_shared = false
  rate.value.tg_paid = false
  rate.value.gy_enable = true
  rate.value.gy_concurrency = DEFAULTS.guangya_concurrency
}
const current = ref<Settings | null>(null)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

const steps = [
  { title: '录入链接', desc: '粘贴分享文本或上传 CSV，按 shareId 自动去重' },
  { title: 'TMDB 刮削', desc: '用文件名里的 TMDB ID 直取详情与海报，缺失时按标题搜索' },
  { title: '推送频道', desc: '先预览卡片，确认后批量推送到 Telegram 频道' },
  { title: '资源库回看', desc: '查看状态、搜索筛选、导出 CSV、失败重推' },
]

type Target = 'tmdb' | 'telegram' | 'proxy'
const testing = ref<Target | null>(null)
const results = ref<Partial<Record<Target, TestResult>>>({})

// 输入一变，上一次的测试结论就过期了，直接撤掉免得误导
watch(
  () => form.value.tmdb_api_key,
  () => delete results.value.tmdb,
)
watch(
  () => [form.value.tg_token, form.value.chat_id],
  () => delete results.value.telegram,
)
// 代理是 TMDB/Telegram 测试的共同出网路径，改了它三份结论都作废
watch(
  () => [
    form.value.proxy_enable,
    form.value.proxy_addr,
    form.value.proxy_username,
    form.value.proxy_password,
  ],
  () => {
    delete results.value.proxy
    delete results.value.tmdb
    delete results.value.telegram
  },
)

async function load() {
  try {
    current.value = await api.settings()
    form.value.auto_push = current.value.auto_push
    form.value.public_url = current.value.public_url || ''
    // 频道 ID 不是密钥，直接回显；'0' 是历史脏值，按未设置处理
    form.value.chat_id = current.value.chat_id && current.value.chat_id !== '0' ? current.value.chat_id : ''
    const p = current.value.proxy
    if (p) {
      form.value.proxy_enable = p.enable
      form.value.proxy_addr = p.addr || ''
      form.value.proxy_username = p.username || ''
    }
    const r = current.value.rate_limit
    if (r) {
      rate.value.tmdb_enable = r.tmdb.enable
      rate.value.tmdb_per_second = r.tmdb.per_second || DEFAULTS.tmdb_per_second
      rate.value.tmdb_burst = r.tmdb.burst || DEFAULTS.tmdb_burst
      rate.value.tmdb_shared = r.tmdb.shared_credentials
      rate.value.tg_enable = r.telegram.enable
      rate.value.tg_interval_ms = r.telegram.interval_ms || DEFAULTS.telegram_interval_ms
      rate.value.tg_channel_only = r.telegram.channel_only
      rate.value.tg_shared = r.telegram.shared_credentials
      rate.value.tg_paid = r.telegram.paid_broadcast
      rate.value.gy_enable = r.guangya.enable
      rate.value.gy_concurrency = r.guangya.concurrency || DEFAULTS.guangya_concurrency
    }
  } catch {
    current.value = null
  }
}

async function runTest(target: Target) {
  testing.value = target
  const r = results.value
  delete r[target]
  try {
    const body: Record<string, unknown> = { target }
    // 未保存的临时输入优先，便于"先试再存"
    if (target === 'tmdb' && form.value.tmdb_api_key) body.tmdb_api_key = form.value.tmdb_api_key
    if (target === 'telegram') {
      if (form.value.tg_token) body.tg_token = form.value.tg_token
      if (form.value.chat_id) body.chat_id = form.value.chat_id.trim()
    }
    // 代理是全局出网参数，TMDB 与 Telegram 的测试同样要走它
    body.proxy_enable = form.value.proxy_enable
    if (form.value.proxy_addr) body.proxy_addr = form.value.proxy_addr
    if (form.value.proxy_username) body.proxy_username = form.value.proxy_username
    if (form.value.proxy_password) body.proxy_password = form.value.proxy_password
    results.value = { ...r, [target]: await api.test(body as never) }
  } catch (e) {
    results.value = {
      ...r,
      [target]: { ok: false, message: e instanceof Error ? e.message : '测试请求失败', detail: '', elapsed_ms: 0 },
    }
  } finally {
    testing.value = null
  }
}

async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    // 地址只在真的改过时提交：没读到过配置（load 失败）就发空串会把已设的对外地址清掉
    const loaded = current.value
    const publicURL = form.value.public_url.trim()
    await api.saveSettings({
      tmdb_api_key: form.value.tmdb_api_key || undefined,
      tg_token: form.value.tg_token || undefined,
      chat_id: form.value.chat_id.trim() || undefined,
      auto_push: form.value.auto_push,
      save_to_file: form.value.save_to_file,
      public_url: loaded && publicURL !== loaded.public_url ? publicURL : undefined,
      proxy: {
        enable: form.value.proxy_enable,
        addr: form.value.proxy_addr || undefined,
        username: form.value.proxy_username || undefined,
        password: form.value.proxy_password || undefined,
      },
      rate_limit: {
        tmdb: {
          enable: rate.value.tmdb_enable,
          per_second: rate.value.tmdb_per_second,
          burst: rate.value.tmdb_burst,
          shared_credentials: rate.value.tmdb_shared,
        },
        telegram: {
          enable: rate.value.tg_enable,
          interval_ms: rate.value.tg_interval_ms,
          channel_only: rate.value.tg_channel_only,
          shared_credentials: rate.value.tg_shared,
          paid_broadcast: rate.value.tg_paid,
        },
        guangya: {
          enable: rate.value.gy_enable,
          concurrency: rate.value.gy_concurrency,
        },
      },
    })
    saved.value = true
    form.value.tmdb_api_key = ''
    form.value.tg_token = ''
    form.value.proxy_password = ''
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="grid gap-5 lg:grid-cols-2">
    <Card class="flex flex-col" title="对外地址与密钥" desc="地址供反代/隧道做同源校验；密钥留空表示不修改，填完点测试验证再保存">
      <div class="flex flex-1 flex-col gap-4">
        <div>
          <label class="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
            <Globe class="h-4 w-4" />
            对外访问地址
          </label>
          <Input v-model="form.public_url" placeholder="https://gy.example.com" />
          <p class="mt-1.5 text-xs text-[var(--color-faint)]">
            走反向代理或 Cloudflare Tunnel 时必填：Origin 校验与站点地图都以它为准，留空则按请求 Host 推断。
          </p>
        </div>

        <div>
          <label class="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
            <KeyRound class="h-4 w-4" />
            TMDB API Key (v3)
          </label>
          <div class="flex gap-2">
            <Input v-model="form.tmdb_api_key" placeholder="32 位 API Key 或 eyJ 开头的 Read Access Token" />
            <Button variant="outline" size="md" class="shrink-0" :disabled="testing === 'tmdb'" @click="runTest('tmdb')">
              <Loader2 v-if="testing === 'tmdb'" class="h-4 w-4 animate-spin" />
              测试
            </Button>
          </div>
          <p v-if="current?.tmdb_configured" class="mt-1.5 flex items-center gap-1 text-xs text-[var(--color-ok)]">
            <CheckCircle2 class="h-3.5 w-3.5" />
            已配置
          </p>
          <TestResultLine :result="results.tmdb" />
        </div>

        <div>
          <label class="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
            <Send class="h-4 w-4" />
            Telegram Bot Token
          </label>
          <div class="flex gap-2">
            <Input v-model="form.tg_token" placeholder="由 @BotFather 创建" />
            <Button
              variant="outline"
              size="md"
              class="shrink-0"
              :disabled="testing === 'telegram'"
              @click="runTest('telegram')"
            >
              <Loader2 v-if="testing === 'telegram'" class="h-4 w-4 animate-spin" />
              测试
            </Button>
          </div>
          <p v-if="current?.telegram_configured" class="mt-1.5 flex items-center gap-1 text-xs text-[var(--color-ok)]">
            <CheckCircle2 class="h-3.5 w-3.5" />
            已配置
          </p>
          <TestResultLine :result="results.telegram" />
        </div>

        <div>
          <label class="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium">
            <Database class="h-4 w-4" />
            频道 ID
          </label>
          <Input v-model="form.chat_id" placeholder="例如 -1001234567890 或 @频道用户名" />
          <p class="mt-1.5 text-xs text-[var(--color-muted)]">
            数字 ID（-100 开头）或频道用户名（@xxx，@ 可省略）都支持；把 Bot 加进频道设为管理员后，测试会顺带校验身份
          </p>
        </div>

        <div class="rounded-lg border border-[var(--color-line)] p-3">
          <div class="flex items-center justify-between">
            <label class="flex items-center gap-1.5 text-[13px] font-medium">
              <Shield class="h-4 w-4" />
              代理（SOCKS5 / HTTP）
            </label>
            <label class="flex items-center gap-1.5 text-[13px] text-[var(--color-muted)]">
              <input v-model="form.proxy_enable" type="checkbox" />
              启用
            </label>
          </div>

          <div v-if="form.proxy_enable" class="mt-3 space-y-2">
            <div class="flex gap-2">
              <Input v-model="form.proxy_addr" placeholder="127.0.0.1:7890 或 socks5://host:port" />
              <Button
                variant="outline"
                size="md"
                class="shrink-0"
                :disabled="testing === 'proxy'"
                @click="runTest('proxy')"
              >
                <Loader2 v-if="testing === 'proxy'" class="h-4 w-4 animate-spin" />
                测试
              </Button>
            </div>
            <div class="flex gap-2">
              <Input v-model="form.proxy_username" placeholder="用户名（可留空）" />
              <Input v-model="form.proxy_password" type="password" placeholder="密码（可留空）" />
            </div>
            <p class="text-xs text-[var(--color-muted)]">
              留空则默认按 socks5 处理；TMDB 与 Telegram 的出网请求都会走这个代理
            </p>
            <TestResultLine :result="results.proxy" />
          </div>
        </div>

        <label class="flex items-center gap-2 text-[13px]">
          <input v-model="form.auto_push" type="checkbox" />
          Bot 收到链接后自动刮削
        </label>

        <label class="flex items-center gap-2 text-[13px]">
          <input v-model="form.save_to_file" type="checkbox" />
          同时写入配置文件，重启后仍然有效
        </label>

        <div class="mt-auto flex flex-wrap items-center gap-3 border-t border-[var(--color-line)] pt-4">
          <Button :disabled="saving" @click="save">
            <Save class="h-4 w-4" />
            保存配置
          </Button>
          <span v-if="saved" class="flex items-center gap-1 text-[13px] text-[var(--color-ok)]">
            <CheckCircle2 class="h-4 w-4" />
            已保存并生效
          </span>
          <span v-if="error" class="text-[13px] text-[var(--color-bad)]">{{ error }}</span>
        </div>
      </div>
    </Card>

    <Card class="flex flex-col" title="使用步骤" desc="从录入到推送的完整流程">
      <ol class="flex-1 space-y-3 text-[13px]">
        <li v-for="(s, i) in steps" :key="s.title" class="flex gap-3">
          <span
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[var(--color-brand-soft)] text-[12px] font-medium text-[var(--color-brand)]"
          >{{ i + 1 }}</span>
          <div>
            <p class="font-medium">{{ s.title }}</p>
            <p class="text-[var(--color-muted)]">{{ s.desc }}</p>
          </div>
        </li>
      </ol>

      <div class="mt-5 rounded-lg bg-[var(--color-surface-sunken)] p-3">
        <p class="mb-2 text-[13px] font-medium text-[var(--color-ink)]">当前配置状态</p>
        <dl class="grid grid-cols-[4em_1fr] gap-x-3 gap-y-1 text-[13px]">
          <dt class="text-[var(--color-muted)]">TMDB</dt>
          <dd :class="current?.tmdb_configured ? 'text-[var(--color-ok)]' : 'text-[var(--color-bad)]'">
            {{ current?.tmdb_configured ? '已配置' : '未配置' }}
          </dd>
          <dt class="text-[var(--color-muted)]">Telegram</dt>
          <dd :class="current?.telegram_configured ? 'text-[var(--color-ok)]' : 'text-[var(--color-bad)]'">
            {{ current?.telegram_configured ? '已配置' : '未配置' }}
          </dd>
          <dt class="text-[var(--color-muted)]">频道</dt>
          <dd class="truncate">{{ current?.chat_id && current.chat_id !== '0' ? current.chat_id : '未设置' }}</dd>
          <dt class="text-[var(--color-muted)]">代理</dt>
          <dd :class="current?.proxy?.enable ? 'text-[var(--color-ok)]' : 'text-[var(--color-muted)]'">
            {{ current?.proxy?.enable ? current.proxy.addr || '已启用' : '未启用' }}
          </dd>
          <dt class="text-[var(--color-muted)]">Worker</dt>
          <dd>{{ current?.workers ?? '-' }}</dd>
        </dl>
      </div>
    </Card>

    <Card
      class="lg:col-span-2"
      desc="默认值即官方建议值，改小更保守；保存后即时生效"
    >
      <template #title>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-[15px] font-medium">限流</h2>
          <button
            class="flex items-center gap-1 text-[13px] text-[var(--color-muted)] hover:text-[var(--color-brand)]"
            @click="resetRate"
          >
            <RotateCcw class="h-3.5 w-3.5" />
            恢复官方默认值
          </button>
        </div>
      </template>

      <div class="grid gap-4 lg:grid-cols-3">
        <!-- TMDB -->
        <div class="flex flex-col rounded-lg border border-[var(--color-line)] p-3">
          <div class="flex items-center justify-between">
            <label class="flex items-center gap-1.5 text-[13px] font-medium">
              <Gauge class="h-4 w-4" />
              TMDB
            </label>
            <label class="flex items-center gap-1.5 text-[13px] text-[var(--color-muted)]">
              <input v-model="rate.tmdb_enable" type="checkbox" />
              启用
            </label>
          </div>
          <div v-if="rate.tmdb_enable" class="mt-3 grid grid-cols-2 gap-2">
            <label class="text-xs text-[var(--color-muted)]">
              每秒请求数
              <Input v-model.number="rate.tmdb_per_second" type="number" min="1" max="40" class="mt-1" />
            </label>
            <label class="text-xs text-[var(--color-muted)]">
              瞬时突发
              <Input v-model.number="rate.tmdb_burst" type="number" min="1" class="mt-1" />
            </label>
          </div>
          <label v-if="rate.tmdb_enable" class="mt-2 flex items-center gap-2 text-[13px]">
            <input v-model="rate.tmdb_shared" type="checkbox" />
            同一 Key 还被其他应用共用（自动减半）
          </label>
          <p class="mt-auto pt-2 text-xs text-[var(--color-muted)]">
            官方保护阈值约 40 次/秒（软限制，随时可能调整）；默认 4 次/秒，留出十倍余量。
            收到 429 会按 Retry-After 自动退让。当前生效：{{ tmdbEffective }} 次/秒
          </p>
        </div>

        <!-- Telegram -->
        <div class="flex flex-col rounded-lg border border-[var(--color-line)] p-3">
          <div class="flex items-center justify-between">
            <label class="flex items-center gap-1.5 text-[13px] font-medium">
              <Send class="h-4 w-4" />
              Telegram 发送间隔
            </label>
            <label class="flex items-center gap-1.5 text-[13px] text-[var(--color-muted)]">
              <input v-model="rate.tg_enable" type="checkbox" />
              启用
            </label>
          </div>
          <div v-if="rate.tg_enable" class="mt-3">
            <label class="text-xs text-[var(--color-muted)]">
              间隔（毫秒）
              <Input v-model.number="rate.tg_interval_ms" type="number" min="0" step="100" class="mt-1" />
            </label>
            <div class="mt-2 space-y-1.5">
              <label class="flex items-center gap-2 text-[13px]">
                <input v-model="rate.tg_channel_only" type="checkbox" />
                只推送频道（不含群组）
              </label>
              <label class="flex items-center gap-2 text-[13px]">
                <input v-model="rate.tg_shared" type="checkbox" />
                同一 Bot Token 还被其他应用共用（间隔 ×1.5）
              </label>
              <label class="flex items-center gap-2 text-[13px]">
                <input v-model="rate.tg_paid" type="checkbox" />
                已启用付费广播（放宽下限）
              </label>
            </div>
          </div>
          <p class="mt-auto pt-2 text-xs text-[var(--color-muted)]">
            官方要求同一会话不超过 1 条/秒、群组不超过 20 条/分钟。默认 3100ms ≈ 19 条/分钟；
            仅频道可按官方建议调到 1100ms。海报失败后的文本回退同样计入限流。
            当前生效：{{ tgEffective }} ms（约 {{ tgPerMinute }} 条/分钟）
          </p>
        </div>

        <!-- 光鸭 -->
        <div class="flex flex-col rounded-lg border border-[var(--color-line)] p-3">
          <div class="flex items-center justify-between">
            <label class="flex items-center gap-1.5 text-[13px] font-medium">
              <Zap class="h-4 w-4" />
              光鸭分享检测
            </label>
            <label class="flex items-center gap-1.5 text-[13px] text-[var(--color-muted)]">
              <input v-model="rate.gy_enable" type="checkbox" />
              启用
            </label>
          </div>
          <div v-if="rate.gy_enable" class="mt-3">
            <label class="text-xs text-[var(--color-muted)]">
              并发上限
              <Input v-model.number="rate.gy_concurrency" type="number" min="1" max="16" class="mt-1" />
            </label>
          </div>
          <p class="mt-auto pt-2 text-xs text-[var(--color-muted)]">
            光鸭尚未公布官方配额，保守限制并发；当前 {{ rate.gy_enable ? rate.gy_concurrency : '不限' }}
          </p>
        </div>
      </div>
    </Card>

    <Card
      class="lg:col-span-2"
      title="运行日志"
      desc="启动配置、刮削与推送结果都写在这里，排查问题时可直接下载"
    >
      <div class="flex flex-wrap items-center gap-3">
        <a
          href="/api/logs/download"
          class="inline-flex items-center gap-2 rounded-lg border border-[var(--color-line)] px-4 py-2 text-[13px] font-medium transition-colors hover:bg-[var(--color-surface-hover)]"
        >
          <Download class="h-4 w-4" />
          下载当前日志
        </a>
        <p class="text-xs text-[var(--color-muted)]">
          单文件上限 5 MB，轮转保留 3 份历史，文件位于数据目录的 logs/ 下
        </p>
      </div>
    </Card>
  </div>
</template>
