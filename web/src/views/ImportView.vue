<script setup lang="ts">
import { ref } from 'vue'
import { Upload, ClipboardPaste, Loader2, CheckCircle2, AlertTriangle, Sparkles } from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Textarea from '../components/ui/Textarea.vue'
import { api, type ImportResult } from '../lib/api'
import { toast } from '../lib/toast'

const text = ref('')
const loading = ref(false)
const result = ref<ImportResult | null>(null)
const error = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const emit = defineEmits<{ imported: []; goto: [tab: string] }>()

const scraping = ref(false)

/** 导入后直接对本次新增的记录发起刮削，仍停在录入页看进度 */
async function scrapeImported() {
  if (!result.value?.ids.length) return
  scraping.value = true
  try {
    const res = await api.scrape(result.value.ids)
    toast.success(`已加入刮削队列 ${res.queued} 条`)
    emit('imported')
    emit('goto', 'scrape')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '刮削启动失败')
  } finally {
    scraping.value = false
  }
}

async function submitText() {
  if (!text.value.trim()) {
    error.value = '请先粘贴分享内容'
    return
  }
  run(() => api.importText(text.value))
}

async function submitFile(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  run(() => api.importFile(file))
  target.value = ''
}

async function run(fn: () => Promise<ImportResult>) {
  loading.value = true
  error.value = ''
  result.value = null
  try {
    result.value = await fn()
    const r = result.value
    if (r.inserted > 0) {
      text.value = ''
      emit('imported')
      toast.success(`新增 ${r.inserted} 条${r.skipped ? `，跳过重复 ${r.skipped} 条` : ''}`)
    } else if (r.skipped > 0) {
      toast.info(`没有新增记录，${r.skipped} 条已存在`)
    }
    if (r.failed.length) toast.error(`${r.failed.length} 行解析或入库失败，见右侧明细`)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '导入失败'
  } finally {
    loading.value = false
  }
}

function pickFile() {
  fileInput.value?.click()
}
</script>

<template>
  <div class="grid gap-5 lg:grid-cols-[1.4fr_1fr]">
    <Card title="录入分享内容" desc="粘贴光鸭分享文本，或上传批量分享记录 CSV">
      <Textarea
        v-model="text"
        placeholder="光鸭云盘用户给你分享了：「片名 (2022) {tmdb-156138}」&#10;链接：https://www.guangyapan.com/s/xxx_xxx?code=abcd&#10;提取码：abcd"
      />

      <div class="mt-4 flex flex-wrap items-center gap-2">
        <Button :disabled="loading" @click="submitText">
          <Loader2 v-if="loading" class="h-4 w-4 animate-spin" />
          <ClipboardPaste v-else class="h-4 w-4" />
          解析并导入
        </Button>
        <Button variant="outline" :disabled="loading" @click="pickFile">
          <Upload class="h-4 w-4" />
          上传 CSV
        </Button>
        <input
          ref="fileInput"
          type="file"
          accept=".csv,text/csv,text/plain"
          class="hidden"
          @change="submitFile"
        />
        <span class="text-[13px] text-[var(--color-muted)]">重复链接会自动跳过</span>
      </div>

      <p v-if="error" class="mt-3 flex items-center gap-1.5 text-[13px] text-[var(--color-bad)]">
        <AlertTriangle class="h-4 w-4" />
        {{ error }}
      </p>
    </Card>

    <Card title="解析结果" desc="导入后显示新增、跳过与失败明细">
      <div v-if="!result" class="flex h-40 items-center justify-center text-[13px] text-[var(--color-faint)]">
        尚无导入记录
      </div>

      <div v-else class="space-y-4">
        <div class="grid grid-cols-3 gap-3">
          <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
            <p class="text-[13px] text-[var(--color-muted)]">新增</p>
            <p class="text-2xl font-medium text-[var(--color-ok)]">{{ result.inserted }}</p>
          </div>
          <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
            <p class="text-[13px] text-[var(--color-muted)]">重复跳过</p>
            <p class="text-2xl font-medium">{{ result.skipped }}</p>
          </div>
          <div class="rounded-lg bg-[var(--color-surface-sunken)] p-3">
            <p class="text-[13px] text-[var(--color-muted)]">失败</p>
            <p class="text-2xl font-medium" :class="result.failed.length ? 'text-[var(--color-bad)]' : ''">
              {{ result.failed.length }}
            </p>
          </div>
        </div>

        <div v-if="result.inserted" class="space-y-3">
          <div class="flex items-center gap-1.5 text-[13px] text-[var(--color-ok)]">
            <CheckCircle2 class="h-4 w-4" />
            已入库 {{ result.inserted }} 条，刮削完成后才会进入资源库
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <Button size="sm" :disabled="scraping" @click="scrapeImported">
              <Loader2 v-if="scraping" class="h-4 w-4 animate-spin" />
              <Sparkles v-else class="h-4 w-4" />
              立即刮削这 {{ result.inserted }} 条
            </Button>
            <Button size="sm" variant="ghost" @click="emit('goto', 'scrape')">稍后自己去刮削队列</Button>
          </div>
        </div>

        <ul v-if="result.failed.length" class="space-y-1.5">
          <li
            v-for="(f, i) in result.failed"
            :key="i"
            class="rounded-lg bg-[var(--color-bad-soft)] px-3 py-2 text-[13px] text-[var(--color-bad)]"
          >
            <span v-if="f.line">第 {{ f.line }} 行：</span>{{ f.err }}
          </li>
        </ul>
      </div>
    </Card>
  </div>
</template>
