<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  UsersRound,
  UserPlus,
  Loader2,
  KeyRound,
  Trash2,
  Ban,
  CheckCircle2,
  RotateCcw,
  AlertCircle,
  X,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import Input from '../components/ui/Input.vue'
import { api, type Role, type User } from '../lib/api'
import { toast } from '../lib/toast'
import { confirmAction } from '../lib/confirm'

defineProps<{ me: User }>()

const users = ref<User[]>([])
const loading = ref(false)
const loadError = ref('')
const acting = ref<number | null>(null)

const showCreate = ref(false)
const creating = ref(false)
const createError = ref('')
const form = ref({ username: '', display_name: '', password: '', role: 'operator' as Role })

const resetId = ref<number | null>(null)
const resetPwd = ref('')
const resetting = ref(false)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await api.users()
    users.value = res.items
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

function formatTime(v: string | null) {
  if (!v) return '—'
  const d = new Date(v)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 1970) return '—'
  return d.toLocaleString('zh-CN', { hour12: false })
}

function roleLabel(role: string) {
  return role === 'admin' ? '管理员' : '运营'
}

function resetForm() {
  form.value = { username: '', display_name: '', password: '', role: 'operator' }
  createError.value = ''
}

function toggleCreate() {
  showCreate.value = !showCreate.value
  resetForm()
}

async function create() {
  createError.value = ''
  const f = form.value
  if (!/^[a-zA-Z0-9_.-]{3,32}$/.test(f.username.trim())) {
    createError.value = '用户名 3-32 位，仅限字母、数字、下划线、点和短横线'
    return
  }
  if (f.password.length < 8) {
    createError.value = '密码至少 8 位'
    return
  }
  creating.value = true
  try {
    await api.createUser({
      username: f.username.trim(),
      display_name: f.display_name.trim(),
      password: f.password,
      role: f.role,
    })
    toast.success(`已创建账号 ${f.username.trim()}`)
    showCreate.value = false
    resetForm()
    await load()
  } catch (e) {
    createError.value = e instanceof Error ? e.message : '创建失败'
  } finally {
    creating.value = false
  }
}

async function changeRole(u: User, event: Event) {
  const role = (event.target as HTMLSelectElement).value as Role
  if (role === u.role) return
  acting.value = u.id
  try {
    await api.updateUser(u.id, { role })
    toast.success(`已将 ${u.username} 设为${roleLabel(role)}`)
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '修改角色失败')
    await load()
  } finally {
    acting.value = null
  }
}

async function toggleEnabled(u: User) {
  if (u.enabled) {
    const ok = await confirmAction({
      title: `停用账号 ${u.username}？`,
      message: '停用后该账号会立即退出登录且无法再登录，可随时重新启用。',
      confirmText: '停用',
      danger: true,
    })
    if (!ok) return
  }
  acting.value = u.id
  try {
    await api.updateUser(u.id, { enabled: !u.enabled })
    toast.success(u.enabled ? '账号已停用' : '账号已启用')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '操作失败')
  } finally {
    acting.value = null
  }
}

function openReset(u: User) {
  resetId.value = u.id
  resetPwd.value = ''
}

async function submitReset(u: User) {
  if (resetPwd.value.length < 8) {
    toast.error('密码至少 8 位')
    return
  }
  resetting.value = true
  try {
    await api.resetUserPassword(u.id, resetPwd.value)
    toast.success(`已重置 ${u.username} 的密码，该账号需要重新登录`)
    resetId.value = null
    resetPwd.value = ''
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '重置密码失败')
  } finally {
    resetting.value = false
  }
}

