export type Status =
  | 'pending'
  | 'review'
  | 'scraping'
  | 'scraped'
  | 'unmatched'
  | 'pushed'
  | 'push_failed'
  | 'failed'

export interface Link {
  id: number
  share_id: string
  url: string
  code: string
  source: string
  raw_title: string
  title: string
  original_title: string
  year: number
  media_type: string
  tmdb_id: number
  overview: string
  genres: string
  vote_average: number
  runtime: number
  poster_url: string
  poster_path: string
  shared_at: string
  share_status: string
  status: Status
  error: string
  tg_message_id: number
  created_at: string
  updated_at: string
}

export interface ListResponse {
  total: number
  items: Link[]
}

export interface TaskProgress {
  running: boolean
  kind: string
  total: number
  done: number
  failed: number
  current: string
  error: string
}

export interface ProxyState {
  enable: boolean
  addr: string
  username: string
  has_password: boolean
}

export interface Settings {
  tmdb_configured: boolean
  telegram_configured: boolean
  chat_id: string
  auto_push: boolean
  workers: number
  proxy?: ProxyState
  rate_limit?: RateLimit
}

export interface SettingsInput {
  tmdb_api_key?: string
  tg_token?: string
  chat_id?: string
  auto_push?: boolean
  save_to_file?: boolean
  proxy?: {
    enable?: boolean
    addr?: string
    username?: string
    password?: string
  }
  rate_limit?: {
    tmdb?: { enable?: boolean; per_second?: number; burst?: number; shared_credentials?: boolean }
    telegram?: {
      enable?: boolean
      interval_ms?: number
      channel_only?: boolean
      shared_credentials?: boolean
      paid_broadcast?: boolean
    }
    guangya?: { enable?: boolean; concurrency?: number }
  }
}

export interface TestResult {
  ok: boolean
  message: string
  detail: string
  elapsed_ms: number
}

/** TMDB 条目预览：补 ID 时先查询确认，再决定是否落库刮削 */
export interface TmdbCandidate {
  media_type: string
  tmdb_id: number
  title: string
  original_title: string
  year: number
  overview: string
  genres: string
  vote_average: number
  runtime: number
  poster_url: string
  site_url: string
  media_label: string
}

export interface TmdbLookupResult {
  mode: 'id' | 'search'
  item?: TmdbCandidate
  items: TmdbCandidate[]
}

export interface TmdbLookupInput {
  tmdb_id?: number
  media_type?: string
  query?: string
  year?: number
}

/** 限流配置：默认值取 TMDB 与 Telegram 官方建议 */
export interface RateLimit {
  tmdb: {
    enable: boolean
    per_second: number
    burst: number
    shared_credentials: boolean
  }
  telegram: {
    enable: boolean
    interval_ms: number
    channel_only: boolean
    shared_credentials: boolean
    paid_broadcast: boolean
  }
  guangya: {
    enable: boolean
    concurrency: number
  }
}

export interface TestInput {
  target: 'tmdb' | 'telegram' | 'proxy'
  tmdb_api_key?: string
  tg_token?: string
  chat_id?: string
  proxy_enable?: boolean
  proxy_addr?: string
  proxy_username?: string
  proxy_password?: string
}

export interface ImportResult {
  total: number
  inserted: number
  skipped: number
  failed: { line: number; raw: string; err: string }[]
  ids: number[]
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, init)
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || `请求失败 ${res.status}`)
  }
  return res.json() as Promise<T>
}

export const api = {
  list(params: { status?: string; q?: string; limit?: number; offset?: number }) {
    const qs = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== '') qs.set(k, String(v))
    })
    return request<ListResponse>(`/links?${qs.toString()}`)
  },
  stats: () => request<Record<string, number>>('/stats'),
  importText: (text: string) =>
    request<ImportResult>('/links/text', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    }),
  importFile: (file: File) => {
    const form = new FormData()
    form.append('file', file)
    return request<ImportResult>('/links/import', { method: 'POST', body: form })
  },
  remove: (id: number) => request<{ deleted: number }>(`/links/${id}`, { method: 'DELETE' }),
  batchRemove: (ids: number[]) =>
    request<{ deleted: number }>('/links/batch-delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids }),
    }),
  exportUrl: (params: { status?: string; q?: string }) => {
    const qs = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v) qs.set(k, String(v))
    })
    return `/api/links/export?${qs.toString()}`
  },
  /** 语义状态集合：后端按一组状态做 OR 匹配 */
  scrape: (ids: number[] = [], all = false, statuses: string[] = []) =>
    request<{ queued: number }>('/scrape', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids, all, statuses }),
    }),
  scrapeOne: (id: number) => request<{ queued: number }>(`/links/${id}/scrape`, { method: 'POST' }),
  /** 手动指定 TMDB ID 与类型，重置为待刮削并可选立即入队 */
  setTmdb: (id: number, tmdbId: number, mediaType: string, scrape = true) =>
    request<{ ok: boolean }>(`/links/${id}/tmdb`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tmdb_id: tmdbId, media_type: mediaType, scrape }),
    }),
  /** 卡片文案预览，与实际推送到频道的内容一致 */
  caption: (id: number) => request<{ html: string; text: string }>(`/links/${id}/caption`),
  push: (ids: number[] = [], scraped = false, statuses: string[] = []) =>
    request<{ queued: number }>('/push', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids, scraped, statuses }),
    }),
  pushOne: (id: number) => request<{ queued: number }>(`/links/${id}/push`, { method: 'POST' }),
  /** TMDB 条目预览：只查询不写库，确认无误后再调用 setTmdb */
  lookup: (input: TmdbLookupInput) =>
    request<TmdbLookupResult>('/tmdb/lookup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }),
  tasks: () => request<TaskProgress>('/tasks'),
  settings: () => request<Settings>('/settings'),
  saveSettings: (input: SettingsInput) =>
    request<{ ok: boolean }>('/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }),
  test: (input: TestInput) =>
    request<TestResult>('/settings/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }),
}

export const statusMeta: Record<Status, { label: string; cls: string }> = {
  pending: { label: '待处理', cls: 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]' },
  review: { label: '待核查', cls: 'bg-[var(--color-warn-soft)] text-[var(--color-warn)]' },
  scraping: { label: '刮削中', cls: 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]' },
  scraped: { label: '已刮削', cls: 'bg-[var(--color-ok-soft)] text-[var(--color-ok)]' },
  unmatched: { label: '未匹配', cls: 'bg-[var(--color-warn-soft)] text-[var(--color-warn)]' },
  pushed: { label: '已推送', cls: 'bg-[var(--color-ok-soft)] text-[var(--color-ok)]' },
  push_failed: { label: '推送失败', cls: 'bg-[var(--color-bad-soft)] text-[var(--color-bad)]' },
  failed: { label: '失败', cls: 'bg-[var(--color-bad-soft)] text-[var(--color-bad)]' },
}

/** 影视类型标识：刮削结果必须能一眼看出是电影还是剧集 */
export const mediaMeta: Record<string, { label: string; cls: string }> = {
  movie: { label: '电影', cls: 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]' },
  tv: { label: '剧集', cls: 'bg-[var(--color-warn-soft)] text-[var(--color-warn)]' },
}

export function mediaLabel(mediaType?: string): string {
  return mediaMeta[mediaType ?? '']?.label ?? ''
}
