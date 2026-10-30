<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Cloud, Loader2, AlertCircle, Check, Key, Globe, Settings, User } from 'lucide-vue-next'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import { api } from '../lib/api'

const step = ref(1)
const totalSteps = 3
const submitting = ref(false)
const error = ref('')

// 步骤 1: 管理员账号
const username = ref('admin')
const password = ref('')
const confirmPassword = ref('')
const displayName = ref('管理员')

// 步骤 2: 访问地址与 TMDB 配置
// 默认取浏览器正在用的地址：走反代或隧道时这就是用户真实访问的域名，
// 留空会让同源校验按被改写过的 Host 判断，提交类接口会一律 403。
const publicURL = ref(location.origin)
const tmdbApiKey = ref('')

// 步骤 3: 完成
const installSuccess = ref(false)

function nextStep() {
  error.value = ''
  if (step.value === 1) {
    if (!username.value.trim()) {
      error.value = '请输入用户名'
      return
    }
    if (username.value.trim().length < 3) {
      error.value = '用户名至少 3 个字符'
      return
    }
    if (!password.value || password.value.length < 8) {
      error.value = '密码至少 8 位'
      return
    }
    if (password.value !== confirmPassword.value) {
      error.value = '两次输入的密码不一致'
      return
    }
  }
  step.value++
}

function prevStep() {
  error.value = ''
  if (step.value > 1) step.value--
}

async function submit() {
  if (submitting.value) return
  error.value = ''
  submitting.value = true
  try {
    await api.setup({
      username: username.value.trim(),
      password: password.value,
      display_name: displayName.value.trim() || '管理员',
      public_url: publicURL.value.trim(),
      tmdb_api_key: tmdbApiKey.value.trim(),
    })
    installSuccess.value = true
    step.value = 3
  } catch (e) {
    error.value = e instanceof Error ? e.message : '安装失败'
  } finally {
    submitting.value = false
  }
}

function goToLogin() {
  location.replace('/login')
}

onMounted(async () => {
  document.title = '安装向导 · 光鸭 GO'
  // 检查是否已安装
  try {
    const status = await api.setupStatus()
    if (status.installed) {
      location.replace('/login')
    }
  } catch {
    // 接口异常继续
  }
})
</script>