async function remove(u: User) {
  const ok = await confirmAction({
    title: `删除账号 ${u.username}？`,
    message: '删除后无法恢复，该账号的登录会话会同时失效。',
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  acting.value = u.id
  try {
    await api.deleteUser(u.id)
    toast.success('账号已删除')
    await load()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '删除失败')
  } finally {
    acting.value = null
  }
}

onMounted(load)
</script>

<template>
  <Card title="用户管理" desc="后台账号与权限：管理员可管理用户，运营只能使用录入、刮削、推送与资源库">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2 text-[13px] text-[var(--color-muted)]">
        <UsersRound class="h-4 w-4" />
        共 {{ users.length }} 个账号
      </div>
      <Button variant="outline" size="sm" @click="toggleCreate">
        <UserPlus class="h-4 w-4" />
        {{ showCreate ? '收起' : '新增用户' }}
      </Button>
    </div>

    <!-- 新增账号 -->
    <div v-if="showCreate" class="mt-4 rounded-lg border border-[var(--color-line)] bg-[var(--color-surface-sunken)] p-4">
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label class="mb-1.5 block text-[13px] font-medium">用户名</label>
          <Input v-model="form.username" placeholder="3-32 位字母或数字" autocomplete="off" />
        </div>
        <div>
          <label class="mb-1.5 block text-[13px] font-medium">显示名（可留空）</label>
          <Input v-model="form.display_name" placeholder="例如：运营一号" />
        </div>
        <div>
          <label class="mb-1.5 block text-[13px] font-medium">初始密码</label>
          <Input v-model="form.password" type="password" placeholder="至少 8 位" autocomplete="new-password" />
        </div>
        <div>
          <label class="mb-1.5 block text-[13px] font-medium">角色</label>
          <select
            v-model="form.role"
            class="h-9 w-full rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-[var(--color-surface)] px-2 text-[13px]"
          >
            <option value="operator">运营</option>
            <option value="admin">管理员</option>
          </select>
        </div>
      </div>
      <p v-if="createError" class="mt-3 text-[13px] text-[var(--color-bad)]">{{ createError }}</p>
      <div class="mt-3 flex items-center gap-2">
        <Button size="sm" :disabled="creating" @click="create">
          <Loader2 v-if="creating" class="h-3.5 w-3.5 animate-spin" />
          <UserPlus v-else class="h-3.5 w-3.5" />
          创建账号
        </Button>
        <Button variant="ghost" size="sm" @click="toggleCreate">
          <X class="h-3.5 w-3.5" />
          取消
        </Button>
      </div>
    </div>

    <div
      v-if="loadError"
      class="mt-4 flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]"
    >
      <AlertCircle class="h-4 w-4 shrink-0" />
      <span class="min-w-0 flex-1">加载失败：{{ loadError }}</span>
      <Button size="sm" variant="outline" @click="load">
        <RotateCcw class="h-3.5 w-3.5" />
        重试
      </Button>
    </div>

    <div v-if="loading" class="py-10 text-center text-[var(--color-muted)]">
      <Loader2 class="mx-auto h-5 w-5 animate-spin" />
    </div>

    <div v-else class="mt-4 overflow-x-auto">
      <table class="w-full border-collapse text-[13px]">
        <thead>
          <tr class="border-b border-[var(--color-line)] text-left text-[var(--color-muted)]">
            <th class="py-2 pr-4 font-medium">用户名</th>
            <th class="py-2 pr-4 font-medium">显示名</th>
            <th class="w-28 py-2 pr-4 font-medium">角色</th>
            <th class="w-20 py-2 pr-4 font-medium">状态</th>
            <th class="w-40 py-2 pr-4 font-medium">最近登录</th>
            <th class="w-40 py-2 pr-4 font-medium">创建时间</th>
            <th class="w-44 py-2 font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="u in users" :key="u.id">
            <tr class="border-b border-[var(--color-line)]">
              <td class="py-2.5 pr-4">
                <span class="font-medium">{{ u.username }}</span>
                <span v-if="u.id === me.id" class="ml-1.5 text-xs text-[var(--color-brand)]">当前</span>
              </td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ u.display_name || '—' }}</td>
              <td class="py-2.5 pr-4">
                <select
                  :value="u.role"
                  :disabled="u.id === me.id || acting === u.id"
                  class="h-7 rounded-[var(--radius-control)] border border-[var(--color-line-strong)] bg-transparent px-1.5 text-[13px] disabled:opacity-50"
                  @change="changeRole(u, $event)"
                >
                  <option value="operator">运营</option>
                  <option value="admin">管理员</option>
                </select>
              </td>
              <td class="py-2.5 pr-4">
                <Badge
                  :class="
                    u.enabled
                      ? 'bg-[var(--color-ok-soft)] text-[var(--color-ok)]'
                      : 'bg-[var(--color-surface-hover)] text-[var(--color-muted)]'
                  "
                >
                  {{ u.enabled ? '已启用' : '已停用' }}
                </Badge>
              </td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ formatTime(u.last_login_at) }}</td>
              <td class="py-2.5 pr-4 text-[var(--color-muted)]">{{ formatTime(u.created_at) }}</td>
              <td class="py-2.5">
                <div class="flex items-center gap-1">
                  <button
                    class="flex items-center gap-1 rounded px-1.5 py-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-brand)] disabled:opacity-40"
                    :disabled="acting === u.id"
                    @click="openReset(u)"
                  >
                    <KeyRound class="h-3.5 w-3.5" />
                    重置密码
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] disabled:pointer-events-none disabled:opacity-30"
                    :title="u.enabled ? '停用账号' : '启用账号'"
                    :disabled="u.id === me.id || acting === u.id"
                    @click="toggleEnabled(u)"
                  >
                    <Ban v-if="u.enabled" class="h-3.5 w-3.5" />
                    <CheckCircle2 v-else class="h-3.5 w-3.5" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-bad)] disabled:pointer-events-none disabled:opacity-30"
                    title="删除账号"
                    :disabled="u.id === me.id || acting === u.id"
                    @click="remove(u)"
                  >
                    <Trash2 class="h-3.5 w-3.5" />
                  </button>
                </div>
              </td>
            </tr>
            <!-- 重置密码：就地展开输入 -->
            <tr v-if="resetId === u.id" class="border-b border-[var(--color-line)] bg-[var(--color-surface-sunken)]">
              <td colspan="7" class="px-3 py-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-[13px] text-[var(--color-muted)]">为 {{ u.username }} 设置新密码</span>
                  <Input
                    v-model="resetPwd"
                    class="max-w-52"
                    type="password"
                    placeholder="至少 8 位"
                    autocomplete="new-password"
                    @keyup.enter="submitReset(u)"
                  />
                  <Button size="sm" :disabled="resetting" @click="submitReset(u)">
                    <Loader2 v-if="resetting" class="h-3.5 w-3.5 animate-spin" />
                    确认重置
                  </Button>
                  <Button variant="ghost" size="sm" @click="resetId = null">取消</Button>
                </div>
                <p class="mt-1.5 text-xs text-[var(--color-muted)]">重置后该账号的所有登录会话会立即失效。</p>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </Card>
</template>