<template>
  <div class="flex min-h-screen items-center justify-center px-6 py-10">
    <div class="w-full max-w-md">
      <!-- 标题 -->
      <div class="mb-6 flex flex-col items-center text-center">
        <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-[var(--color-brand)]">
          <Cloud class="h-7 w-7 text-white" />
        </div>
        <h1 class="mt-4 text-2xl font-semibold">光鸭 GO</h1>
        <p class="mt-2 text-sm text-[var(--color-muted)]">欢迎！请完成以下设置开始使用</p>
      </div>

      <!-- 步骤指示器 -->
      <div class="mb-6 flex items-center justify-center gap-2">
        <template v-for="i in totalSteps" :key="i">
          <div
            class="flex h-8 w-8 items-center justify-center rounded-full text-xs font-medium transition-colors"
            :class="[
              i < step ? 'bg-[var(--color-good)] text-white' :
              i === step ? 'bg-[var(--color-brand)] text-white' :
              'bg-[var(--color-fill)] text-[var(--color-muted)]'
            ]"
          >
            <Check v-if="i < step" class="h-4 w-4" />
            <span v-else>{{ i }}</span>
          </div>
          <div
            v-if="i < totalSteps"
            class="h-0.5 w-8 transition-colors"
            :class="i < step ? 'bg-[var(--color-good)]' : 'bg-[var(--color-fill)]'"
          />
        </template>
      </div>

      <!-- 卡片 -->
      <div class="rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] p-6 shadow-[0_1px_3px_rgba(20,20,20,0.05)]">
        <!-- 步骤 1: 管理员账号 -->
        <div v-if="step === 1">
          <div class="mb-4 flex items-center gap-2">
            <User class="h-5 w-5 text-[var(--color-brand)]" />
            <h2 class="text-base font-medium">设置管理员账号</h2>
          </div>
          <p class="mb-5 text-sm text-[var(--color-muted)]">
            这是您的第一个登录账号，拥有管理员权限
          </p>

          <div class="space-y-4">
            <div>
              <label class="mb-1.5 block text-sm font-medium" for="setup-username">用户名</label>
              <Input id="setup-username" v-model="username" placeholder="admin" autocomplete="username" />
              <p class="mt-1 text-xs text-[var(--color-faint)]">3-32 位，仅限字母、数字、下划线、点和短横线</p>
            </div>

            <div>
              <label class="mb-1.5 block text-sm font-medium" for="setup-displayname">显示名称</label>
              <Input id="setup-displayname" v-model="displayName" placeholder="管理员" />
            </div>

            <div>
              <label class="mb-1.5 block text-sm font-medium" for="setup-password">密码</label>
              <Input
                id="setup-password"
                v-model="password"
                type="password"
                placeholder="至少 8 位"
                autocomplete="new-password"
              />
            </div>

            <div>
              <label class="mb-1.5 block text-sm font-medium" for="setup-confirm">确认密码</label>
              <Input
                id="setup-confirm"
                v-model="confirmPassword"
                type="password"
                placeholder="再次输入密码"
                autocomplete="new-password"
              />
            </div>
          </div>
        </div>

        <!-- 步骤 2: 访问地址与 TMDB 配置 -->
        <div v-if="step === 2">
          <div class="mb-4 flex items-center gap-2">
            <Settings class="h-5 w-5 text-[var(--color-brand)]" />
            <h2 class="text-base font-medium">配置访问地址与 TMDB</h2>
          </div>
          <p class="mb-5 text-sm text-[var(--color-muted)]">
            访问地址用于反代/隧道下的同源校验，TMDB 用于自动刮削元数据与海报
          </p>

          <div class="space-y-4">
            <div>
              <label class="mb-1.5 flex items-center gap-1.5 text-sm font-medium" for="setup-public-url">
                <Globe class="h-3.5 w-3.5" />
                对外访问地址
              </label>
              <Input
                id="setup-public-url"
                v-model="publicURL"
                placeholder="https://gy.example.com"
                autocomplete="off"
              />
              <p class="mt-1 text-xs text-[var(--color-faint)]">
                已按当前地址填好；用域名或隧道访问请保持这个值，本机直连可留空
              </p>
            </div>

            <div>
              <label class="mb-1.5 block text-sm font-medium" for="setup-tmdb">
                <div class="flex items-center gap-1.5">
                  <Key class="h-3.5 w-3.5" />
                  TMDB API Key
                </div>
              </label>
              <Input
                id="setup-tmdb"
                v-model="tmdbApiKey"
                type="password"
                placeholder="留空则稍后在设置中配置"
                autocomplete="off"
              />
              <p class="mt-1 text-xs text-[var(--color-faint)]">
                在
                <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener noreferrer" class="text-[var(--color-brand)] hover:underline">
                  themoviedb.org
                </a>
                申请 API Key
              </p>
            </div>
          </div>
        </div>

        <!-- 步骤 3: 完成 -->
        <div v-if="step === 3" class="text-center">
          <div class="mb-4 flex justify-center">
            <div class="flex h-16 w-16 items-center justify-center rounded-full bg-[var(--color-good-soft)]">
              <Check class="h-8 w-8 text-[var(--color-good)]" />
            </div>
          </div>
          <h2 class="mb-2 text-lg font-medium">安装完成！</h2>
          <p class="mb-6 text-sm text-[var(--color-muted)]">
            系统已准备就绪，现在可以使用管理员账号登录了
          </p>
        </div>

        <!-- 错误提示 -->
        <div
          v-if="error"
          class="mt-4 flex items-start gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2 text-sm text-[var(--color-bad)]"
        >
          <AlertCircle class="mt-0.5 h-4 w-4 shrink-0" />
          <span class="min-w-0 flex-1">{{ error }}</span>
        </div>

        <!-- 按钮 -->
        <div class="mt-6 flex gap-3">
          <Button
            v-if="step > 1 && step < 3"
            variant="outline"
            :disabled="submitting"
            @click="prevStep"
          >
            上一步
          </Button>
          <Button
            v-if="step < 2"
            class="flex-1"
            :disabled="submitting"
            @click="nextStep"
          >
            下一步
          </Button>
          <Button
            v-if="step === 2"
            class="flex-1"
            :disabled="submitting"
            @click="submit"
          >
            <Loader2 v-if="submitting" class="h-4 w-4 animate-spin" />
            完成安装
          </Button>
          <Button
            v-if="step === 3 && installSuccess"
            class="flex-1"
            @click="goToLogin"
          >
            登录后台
          </Button>
        </div>
      </div>

      <!-- 底部提示 -->
      <p class="mt-4 text-center text-xs text-[var(--color-faint)]">
        安装完成后可在设置中修改这些配置
      </p>
    </div>
  </div>
</template>
